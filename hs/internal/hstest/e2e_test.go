//go:build hs_integration

// Phase-1b gate chain (PLAN.md): boots the real stack on the CI runner and
// walks the decision gates. Hard gates (test fails): G2, G5, G4p.
// G1/G3 are recorded observations adjudicated in ADR-005 / ADR-003.
//
// Run via:  go test ./hs/internal/hstest -tags hs_integration -count=1
// Requires: HS_BIN, SB_BIN, TS_BIN, TSD_BIN (hs/scripts/e2e-deps.sh).
package hstest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestPhase1bChain(t *testing.T) {
	if os.Getenv("HS_BIN") == "" {
		t.Skip("HS_BIN not set (source .e2e-deps/paths.env)")
	}
	hsBin := envBin(t, "HS_BIN")
	sbBin := envBin(t, "SB_BIN")
	tsBin := envBin(t, "TS_BIN")
	tsdBin := envBin(t, "TSD_BIN")

	ws := t.TempDir()
	me := func(sub string) string { return ws + "/" + sub }
	if err := os.MkdirAll(me("hs"), 0o755); err != nil {
		t.Fatal(err)
	}

	username := os.Getenv("USER")
	if username == "" {
		username = os.Getenv("USERNAME")
	}
	if username == "" {
		username = "runner"
	}
	t.Logf("login user for ssh sessions: %s", username)

	// -- policy: phase-1b uses an explicit allow-all ACL + one SSH rule.
	// (Headscale default with NO policy is allow-all; the explicit file
	// already exercises the policy path. Deny-by-default lands in Phase 4.)
	policy := `{
  // huJSON: trailing comments allowed
  "acls": [
    { "action": "accept", "src": ["*"], "dst": ["*:*"] }
  ],
  "ssh": [
    // policy v2: SSH rules accept neither "*" in src (alias not supported)
    // nor "*" in dst ("wildcard is not supported as SSH destination") --
    // autogroup:member covers user-owned devices on both sides.
    { "action": "accept", "src": ["autogroup:member"], "dst": ["autogroup:member"], "users": ["autogroup:nonroot"] }
  ]
}`
	if err := os.WriteFile(me("hs/policy.json"), []byte(policy), 0o644); err != nil {
		t.Fatal(err)
	}

	// -- headscale config (schema from config-example.yaml @ v0.29.7)
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
    region_id: 901
    region_code: ci
    region_name: CI Embedded DERP
    verify_clients: false
    stun_listen_addr: 127.0.0.1:3478
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
      - 8.8.8.8
unix_socket: %s
log:
  level: info
  format: text
`,
		me("hs/noise_private.key"), me("hs/derp_server_private.key"),
		me("hs/db.sqlite"), me("hs/policy.json"), me("hs/headscale.sock"))
	if err := os.WriteFile(me("hs/config.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	// -- start headscale
	hs := startProc(t, "headscale.log", []string{hsBin, "serve", "--config", me("hs/config.yaml")})
	if err := waitForErr(60*time.Second, "headscale /health", func() error {
		return httpHealth("http://127.0.0.1:8080/health")
	}); err != nil {
		dumpLog(t, hs, "headscale did not become healthy")
		t.Fatal(err)
	}
	t.Logf("headscale up (pid %d)", hs.cmd.Process.Pid)

	// -- user + preauth keys
	hsCLI(t, ws, "users", "create", "ci")
	usersOut := hsCLI(t, ws, "users", "list", "-o", "json")
	var users []hsUser
	if i := strings.Index(usersOut, "["); i >= 0 {
		if err := jsonUnmarshal(usersOut[i:], &users); err != nil {
			t.Fatalf("parse users list: %v\nraw: %s", err, usersOut)
		}
	}
	if len(users) == 0 {
		t.Fatalf("no users after create: %s", usersOut)
	}
	userID := users[0].ID

	keyOut := hsCLI(t, ws, "preauthkeys", "create", "--user", fmt.Sprint(userID), "--expiration", "1h", "-o", "json")
	nodeKey := jsonKey(t, keyOut, "key")
	keyOut2 := hsCLI(t, ws, "preauthkeys", "create", "--user", fmt.Sprint(userID), "--expiration", "1h", "-o", "json")
	ctrlKey := jsonKey(t, keyOut2, "key")
	t.Logf("preauth keys created (single-use)")

	// -- sing-box node-a (userspace endpoint + ssh server)
	writeNodeCfg := func(authKey string) string {
		var sbCfg string
		if authKey == "" {
			sbCfg = fmt.Sprintf(`{
  "log": { "level": "info" },
  "endpoints": [{
    "type": "tailscale",
    "tag": "ts",
    "control_url": "http://127.0.0.1:8080",
    "hostname": "node-a",
    "state_directory": %q,
    "system_interface": false,
    "ssh_server": { "enabled": true }
  }]
}`, me("node-a/state"))
		} else {
			sbCfg = fmt.Sprintf(`{
  "log": { "level": "info" },
  "endpoints": [{
    "type": "tailscale",
    "tag": "ts",
    "control_url": "http://127.0.0.1:8080",
    "hostname": "node-a",
    "auth_key": %q,
    "state_directory": %q,
    "system_interface": false,
    "ssh_server": { "enabled": true }
  }]
}`, authKey, me("node-a/state"))
		}
		path := me("node-a/config.json")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(sbCfg), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}

	cfgPath := writeNodeCfg(nodeKey)
	sbA := startProc(t, "singbox-a.log", []string{sbBin, "run", "-c", cfgPath})
	if err := waitForErr(120*time.Second, "node-a registration in headscale", func() error {
		nodes := hsNodesSafe(t, ws)
		for _, n := range nodes {
			if strings.HasPrefix(n.Hostname, "node-a") || strings.HasPrefix(n.Name, "node-a") {
				return nil
			}
		}
		return fmt.Errorf("not registered yet (%d nodes)", len(nodes))
	}); err != nil {
		dumpLog(t, hs, "headscale during registration wait")
		dumpLog(t, sbA, "sing-box during registration wait")
		if raw, code, _ := runCmd(t, 30*time.Second, []string{hsBin, "--config", me("hs/config.yaml"), "nodes", "list", "-o", "json"}); code == 0 {
			t.Logf("raw nodes list at timeout:
%s", raw)
		}
		t.Fatal(err)
	}
	t.Logf("GATE G2: PASS -- sing-box endpoint registered over http:// control_url")

	nodes := hsNodesSafe(t, ws)
	nodeAID := uint64(0)
	for _, n := range nodes {
		if strings.HasPrefix(n.Hostname, "node-a") || strings.HasPrefix(n.Name, "node-a") {
			nodeAID = n.ID
		}
	}
	nodeACount := len(nodes)

	// -- G5: restart node-a WITHOUT auth_key; identity must persist, no dup
	sbA.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case err := <-sbA.done:
		t.Logf("node-a exited on SIGTERM: %v", err)
	case <-time.After(15 * time.Second):
		dumpLog(t, sbA, "did not exit on SIGTERM")
		t.Fatal("node-a did not exit on SIGTERM within 15s")
	}
	cfgPath = writeNodeCfg("")
	sbA2 := startProc(t, "singbox-a2.log", []string{sbBin, "run", "-c", cfgPath})
	if err := waitForErr(120*time.Second, "node-a re-registration (identity persist)", func() error {
		nodes := hsNodesSafe(t, ws)
		count := 0
		for _, n := range nodes {
			if strings.HasPrefix(n.Hostname, "node-a") || strings.HasPrefix(n.Name, "node-a") {
				count++
				if n.ID != nodeAID {
					return fmt.Errorf("node-a came back with a NEW id (%d -> %d)", nodeAID, n.ID)
				}
			}
		}
		if count != 1 {
			return fmt.Errorf("expected exactly 1 node-a, got %d (duplicate identity)", count)
		}
		return nil
	}); err != nil {
		dumpLog(t, sbA2, "sing-box during re-registration wait")
		t.Fatal(err)
	}
	if n := len(hsNodesSafe(t, ws)); n != nodeACount {
		t.Fatalf("G5 FAILED: node count changed after restart: %d -> %d", nodeACount, n)
	}
	t.Logf("GATE G5: PASS -- identity persisted across restart (node id %d, no duplicates)", nodeAID)

	// -- controller: official tailscaled (userspace networking, no root)
	startProc(t, "tailscaled.log", []string{tsdBin,
		"--tun=userspace-networking",
		"--statedir=" + me("ctrl"),
		"--socket=" + me("ctrl/ts.sock"),
	})
	if err := waitForErr(30*time.Second, "tailscaled socket", func() error {
		_, err := os.Stat(me("ctrl/ts.sock"))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	sockArg := "--socket=" + me("ctrl/ts.sock")
	out, code, _ := runCmd(t, 90*time.Second, []string{tsBin, sockArg, "up",
		"--login-server=http://127.0.0.1:8080",
		"--authkey=" + ctrlKey,
		"--hostname=ctrl",
		"--accept-dns=false",
	})
	if code != 0 {
		t.Fatalf("tailscale up failed (exit %d): %s", code, out)
	}
	if err := waitForErr(60*time.Second, "ctrl node visible in headscale", func() error {
		for _, n := range hsNodesSafe(t, ws) {
			if strings.HasPrefix(n.Hostname, "ctrl") || strings.HasPrefix(n.Name, "ctrl") {
				return nil
			}
		}
		return fmt.Errorf("ctrl not registered")
	}); err != nil {
		t.Fatal(err)
	}
	t.Logf("controller (official tailscale client, pinned 1.104.7) registered")

	// -- G4p: tailscale ssh controller -> node-a (policy-governed)
	sshOut, sshCode, sshErr := runCmd(t, 90*time.Second, []string{tsBin, sockArg,
		"ssh", username + "@node-a", "echo hs-e2e-ssh-ok && whoami"})
	if sshCode != 0 || !strings.Contains(sshOut, "hs-e2e-ssh-ok") {
		dumpLog(t, sbA2, "sing-box during ssh attempt")
		t.Fatalf("G4p FAILED: tailscale ssh exit=%d err=%v out=%q", sshCode, sshErr, sshOut)
	}
	t.Logf("GATE G4p: PASS -- controller ssh into node-a worked; output=%q", strings.TrimSpace(sshOut))

	// -- G1 observation: does inbound tailnet traffic reach the endpoint?
	banner, bCode, _ := runCmd(t, 30*time.Second, []string{tsBin, sockArg, "nc", "node-a", "22"})
	if bCode == 0 && strings.Contains(banner, "SSH-2.0") {
		t.Logf("GATE G1 OBSERVATION: tailnet:22 answers %q (ssh_server reachable)", strings.TrimSpace(firstLine(banner)))
	} else {
		t.Logf("GATE G1 OBSERVATION: tailnet:22 probe exit=%d out=%q", bCode, banner)
	}
	probe, pCode, _ := runCmd(t, 30*time.Second, []string{tsBin, sockArg, "nc", "node-a", "8443"})
	if pCode != 0 {
		t.Logf("GATE G1 OBSERVATION: tailnet:8443 exit=%d out=%q (fast refusal => inbound reaches endpoint; timeout => black hole)", pCode, probe)
	} else {
		t.Logf("GATE G1 OBSERVATION: tailnet:8443 unexpectedly connected: %q", probe)
	}

	// -- G3 first-pass observation: does node revocation kill an established
	//    SSH session? Non-fatal; only records what actually happens.
	sshProc := exec.Command(tsBin, sockArg, "ssh", username+"@node-a", "sleep 20 && echo session-survived")
	var sessOut strings.Builder
	sshProc.Stdout = &sessOut
	sshProc.Stderr = &sessOut
	if err := sshProc.Start(); err == nil {
		time.Sleep(8 * time.Second) // let the session establish
		expOut, expCode, _ := runCmd(t, 30*time.Second, []string{hsBin, "--config", me("hs/config.yaml"), "nodes", "expire", fmt.Sprint(nodeAID)})
		t.Logf("GATE G3: node expired (exit %d): %s", expCode, strings.TrimSpace(expOut))
		done := make(chan error, 1)
		go func() { done <- sshProc.Wait() }()
		select {
		case err := <-done:
			t.Logf("GATE G3 OBSERVATION: ssh session ended %v after expire; output=%q", err, sessOut.String())
		case <-time.After(45 * time.Second):
			_ = sshProc.Process.Kill()
			<-done
			t.Logf("GATE G3 OBSERVATION: ssh session was STILL RUNNING 45s after node expire; output=%q", sessOut.String())
		}
	} else {
		t.Logf("GATE G3: could not start observation session: %v", err)
	}

	// -- summary
	t.Logf("==== PHASE 1B SUMMARY ====")
	t.Logf("GATE G2: PASS (http control_url)")
	t.Logf("GATE G5: PASS (identity persistence)")
	t.Logf("GATE G4p: PASS (policy-governed ssh)")
	t.Logf("GATE G1: OBSERVATION (see lines above)")
	t.Logf("GATE G3: OBSERVATION (see lines above)")
	t.Logf("RESULT: PASS (hard gates)")
}
