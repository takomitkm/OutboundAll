// Package controller implements the hs-controller management plane: the
// invitation service, node registry, resume-key channel and the admin API.
//
// Identity separation (ADR-002): invitation code (one-time, human-carried)
// != node secret (machine-held, resume only) != preauth key (headscale-side,
// short-lived single-use). The store never holds plaintext tokens -- only
// SHA-256 hashes.
//
// Persistence: JSON file under a lock with atomic writes and timestamped
// backups -- the same pattern as the reference relay registry.json
// (PROTOCOL.md §3). SQLite was named in PLAN.md; the repo is stdlib-only, so
// the JSON store is the dependency-free equivalent for a single VPS
// (deviation recorded in IMPLEMENTATION_PROGRESS.md; SQLite remains the
// option if scale ever demands it).
package controller

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/takomitkm/dialhome/internal/lockfile"
)

// Store is the management-plane state: nodes, invitations, audit events.
type Store struct {
	path  string
	lockP string
	data  data
}

type data struct {
	Version int `json:"version"`
	// by node id
	Nodes map[string]*NodeRecord `json:"nodes"`
	// by invitation id
	Invitations map[string]*Invitation `json:"invitations"`
	AdminToken  string                 `json:"admin_token_sha256"`
	Headscale   HeadscaleSettings      `json:"headscale"`
	Audit       []AuditEvent           `json:"audit"`
}

// HeadscaleSettings pins how hs-controller talks to the local headscale.
type HeadscaleSettings struct {
	BinPath    string `json:"bin_path"`
	ConfigPath string `json:"config_path"`
	UserID     uint64 `json:"user_id"`
	// RFC3339 of last id check
	UserCacheAt string `json:"user_cache_at,omitempty"`
}

// NodeState is the node lifecycle (ADR-002).
type NodeState string

const (
	NodePending NodeState = "pending" // record created at redeem; node not yet seen
	NodeActive  NodeState = "active"  // node completed first contact (resume)
	NodeRevoked NodeState = "revoked" // tombstone kept for audit
)

// NodeRecord is one enrolled machine.
type NodeRecord struct {
	// = hostname (uniqueness enforced)
	ID              string    `json:"id"`
	State           NodeState `json:"state"`
	Hostname        string    `json:"hostname"`
	NodeSecretSHA   string    `json:"node_secret_sha256"`
	Profile         string    `json:"profile"`
	InvitationID    string    `json:"invitation_id"`
	CreatedAt       time.Time `json:"created_at"`
	FirstSeenAt     time.Time `json:"first_seen_at,omitempty"`
	LastResumeAt    time.Time `json:"last_resume_at,omitempty"`
	RevokedAt       time.Time `json:"revoked_at,omitempty"`
	HeadscaleNodeID uint64    `json:"headscale_node_id,omitempty"`
}

// InvitationState is the invitation lifecycle (ADR-002 crash-safe chain).
type InvitationState string

const (
	InvCreated  InvitationState = "created"
	InvRedeemed InvitationState = "redeemed"
	InvUsed     InvitationState = "used"   // node completed first contact
	InvFailed   InvitationState = "failed" // expired or revoked before use
)

// Invitation is a one-time enrollment invitation.
type Invitation struct {
	ID string `json:"id"`
	// never the code itself
	CodeSHA   string          `json:"code_sha256"`
	Label     string          `json:"label,omitempty"`
	Profile   string          `json:"profile,omitempty"`
	State     InvitationState `json:"state"`
	CreatedAt time.Time       `json:"created_at"`
	ExpiresAt time.Time       `json:"expires_at"`
	UsedAt    time.Time       `json:"used_at,omitempty"`
	NodeID    string          `json:"node_id,omitempty"`
}

// AuditEvent is one management-plane action (who/what/when, no secrets).
type AuditEvent struct {
	At time.Time `json:"at"`
	// invite.create / invite.redeem / node.resume / node.revoke / invite.expire ...
	Kind   string `json:"kind"`
	Actor  string `json:"actor"`
	Detail string `json:"detail,omitempty"`
}

// Open loads (or initializes) the store at dir/registry.json.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	s := &Store{
		path:  filepath.Join(dir, "registry.json"),
		lockP: filepath.Join(dir, "registry.lock"),
	}
	b, err := os.ReadFile(s.path)
	switch {
	case os.IsNotExist(err):
		s.data = data{Version: 1, Nodes: map[string]*NodeRecord{}, Invitations: map[string]*Invitation{}}
		if err := s.flushNoLock(); err != nil {
			return nil, err
		}
	case err != nil:
		return nil, err
	default:
		if err := json.Unmarshal(b, &s.data); err != nil {
			return nil, fmt.Errorf("parse %s: %w (refusing to overwrite; restore from a backup)", s.path, err)
		}
		if s.data.Nodes == nil {
			s.data.Nodes = map[string]*NodeRecord{}
		}
		if s.data.Invitations == nil {
			s.data.Invitations = map[string]*Invitation{}
		}
	}
	return s, nil
}

