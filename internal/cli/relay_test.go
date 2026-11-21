package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/takomitkm/outboundall/internal/crypto"
	"github.com/takomitkm/outboundall/internal/relay"
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
	line := `restrict,command="/x/bin/OutboundAll relay-helper --port 13437 tunnel",port-forwarding,permitlisten="127.0.0.1:13437",permitlisten="127.0.0.1:13435",permitopen="127.0.0.1:1" ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIHiXedYmFKT4g79Lh4AUkRVOJ1GOiOMugfq01ybRzyaW OutboundAll-node-win2` + "\n# foreign line preserved\n"
	if err := os.WriteFile(ak, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(ak)
	base := filepath.Join(dir, "relay")
	os.MkdirAll(base, 0o700)
	st := relay.NewStore(filepath.Join(base, "registry.json"), filepath.Join(base, "registry.lock"))
	if err := relayMigrateLegacy(st, ak, "/x/bin/OutboundAll", base, filepath.Join(dir, "legacy")); err != nil {
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
	err := relayPurge(st, filepath.Join(t.TempDir(), "authorized_keys"), "live")
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
	ak := filepath.Join(t.TempDir(), "authorized_keys")
	// seed credential AK lines (review finding P2: purge must remove them too)
	// real key bodies are required: Parse validates the embedded ssh-ed25519 blob
	kp1, _ := crypto.Generate("c1")
	kp2, _ := crypto.Generate("c2")
	akBody := "# foreign\n" +
		relay.CredentialLine("/x/bin/OutboundAll", "cred-x", kp1.Public) + "\n" +
		relay.CredentialLine("/x/bin/OutboundAll", "cred-keep", kp2.Public) + "\n"
	if err := os.WriteFile(ak, []byte(akBody), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.WithLock(func(r *relay.Registry) error {
		r.Nodes["gone"] = &relay.NodeRecord{State: "revoked", Ports: relay.Ports{SSH: 13441, App: 13440}}
		r.Credentials["cred-x"] = &relay.Credential{Node: "gone"}
		r.Credentials["cred-keep"] = &relay.Credential{Node: "other"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := relayPurge(st, ak, "gone"); err != nil {
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
	raw, _ := os.ReadFile(ak)
	text := string(raw)
	if strings.Contains(text, "OutboundAll-credential-cred-x") {
		t.Fatal("purged credential AK line still trusts sshd")
	}
	if !strings.Contains(text, "OutboundAll-credential-cred-keep") || !strings.Contains(text, "# foreign") {
		t.Fatal("unrelated AK lines must be preserved")
	}
}

// TestRevokeRetiresUnusedCredentials locks the P1 fix end-to-end at CLI level:
// revoke must delete both the registry credential records and their AK lines.
func TestRevokeRetiresUnusedCredentials(t *testing.T) {
	st := newTestStore(t)
	ak := filepath.Join(t.TempDir(), "authorized_keys")
	kpA, _ := crypto.Generate("a")
	kpB, _ := crypto.Generate("b")
	kpC, _ := crypto.Generate("c")
	kpN, _ := crypto.Generate("n")
	akBody := relay.CredentialLine("/x/bin/OutboundAll", "cred-a", kpA.Public) + "\n" +
		relay.CredentialLine("/x/bin/OutboundAll", "cred-b", kpB.Public) + "\n" +
		relay.CredentialLine("/x/bin/OutboundAll", "cred-c", kpC.Public) + "\n" +
		relay.NodeLine("/x/bin/OutboundAll", relay.Ports{SSH: 13441, App: 13440}, kpN.Public, "victim") + "\n"
	if err := os.WriteFile(ak, []byte(akBody), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.WithLock(func(r *relay.Registry) error {
		r.Nodes["victim"] = &relay.NodeRecord{State: "active", Ports: relay.Ports{SSH: 13441, App: 13440}, CredentialID: "cred-a"}
		r.Nodes["other"] = &relay.NodeRecord{State: "active", Ports: relay.Ports{SSH: 13443, App: 13442}}
		r.Credentials["cred-a"] = &relay.Credential{Node: "victim"}
		r.Credentials["cred-b"] = &relay.Credential{Node: "victim"}
		r.Credentials["cred-c"] = &relay.Credential{Node: "other"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := relayRevoke(st, ak, "victim"); err != nil {
		t.Fatal(err)
	}
	r, _ := st.Load()
	if r.Nodes["victim"].State != "revoked" || r.Nodes["victim"].CredentialID != "" {
		t.Fatal("node not fully revoked")
	}
	if r.Credentials["cred-a"] != nil || r.Credentials["cred-b"] != nil {
		t.Fatal("victim credentials still in registry")
	}
	if r.Credentials["cred-c"] == nil {
		t.Fatal("unrelated node credential was removed")
	}
	raw, _ := os.ReadFile(ak)
	text := string(raw)
	for _, banned := range []string{"OutboundAll-node-victim", "OutboundAll-credential-cred-a", "OutboundAll-credential-cred-b"} {
		if strings.Contains(text, banned) {
			t.Fatalf("revoke left %s in authorized_keys", banned)
		}
	}
	if !strings.Contains(text, "OutboundAll-credential-cred-c") {
		t.Fatal("unrelated credential AK line must survive revoke")
	}
}
