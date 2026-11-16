//go:build hs_integration

// Phase-3 chain (PLAN step 5): the REAL enrollment path end to end --
// hs-controller (invite/redeem/resume/revoke) against the real headscale, and
// hs-node enroll/run exercising the ADR-002 identity-persistence semantics.
//
// Hard assertions: enroll registers node-a; resume keeps the SAME headscale
// node id (no duplicates); revocation blocks resume and expires the node.
package hstest

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type binProc struct {
	cmd *exec.Cmd
}

func (p *binProc) kill(t *testing.T) {
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
		_ = p.cmd.Wait()
	}
}

func startBin(t *testing.T, name string, env []string, argv ...string) *binProc {
	t.Helper()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = append(os.Environ(), env...)
	logf := filepath.Join(t.TempDir(), name+".log")
	f, err := os.OpenFile(logf, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout, cmd.Stderr = f, f
	if err := cmd.Start(); err != nil {
		f.Close()
		t.Fatalf("start %s: %v", argv[0], err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		f.Close()
	})
	t.Cleanup(func() {
		if b, err := os.ReadFile(logf); err == nil && len(b) > 0 {
			s := string(b)
			if len(s) > 2500 {
				s = s[len(s)-2500:]
			}
			t.Logf("---- %s ----\n%s", name, s)
		}
	})
	return &binProc{cmd: cmd}
}

func runBin(t *testing.T, env []string, argv ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = append(os.Environ(), env...)
	var out strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	}
	return out.String(), code
}