// lockWaitBounds bounds how long a mutate waits for the store lock. The
// windows lockfile implementation serializes lock operations through a
// package mutex and a blocking Acquire inside that mutex deadlocks every
// other holder (LIVE-RED: TestRedeemAtomicUnderConcurrency 10-min timeout on
// windows-latest) -- so the store never blocks: it retries TryAcquire.
const (
	storeLockWait    = 15 * time.Second
	storeLockRetries = 60
)

// acquireStoreLock takes the store lock with bounded retries.
func (s *Store) acquireStoreLock() (*lockfile.Lock, error) {
	deadline := time.Now().Add(storeLockWait)
	for i := 0; i < storeLockRetries; i++ {
		lock, err := lockfile.TryAcquire(s.lockP)
		if err == nil {
			return lock, nil
		}
		if !errors.Is(err, lockfile.ErrLocked) {
			return nil, err
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("store lock busy after %v (concurrent mutation?)", storeLockWait)
		}
		time.Sleep(250 * time.Millisecond)
	}
	return nil, fmt.Errorf("store lock busy")
}

// mutate runs fn under the store lock, appends the audit event and persists.
// The lock is taken once here -- re-acquiring inside would deadlock (flock is
// per open file description).
func (s *Store) mutate(kind, actor, detail string, fn func() error) error {
	lock, err := s.acquireStoreLock()
	if err != nil {
		return err
	}
	defer lock.Release()
	if err := fn(); err != nil {
		return err
	}
	s.data.Audit = append(s.data.Audit, AuditEvent{At: time.Now().UTC(), Kind: kind, Actor: actor, Detail: detail})
	if len(s.data.Audit) > AuditMax {
		s.data.Audit = s.data.Audit[len(s.data.Audit)-AuditMax:]
	}
	return s.flushNoLock()
}

// flushNoLock atomically writes the store (tmp+rename in the same dir),
// leaving a timestamped backup like the reference relay does. Caller holds
// the lock.
func (s *Store) flushNoLock() error {
	b, err := json.MarshalIndent(&s.data, "", "  ")
	if err != nil {
		return err
	}
	if _, err := os.Stat(s.path); err == nil {
		bak := s.path + ".bak-" + time.Now().UTC().Format("20060102-150405")
		if rb, err := os.ReadFile(s.path); err == nil {
			_ = os.WriteFile(bak, rb, 0o600)
		}
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// SetAdminToken stores the admin token hash (init only).
func (s *Store) SetAdminToken(shaHexStr string) error {
	return s.mutate("admin.token.set", "init", "", func() error {
		s.data.AdminToken = shaHexStr
		return nil
	})
}

// AdminTokenSHA returns the stored admin token hash ("" if unset).
func (s *Store) AdminTokenSHA() string {
	return s.data.AdminToken
}

// AuditMax caps the audit trail (oldest events dropped first). The audit is
// a management-plane record, not an infinite log: unbounded growth on a
// 1 GB VPS is a self-inflicted DoS.
const AuditMax = 10000

// ErrNotRevoked guards purge: only tombstones can be removed.
var ErrNotRevoked = errors.New("only revoked nodes can be purged")

// PurgeNode deletes a REVOKED node record (frees the hostname). Active and
// pending records are refused -- the reference implementation's relay purge
// semantics (RELAY_MANUAL §4.1: refusing to purge a live node).
func (s *Store) PurgeNode(id, actor string) error {
	return s.mutate("node.purge", actor, "id="+id, func() error {
		node, ok := s.data.Nodes[id]
		if !ok {
			return ErrNoInvitation
		}
		if node.State != NodeRevoked {
			return ErrNotRevoked
		}
		delete(s.data.Nodes, id)
		return nil
	})
}

// Audit returns the audit trail (oldest first, capped read view).
func (s *Store) Audit() []AuditEvent {
	out := make([]AuditEvent, len(s.data.Audit))
	copy(out, s.data.Audit)
	sort.Slice(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}

// Counts renders the health counters.
func (s *Store) Counts() (nodes, active, invitations int) {
	for _, n := range s.data.Nodes {
		nodes++
		if n.State == NodeActive {
			active++
		}
	}
	invitations = len(s.data.Invitations)
	return nodes, active, invitations
}

// Snapshot returns a deep read of the store for API rendering (no secrets:
// hashes only by construction).
func (s *Store) Snapshot() ([]*NodeRecord, []*Invitation) {
	nodes := make([]*NodeRecord, 0, len(s.data.Nodes))
	for _, n := range s.data.Nodes {
		nodes = append(nodes, n)
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	invs := make([]*Invitation, 0, len(s.data.Invitations))
	for _, v := range s.data.Invitations {
		invs = append(invs, v)
	}
	sort.Slice(invs, func(i, j int) bool { return invs[i].CreatedAt.Before(invs[j].CreatedAt) })
	return nodes, invs
}

// Node returns one record (read-only copy of the pointer; callers must not
// mutate without mutate()).
func (s *Store) Node(id string) (*NodeRecord, bool) {
	n, ok := s.data.Nodes[id]
	return n, ok
}

// SSHEnabled reports whether the node's profile grants controller SSH access
// (ADR-003: per-node, deny-by-default). "no-ssh" is the opt-out.
func (n *NodeRecord) SSHEnabled() bool {
	return n != nil && n.State != NodeRevoked && n.Profile != "no-ssh"
}
