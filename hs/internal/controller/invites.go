// invites.go -- the invitation service (source spec §6 requirements):
// cryptographically random code (256-bit), hash-only storage, one-time atomic
// redemption, expiry, explicit revocation, rate limiting, audit.
package controller

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// HeadscaleAPI is the control-plane capability hs-controller needs; concrete
// *Headscale drives the CLI, tests use a fake.
type HeadscaleAPI interface {
	CreatePreAuthKey() (string, error)
	ExpireNode(hostname string) error
}

var (
	ErrNoInvitation   = errors.New("unknown invitation code")
	ErrUsed           = errors.New("invitation already used")
	ErrExpired        = errors.New("invitation expired")
	ErrRevoked        = errors.New("invitation revoked")
	ErrNodeExists     = errors.New("node hostname already registered")
	ErrNodeRevoked    = errors.New("node is revoked")
	ErrBadSecret      = errors.New("node secret rejected")
	ErrRateLimited    = errors.New("rate limited")
	ErrNotImplemented = errors.New("not implemented")
)

// RandomToken returns a 256-bit base64url token (43 chars, no padding).
func RandomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func shaHex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// CreateInvitation mints a one-time invitation. The code is returned exactly
// once and never stored in plaintext.
func (s *Store) CreateInvitation(label, profile string, ttl time.Duration, actor string) (code string, inv *Invitation, err error) {
	code, err = RandomToken()
	if err != nil {
		return "", nil, err
	}
	id := "inv-" + shaHex(code)[:12]
	inv = &Invitation{
		ID:        id,
		CodeSHA:   shaHex(code),
		Label:     label,
		Profile:   profile,
		State:     InvCreated,
		CreatedAt: time.Now().UTC(),
		ExpiresAt: time.Now().UTC().Add(ttl),
	}
	err = s.mutate("invite.create", actor, "id="+id+" label="+label+" profile="+profile, func() error {
		s.data.Invitations[id] = inv
		return nil
	})
	if err != nil {
		return "", nil, err
	}
	return code, inv, nil
}

// RevokeInvitation invalidates an unused invitation (used ones are already
// consumed and stay for audit).
func (s *Store) RevokeInvitation(id, actor string) error {
	return s.mutate("invite.revoke", actor, "id="+id, func() error {
		inv, ok := s.data.Invitations[id]
		if !ok {
			return ErrNoInvitation
		}
		if inv.State == InvUsed {
			return ErrUsed
		}
		inv.State = InvFailed
		return nil
	})
}

// RedeemResult is what a node gets from a successful redeem.
type RedeemResult struct {
	NodeID string
	// returned once; stored hashed
	NodeSecret string
	// single-use, short-lived (headscale)
	PreauthKey string
	// control plane URL the node registers against
	Server       string
	InvitationID string
}

// Redeem is the atomic one-time redemption (N5': concurrent redeems -- exactly
// one wins). hostname becomes the node id and must be unique among live nodes.
func (s *Store) Redeem(code, hostname string, hs HeadscaleAPI) (*RedeemResult, error) {
	var result *RedeemResult
	err := s.mutate("invite.redeem", "node:"+hostname, "hostname="+hostname, func() error {
		// sweep expired invitations first so state stays honest
		now := time.Now().UTC()
		for _, inv := range s.data.Invitations {
			if inv.State == InvCreated && now.After(inv.ExpiresAt) {
				inv.State = InvFailed
			}
		}
		sha := shaHex(code)
		var inv *Invitation
		for _, cand := range s.data.Invitations {
			if cand.CodeSHA == sha {
				inv = cand
				break
			}
		}
		if inv == nil {
			return ErrNoInvitation
		}
		switch inv.State {
		case InvUsed, InvRedeemed:
			return ErrUsed
		case InvFailed:
			if now.After(inv.ExpiresAt) {
				return ErrExpired
			}
			return ErrRevoked
		}
		if now.After(inv.ExpiresAt) {
			inv.State = InvFailed
			return ErrExpired
		}
		if _, exists := s.data.Nodes[hostname]; exists {
			return ErrNodeExists
		}

		// mint the node secret (machine-held resume credential)
		secret, err := RandomToken()
		if err != nil {
			return err
		}
		// mint the single-use preauth key through headscale
		key, err := hs.CreatePreAuthKey()
		if err != nil {
			return fmt.Errorf("headscale preauth key: %w", err)
		}

		inv.State = InvRedeemed
		inv.NodeID = hostname
		node := &NodeRecord{
			ID:            hostname,
			State:         NodePending,
			Hostname:      hostname,
			NodeSecretSHA: shaHex(secret),
			Profile:       inv.Profile,
			InvitationID:  inv.ID,
			CreatedAt:     now,
		}
		s.data.Nodes[hostname] = node
		result = &RedeemResult{
			NodeID:       hostname,
			NodeSecret:   secret,
			PreauthKey:   key,
			Server:       hs.ControlURL,
			InvitationID: inv.ID,
		}
		return nil
	})
	return result, err
}

// Resume is the crash-safe continuation path (ADR-002): a node that already
// redeemed (or whose redeem crashed before first contact) presents its node
// secret and receives a fresh single-use preauth key. First successful resume
// moves the node pending -> active. Revoked nodes are refused (N7').
func (s *Store) Resume(hostname, secret string, hs HeadscaleAPI) (string, error) {
	var key string
	err := s.mutate("node.resume", "node:"+hostname, "hostname="+hostname, func() error {
		node, ok := s.data.Nodes[hostname]
		if !ok {
			return ErrNoInvitation
		}
		if node.State == NodeRevoked {
			return ErrNodeRevoked
		}
		if node.NodeSecretSHA != shaHex(secret) {
			return ErrBadSecret
		}
		k, err := hs.CreatePreAuthKey()
		if err != nil {
			return fmt.Errorf("headscale preauth key: %w", err)
		}
		key = k
		now := time.Now().UTC()
		node.LastResumeAt = now
		if node.State == NodePending {
			node.State = NodeActive
			node.FirstSeenAt = now
		}
		return nil
	})
	return key, err
}

// RevokeNode revokes a node: tombstone + its unused invitations fail (N7':
// a revoked node must not be revivable through an old invitation).
func (s *Store) RevokeNode(id, actor string, hs HeadscaleAPI) error {
	return s.mutate("node.revoke", actor, "id="+id, func() error {
		node, ok := s.data.Nodes[id]
		if !ok {
			return ErrNoInvitation
		}
		if node.State == NodeRevoked {
			return nil // idempotent
		}
		node.State = NodeRevoked
		node.RevokedAt = time.Now().UTC()
		for _, inv := range s.data.Invitations {
			if inv.NodeID == id && (inv.State == InvCreated || inv.State == InvRedeemed) {
				inv.State = InvFailed
			}
		}
		return hs.ExpireNode(id)
	})
}
