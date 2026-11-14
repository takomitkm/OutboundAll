package cli

import (
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
