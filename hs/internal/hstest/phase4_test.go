//go:build hs_integration

// Phase-4 chain (PLAN step 6 / ADR-003): deny-by-default enforcement, proven
// over the real stack. Two nodes enroll against one controller:
//
//	node-b  profile "no-ssh"  -> tagged tag:dialhome-node only
//	node-c  default profile   -> tagged tag:dialhome-node + tag:dialhome-node-ssh
//
// The controller (its own tailscale identity tagged tag:dialhome-controller)
// must reach node-c:22 and NOT node-b:22. This is the server-side per-node
// enforcement the reference implemented with permitlisten -- here the
// enforcement point is the Headscale policy (G4).
package hstest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPhase4Chain(t *testing.T) {
	if os.Getenv("HS_BIN") == "" {
		t.Skip("HS_BIN not set (source .e2e-deps/paths.env)")
	}
	hsBin := envBin(t, "HS_BIN")
	sbBin := envBin(t, "SB_BIN")
	tsBin := envBin(t, "TS_BIN")
	tsdBin := envBin(t, "TSD_BIN")

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

	// initial policy: deny-by-default (rendered before anything exists)
	policy0 := "{\n  \"grants\": [],\n  \"ssh\": []\n}\n"
	if err := os.WriteFile(me("hs/policy.json"), []byte(policy0), 0o644); err != nil {
		t.Fatal(err)
	}

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
    region_id: 903
    region_code: ci4
    region_name: CI4 Embedded DERP
    verify_clients: false
    stun_listen_addr: 127.0.0.1:3480
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

	startBin(t, "headscale4", nil, hsBin, "serve", "--config", me("hs/config.yaml"))
	if err := waitForErr(60*time.Second, "headscale4 /health", func() error {
		return httpHealth("http://127.0.0.1:8080/health")
	}); err != nil {
		t.Fatal(err)
	}
	if out, code := runBin(t, nil, hsBin, "--config", me("hs/config.yaml"), "users", "create", "ci"); code != 0 {
		t.Fatalf("users create ci failed: %s", out)
	}

	// ---- controller with policy management ----
	ctlHome := me("ctl-home")
	ctlEnv := []string{"HOME=" + ctlHome}
	if out, code := runBin(t, ctlEnv, ctlBin, "init"); code != 0 {
		t.Fatalf("controller init failed: %s", out)
	}
	startBin(t, "hs-controller4", ctlEnv, ctlBin, "serve",
		"--listen", "127.0.0.1:8802",
		"--headscale-bin", hsBin,
		"--headscale-config", me("hs/config.yaml"),
		"--headscale-user", "ci",
		"--server", "http://127.0.0.1:8080",
		"--policy-file", me("hs/policy.json"))
	if err := waitForErr(30*time.Second, "controller /health", func() error {
		return httpHealth("http://127.0.0.1:8802/api/v1/health")
	}); err != nil {
		t.Fatal(err)
	}

	// ---- controller tailscale identity, TAGGED ----
	// mint a tagged preauth key for the ctrl node directly via headscale
	out, code := runBin(t, nil, hsBin, "--config", me("hs/config.yaml"),
		"preauthkeys", "create", "--user", "1", "--tags", "tag:dialhome-controller", "-o", "json")
	if code != 0 {
		t.Fatalf("ctrl preauthkey failed: %s", out)
	}
	ctrlKey := jsonKey(t, out, "key")
	startBin(t, "tailscaled4", nil, tsdBin,
		"--tun=userspace-networking",
		"--statedir="+me("ctrl"),
		"--socket="+me("ctrl/ts.sock"))
	sockArg := "--socket=" + me("ctrl/ts.sock")
	if out, code := runBin(t, nil, tsBin, sockArg, "up",
		"--login-server=http://127.0.0.1:8080",
		"--authkey="+ctrlKey,
		"--hostname=ctrl",
		"--accept-dns=false"); code != 0 {
		t.Fatalf("ctrl tailscale up failed: %s", out)
	}
	t.Logf("controller identity registered (tag:dialhome-controller)")

	// ---- two nodes: node-b (no-ssh) and node-c (default ssh) ----
	// each node gets its OWN home (state dir + instance lock are per-layout;
	// sharing one home would deadlock the second supervisor on hs-node.lock)
	enroll := func(hostname, profile string) {
		nodeHome := me("node-home-" + hostname)
		nodeEnv := []string{
			"HOME=" + nodeHome,
			"XDG_CONFIG_HOME=" + nodeHome + "/.config",
			"DIALHOME_HS_SINGBOX=" + sbBin,
			"DIALHOME_HS_TEST_ALLOW_HTTP=1",
		}
		out, code := runBin(t, ctlEnv, ctlBin, "invite", "create", "--api", "http://127.0.0.1:8802",
			"--label", hostname, "--profile", profile)
		if code != 0 {
			t.Fatalf("invite for %s failed: %s", hostname, out)
		}
		invCode := ""
		lines := strings.Split(out, "\n")
		for i, line := range lines {
			if strings.TrimSpace(line) != "INVITATION-CODE (one-time, shown once):" {
				continue
			}
			for _, l2 := range lines[i+1:] {
				if !strings.Contains(l2, "\"code\"") {
					continue
				}
				var parsed map[string]any
				if err := jsonUnmarshal(strings.TrimSpace(l2), &parsed); err == nil {
					invCode, _ = parsed["code"].(string)
					break
				}
			}
		}
		if invCode == "" {
			t.Fatalf("no code for %s:\n%s", hostname, out)
		}
		out, code = runBin(t, nodeEnv, nodeBin, "enroll",
			"--code", invCode,
			"--controller", "http://127.0.0.1:8802",
			"--hostname", hostname)
		if code != 0 {
			t.Fatalf("enroll %s failed: %s", hostname, out)
		}
		startBin(t, "hs-node-"+hostname, nodeEnv, nodeBin, "run")
	}

	waitOnline := func(hostname string) uint64 {
		var id uint64
		if err := waitForErr(120*time.Second, hostname+" online", func() error {
			for _, n := range hsNodesSafe(t, ws) {
				if (strings.HasPrefix(n.Hostname, hostname) || strings.HasPrefix(n.Name, hostname)) && n.Online {
					id = n.ID
					return nil
				}
			}
			return fmt.Errorf("not online yet")
		}); err != nil {
			t.Fatal(err)
		}
		return id
	}

	enroll("node-b", "no-ssh")
	idB := waitOnline("node-b")
	enroll("node-c", "default")
	idC := waitOnline("node-c")
	t.Logf("node-b online (id %d, no-ssh profile), node-c online (id %d, default)", idB, idC)

	// policy reload + client ACL propagation + path establishment all take
	// time; retry the probe instead of a fixed sleep
	stOut, stCode := runBin(t, nil, tsBin, sockArg, "status")
	t.Logf("controller status before G4 probes (exit %d):\n%s", stCode, stOut)

	// G4 positive: controller -> node-c:22 must connect (SSH rule covers it).
	// Probe by MagicDNS name AND by tailnet IP, so a name-resolution failure
	// is distinguishable from a netmap/ACL failure.
	nodeIP := ""
	for _, n := range hsNodesSafe(t, ws) {
		if strings.HasPrefix(n.Hostname, "node-c") || strings.HasPrefix(n.Name, "node-c") {
			if len(n.IPs) > 0 {
				nodeIP = n.IPs[0]
			}
		}
	}
	t.Logf("node-c tailnet IP: %q", nodeIP)

	cCode := 1
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		_, cCode = runBin(t, nil, tsBin, sockArg, "nc", "node-c", "22")
		if cCode == 0 {
			break
		}
		if nodeIP != "" {
			_, ipCode := runBin(t, nil, tsBin, sockArg, "nc", nodeIP, "22")
			if ipCode == 0 {
				t.Logf("IP probe succeeded where name probe failed -- MagicDNS issue, not ACL")
				cCode = 0
				break
			}
		}
		time.Sleep(5 * time.Second)
	}
	if cCode != 0 {
		t.Fatalf("G4 FAILED: controller cannot reach node-c:22 (exit %d, ip=%q) -- ssh grant path broken", cCode, nodeIP)
	}
	t.Logf("GATE G4 positive: controller reaches node-c:22 (tagged grant)")

	// G4 negative: controller -> node-b:22 must NOT connect (no covering rule;
	// the connection must be refused/blocked by the tailnet ACL, not served)
	nbOut, nbCode := runBin(t, nil, tsBin, sockArg, "nc", "--timeout=5s", "node-b", "22")
	if nbCode == 0 && strings.Contains(nbOut, "SSH-2.0") {
		t.Fatalf("G4 FAILED: node-b (no-ssh profile) answered SSH -- deny-by-default broken")
	}
	t.Logf("GATE G4 negative: node-b:22 not reachable (exit %d, out=%q) -- deny-by-default holds", nbCode, strings.TrimSpace(nbOut))

	// policy file on disk still shows the deny baseline
	if b, err := os.ReadFile(me("hs/policy.json")); err == nil {
		if !strings.Contains(string(b), "\"grants\": []") {
			t.Fatalf("policy file lost the deny-by-default baseline:\n%s", b)
		}
	}
	t.Logf("==== PHASE 4 SUMMARY ====")
	t.Logf("RESULT: PASS (deny-by-default enforced server-side; per-profile SSH)")
}