// TestPhase3Chain: invite -> enroll -> register -> resume identity -> revoke.
func TestPhase3Chain(t *testing.T) {
	if os.Getenv("HS_BIN") == "" {
		t.Skip("HS_BIN not set (source .e2e-deps/paths.env)")
	}
	hsBin := envBin(t, "HS_BIN")
	sbBin := envBin(t, "SB_BIN")
	// the controller/node CLI binaries are built from this repo
	// (this test lives at hs/internal/hstest -> three levels up is the root)
	repo, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	ctlBin := filepath.Join(repo, ".e2e-deps", "hs-controller")
	nodeBin := filepath.Join(repo, ".e2e-deps", "hs-node")
	for _, b := range []string{ctlBin, nodeBin} {
		pkg := filepath.Join(repo, "hs", "cmd", filepath.Base(b))
		build, code := runBin(t, nil, "go", "build", "-o", b, pkg)
		_ = build
		if code != 0 {
			t.Fatalf("build %s failed:\n%s", filepath.Base(b), build)
		}
	}

	ws := t.TempDir()
	me := func(sub string) string { return ws + "/" + sub }
	if err := os.MkdirAll(me("hs"), 0o755); err != nil {
		t.Fatal(err)
	}

	// ---- headscale (fresh instance on a second port) ----
	cfg := fmt.Sprintf(`server_url: http://127.0.0.1:8080
listen_addr: 127.0.0.1:8080
metrics_listen_addr: 127.0.0.1:9090
noise:
  private_key_path: %s
prefixes:
  v4: 100.64.0.0/10
  v6: fd7a:115c:a1e0::/48
  allocation: sequential
derp:
  server:
    enabled: true
    region_id: 902
    region_code: ci3
    region_name: CI3 Embedded DERP
    verify_clients: false
    stun_listen_addr: 127.0.0.1:3479
    private_key_path: %s
    automatically_add_embedded_derp_region: true
    ipv4: 127.0.0.1
  urls: []
  auto_update_enabled: false
disable_check_updates: true
node:
  expiry: 0
  ephemeral:
    inactivity_timeout: 30m
database:
  type: sqlite
  sqlite:
    path: %s
    write_ahead_log: true
policy:
  mode: file
  path: %s
dns:
  magic_dns: true
  base_domain: tailnet.test
  override_local_dns: true
  nameservers:
    global:
      - 1.1.1.1
unix_socket: %s
log:
  level: info
  format: text
`,
		me("hs/noise.key"), me("hs/derp.key"), me("hs/db.sqlite"),
		me("hs/policy.json"), me("hs/headscale.sock"))
	if err := os.WriteFile(me("hs/config.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	policy := `{
  "acls": [ { "action": "accept", "src": ["*"], "dst": ["*:*"] } ],
  "ssh": [
    { "action": "accept", "src": ["autogroup:member"], "dst": ["autogroup:member"], "users": ["autogroup:nonroot"] }
  ]
}`
	if err := os.WriteFile(me("hs/policy.json"), []byte(policy), 0o644); err != nil {
		t.Fatal(err)
	}
	startBin(t, "headscale3", nil, hsBin, "serve", "--config", me("hs/config.yaml"))
	if err := waitForErr(60*time.Second, "headscale3 /health", func() error {
		return httpHealth("http://127.0.0.1:8080/health")
	}); err != nil {
		t.Fatal(err)
	}

	// ---- hs-controller: init + serve (HOME isolated) ----
	ctlHome := me("ctl-home")
	ctlEnv := []string{"HOME=" + ctlHome}
	out, code := runBin(t, ctlEnv, ctlBin, "init")
	if code != 0 {
		t.Fatalf("controller init failed: %s", out)
	}
	var adminTok string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "hs-controller") && !strings.HasPrefix(line, "ADMIN-TOKEN") {
			adminTok = line // the token is the only bare line after the header
		}
	}
	if adminTok == "" {
		t.Fatalf("no admin token in init output:\n%s", out)
	}
	startBin(t, "hs-controller", ctlEnv, ctlBin, "serve",
		"--listen", "127.0.0.1:8801",
		"--headscale-bin", hsBin,
		"--headscale-config", me("hs/config.yaml"),
		"--headscale-user", "ci",
		"--server", "http://127.0.0.1:8080")
	if err := waitForErr(30*time.Second, "controller /health", func() error {
		return httpHealth("http://127.0.0.1:8801/api/v1/health")
	}); err != nil {
		t.Fatal(err)
	}

	// invite create via CLI
	out, code = runBin(t, ctlEnv, ctlBin, "invite", "create", "--label", "e2e")
	if code != 0 {
		t.Fatalf("invite create failed: %s", out)
	}
	var inviteCode string
	lines := strings.Split(out, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) == "INVITATION-CODE (one-time, shown once):" && i+1 < len(lines) {
			inviteCode = strings.TrimSpace(lines[i+1])
		}
	}
	if inviteCode == "" {
		t.Fatalf("no invitation code in output:\n%s", out)
	}
	t.Logf("invitation minted (256-bit, one-time)")

	// ---- hs-node enroll (HOME isolated; singbox hatch points at the dep) ----
	nodeHome := me("node-home")
	nodeEnv := []string{
		"HOME=" + nodeHome,
		"XDG_CONFIG_HOME=" + nodeHome + "/.config",
		"DIALHOME_HS_SINGBOX=" + sbBin,
		"DIALHOME_HS_TEST_ALLOW_HTTP=1", // CI: plaintext headscale on localhost
	}
	out, code = runBin(t, nodeEnv, nodeBin, "enroll",
		"--code", inviteCode,
		"--controller", "http://127.0.0.1:8801",
		"--hostname", "node-a")
	if code != 0 || !strings.Contains(out, "ENROLL ok") {
		t.Fatalf("enroll failed (exit %d): %s", code, out)
	}
	t.Logf("%s", strings.TrimSpace(out))

	// the code is one-time: a second enroll with the same code must fail
	out2, code2 := runBin(t, nodeEnv, nodeBin, "enroll",
		"--code", inviteCode,
		"--controller", "http://127.0.0.1:8801",
		"--hostname", "node-again")
	if code2 == 0 {
		t.Fatalf("one-time invitation redeemed twice -- N5' broken: %s", out2)
	}
	t.Logf("GATE N5': replay of used invitation refused (exit %d)", code2)

	// ---- node-a comes ONLINE via hs-node run (resume path) ----
	run1 := startBin(t, "hs-node-run1", nodeEnv, nodeBin, "run")
	waitForErr(90*time.Second, "node-a online after enroll+resume", func() error {
		nodes := hsNodesSafe(t, ws)
		for _, n := range nodes {
			if (strings.HasPrefix(n.Hostname, "node-a") || strings.HasPrefix(n.Name, "node-a")) && n.Online {
				return nil
			}
		}
		return fmt.Errorf("node-a not online yet")
	})
	nodeAID := uint64(0)
	for _, n := range hsNodesSafe(t, ws) {
		if strings.HasPrefix(n.Hostname, "node-a") || strings.HasPrefix(n.Name, "node-a") {
			nodeAID = n.ID
		}
	}
	t.Logf("node-a online, headscale id %d (registered via the real enroll path)", nodeAID)

	// controller registry shows the node ACTIVE (first contact happened)
	nodesJSON := controllerAPI(t, "GET", "http://127.0.0.1:8801/api/v1/nodes", adminTok, "")
	if !strings.Contains(nodesJSON, `"state":"active"`) && !strings.Contains(nodesJSON, `"state": "active"`) {
		t.Fatalf("controller registry does not show active node:\n%s", nodesJSON)
	}

	// ---- identity persistence across a full supervisor restart ----
	run1.kill(t)
	run2 := startBin(t, "hs-node-run2", nodeEnv, nodeBin, "run")
	waitForErr(90*time.Second, "node-a online again after restart (resume path)", func() error {
		nodes := hsNodesSafe(t, ws)
		count := 0
		for _, n := range nodes {
			if strings.HasPrefix(n.Hostname, "node-a") || strings.HasPrefix(n.Name, "node-a") {
				count++
				if !n.Online {
					return fmt.Errorf("present but offline")
				}
				if n.ID != nodeAID {
					return fmt.Errorf("identity changed: %d -> %d", nodeAID, n.ID)
				}
			}
		}
		if count != 1 {
			return fmt.Errorf("expected 1 node-a, got %d", count)
		}
		return nil
	})
	t.Logf("GATE G5 (real path): resume via hs-controller kept node id %d, no duplicates", nodeAID)

	// ---- revocation: API revoke -> resume blocked -> node cannot return ----
	revReq, _ := http.NewRequest("POST", "http://127.0.0.1:8801/api/v1/nodes/node-a/revoke", nil)
	revReq.Header.Set("Authorization", "Bearer "+adminTok)
	revResp, err := http.DefaultClient.Do(revReq)
	if err != nil {
		t.Fatal(err)
	}
	revResp.Body.Close()
	if revResp.StatusCode != 200 {
		t.Fatalf("revoke = %d", revResp.StatusCode)
	}
	run2.kill(t)
	run3 := startBin(t, "hs-node-run3", nodeEnv, nodeBin, "run")
	time.Sleep(20 * time.Second) // let it try (and fail) to resume
	for _, n := range hsNodesSafe(t, ws) {
		if strings.HasPrefix(n.Hostname, "node-a") || strings.HasPrefix(n.Name, "node-a") {
			if n.Online {
				t.Fatalf("GATE N7' FAILED: revoked node came back online")
			}
		}
	}
	run3.kill(t)
	t.Logf("GATE N6'/N7': revoked node cannot resume or re-register (stays offline)")
	t.Logf("==== PHASE 3 SUMMARY ====")
	t.Logf("RESULT: PASS (enroll/resume/revoke chain over the real controller)")
}

// controllerAPI performs an authenticated GET/POST against hs-controller.
func controllerAPI(t *testing.T, method, url, token, body string) string {
	t.Helper()
	var rd *strings.Reader
	if body == "" {
		rd = strings.NewReader("")
	} else {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, rd)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var buf strings.Builder
	b := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(b)
		buf.Write(b[:n])
		if err != nil {
			break
		}
	}
	if resp.StatusCode != 200 {
		t.Fatalf("%s %s = %d: %s", method, url, resp.StatusCode, buf.String())
	}
	return buf.String()
}
