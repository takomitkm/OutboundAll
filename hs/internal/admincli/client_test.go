package admincli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/takomitkm/outboundall/hs/internal/controller"
)

// newFixture stands up a real controller server (reuse the phase-3 fixture
// shape) and returns a client pointed at it plus the admin token.
func newFixture(t *testing.T) (*Client, string, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := controller.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	adminTok, err := controller.RandomToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetAdminToken(controller.AdminTokenSHA(adminTok)); err != nil {
		t.Fatal(err)
	}
	srv := controller.NewServer(store, controller.Options{
		AdminTokenSHA: controller.AdminTokenSHA(adminTok),
		Server:        "http://127.0.0.1:8080",
		Headscale:     &controller.FakeHeadscale{},
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return New(ts.URL, adminTok), adminTok, ts.URL
}

// N-cli-1: node/invitation listing round-trips through the real server.
func TestListNodesRoundTrip(t *testing.T) {
	c, _, _ := newFixture(t)
	code, _, err := c.CreateInvite("lab", "default", 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.RedeemProbe(code, "win-a"); err != nil {
		t.Fatal(err)
	}
	nodes, invs, err := c.ListNodes()
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].ID != "win-a" || nodes[0].State != "pending" {
		t.Fatalf("nodes = %+v", nodes)
	}
	if len(invs) != 1 || invs[0].State != "redeemed" {
		t.Fatalf("invitations = %+v", invs)
	}
}

// RedeemProbe exists so tests can drive the one-shot redeem through the
// client; production enroll uses hs-node (which owns its own flow).
func (c *Client) RedeemProbe(code, hostname string) (secret string, err error) {
	st, b, err := c.do(http.MethodPost, "/api/v1/enroll/redeem", map[string]string{"code": code, "hostname": hostname})
	if err != nil {
		return "", err
	}
	if st != 201 {
		return "", ErrUnauthorized
	}
	var out struct {
		NodeSecret string `json:"node_secret"`
	}
	_ = json.Unmarshal(b, &out)
	return out.NodeSecret, nil
}

// N-cli-2: 401 maps to ErrUnauthorized with the actionable hint in the CLI.
func TestUnauthorizedMapped(t *testing.T) {
	c, _, _ := newFixture(t)
	c.Token = "wrong-token"
	_, _, err := c.ListNodes()
	if err == nil || !strings.Contains(err.Error(), "token") {
		t.Fatalf("err = %v, want token-mapped error", err)
	}
}

// N-cli-3: token resolution order -- flag beats env beats file.
func TestTokenFromSources(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("OUTBOUNDALL_HS_ADMIN_TOKEN", "from-env")
	if got := TokenFromSources(""); got != "from-env" {
		t.Fatalf("env not used: %q", got)
	}
	if got := TokenFromSources("from-flag"); got != "from-flag" {
		t.Fatalf("flag must win: %q", got)
	}
	t.Setenv("OUTBOUNDALL_HS_ADMIN_TOKEN", "")
	// the client looks for $HOME/.OutboundAll-hs/admin-token
	os.MkdirAll(filepath.Join(dir, ".OutboundAll-hs"), 0o700)
	os.WriteFile(filepath.Join(dir, ".OutboundAll-hs", "admin-token"), []byte("from-file\n"), 0o600)
	// point HOME at dir (linux) and USERPROFILE too (windows variant)
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	if got := TokenFromSources(""); got != "from-file" {
		t.Fatalf("file not used: %q", got)
	}
}

// N-cli-4: command-line builders are byte-exact (the transport is the
// official tailscale client; a drifted line silently changes the security
// surface).
func TestSSHArgs(t *testing.T) {
	got := SSHArgs("/tmp/ts.sock", "ops", "win-a", true, nil)
	want := []string{"tailscale", "--socket=/tmp/ts.sock", "ssh", "ops@win-a"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("SSHArgs = %v, want %v", got, want)
	}
	got = SSHArgs("", "", "win-a", true, []string{"echo", "hi"})
	want = []string{"tailscale", "ssh", "win-a", "echo", "hi"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("SSHArgs = %v, want %v", got, want)
	}
}

func TestSFTPProxy(t *testing.T) {
	argv, proxy := SFTPProxy("/tmp/ts.sock", "ops", "win-a")
	if proxy != "tailscale --socket=/tmp/ts.sock nc win-a 22" {
		t.Fatalf("proxy = %q", proxy)
	}
	if len(argv) != 4 || argv[0] != "sftp" || argv[1] != "-o" || argv[2] != "ProxyCommand="+proxy || argv[3] != "ops@win-a" {
		t.Fatalf("argv = %#v", argv)
	}
}

// N-cli-5: doctor output shape is pinned (machine-greppable state lines).
func TestDoctorLines(t *testing.T) {
	lines := DoctorLines("http://x:8800", `{"status":"ok"}`, "", `{"BackendState": "Running"}`)
	joined := strings.Join(lines, "\n")
	for _, want := range []string{
		"controller: http://x:8800",
		"tailscale state: Running",
		"tailscale socket: default",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("doctor missing %q:\n%s", want, joined)
		}
	}
	lines = DoctorLines("http://x", "", "", "{}")
	if !strings.Contains(strings.Join(lines, "\n"), "NOT Running") {
		t.Fatal("non-running tailscale must be flagged, not silently passed")
	}
}

// N-cli-6: table renderers keep columns aligned (grep-ability contract).
func TestFormatTables(t *testing.T) {
	out := FormatNodes([]*Node{{ID: "win-a", State: "active", Profile: "default"}})
	if !strings.Contains(out, "win-a") || !strings.Contains(out, "active") {
		t.Fatalf("node table missing fields:\n%s", out)
	}
	out = FormatInvitations([]*Invitation{{ID: "inv-x", State: "created", Label: "lab", ExpiresAt: "2026-01-01T00:00:00Z"}})
	if !strings.Contains(out, "inv-x") || !strings.Contains(out, "created") {
		t.Fatalf("invitation table missing fields:\n%s", out)
	}
}
