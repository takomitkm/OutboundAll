package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/takomitkm/dialhome/internal/relay"
)

func newTestStore(t *testing.T) *relay.Store {
	t.Helper()
	dir := t.TempDir()
	return relay.NewStore(filepath.Join(dir, "registry.json"), filepath.Join(dir, "registry.lock"))
}

// TestMigrateLegacyIdempotentKeepsAKByteForByte locks the 2026-11-15 regression:
// re-running migrate on already-migrated lines must NOT rewrite authorized_keys
// (an unconditional Render appended a blank line on every run).
func TestMigrateLegacyIdempotentKeepsAKByteForByte(t *testing.T) {
	dir := t.TempDir()
	ak := filepath.Join(dir, "authorized_keys")
	line := `restrict,command="/x/bin/dialhome relay-helper --port 13437 tunnel",port-forwarding,permitlisten="127.0.0.1:13437",permitlisten="127.0.0.1:13435",permitopen="127.0.0.1:1" ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIHiXedYmFKT4g79Lh4AUkRVOJ1GOiOMugfq01ybRzyaW dialhome-node-win2` + "\n# foreign line preserved\n"
	if err := os.WriteFile(ak, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(ak)
	base := filepath.Join(dir, "relay")
	os.MkdirAll(base, 0o700)
	st := relay.NewStore(filepath.Join(base, "registry.json"), filepath.Join(base, "registry.lock"))
	if err := relayMigrateLegacy(st, ak, "/x/bin/dialhome", base, filepath.Join(dir, "legacy")); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(ak)
	if string(before) != string(after) {
		t.Fatalf("AK rewritten on a no-op migrate:\nbefore=%q\nafter =%q", before, after)
	}
	// no backup should be produced for a no-op
	matches, _ := filepath.Glob(ak + ".bak-*")
	if len(matches) != 0 {
		t.Fatalf("no-op migrate created a backup: %v", matches)
	}
	r, _ := st.Load()
	if n := r.Nodes["win2"]; n == nil || n.State != "active" || n.Ports.SSH != 13437 || n.Ports.App != 13435 {
		t.Fatalf("registry not restored: %+v", n)
	}
}

// TestPurgeRefusesActiveNode locks the safety rule: a live node must never be
// purged (that would free its port pair for another enrollment).
func TestPurgeRefusesActiveNode(t *testing.T) {
	st := newTestStore(t)
	if err := st.WithLock(func(r *relay.Registry) error {
		r.Nodes["live"] = &relay.NodeRecord{State: "active", Ports: relay.Ports{SSH: 13441, App: 13440}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	err := relayPurge(st, "live")
	if err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("active node purge must be refused, got %v", err)
	}
	r, _ := st.Load()
	if r.Nodes["live"] == nil {
		t.Fatal("active node was removed")
	}
}

// TestPurgeRemovesRevokedTombstone verifies that a revoked node's record and
// its bound credentials are removed (used to clean E2E leftovers on the relay).
func TestPurgeRemovesRevokedTombstone(t *testing.T) {
	st := newTestStore(t)
	if err := st.WithLock(func(r *relay.Registry) error {
		r.Nodes["gone"] = &relay.NodeRecord{State: "revoked", Ports: relay.Ports{SSH: 13441, App: 13440}}
		r.Credentials["cred-x"] = &relay.Credential{Node: "gone"}
		r.Credentials["cred-keep"] = &relay.Credential{Node: "other"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := relayPurge(st, "gone"); err != nil {
		t.Fatal(err)
	}
	r, _ := st.Load()
	if r.Nodes["gone"] != nil {
		t.Fatal("revoked tombstone still present")
	}
	if r.Credentials["cred-x"] != nil {
		t.Fatal("bound credential not removed")
	}
	if r.Credentials["cred-keep"] == nil {
		t.Fatal("unrelated credential was removed")
	}
}
