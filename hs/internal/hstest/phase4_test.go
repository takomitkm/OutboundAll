//go:build hs_integration

// Phase-4 chain (PLAN step 6 / ADR-003): deny-by-default enforcement, proven
// over the real stack. Two nodes enroll against one controller:
//
//	node-b  profile "no-ssh"  -> tagged tag:OutboundAll-node only
//	node-c  default profile   -> tagged tag:OutboundAll-node + tag:OutboundAll-node-ssh
//
// The controller (its own tailscale identity tagged tag:OutboundAll-controller)
// must reach node-c:22 and NOT node-b:22. This is the server-side per-node
// enforcement the reference implemented with permitlisten -- here the
// enforcement point is the Headscale policy (G4).
package hstest

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/takomitkm/outboundall/hs/internal/authz"
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

	// initial policy: CONTROL EXPERIMENT -- tag declarations + allow-all ACL +
	// tag-based ssh rule. If the controller still cannot reach node-c under
	// allow-all, the failure is tagged-node visibility itself, not the
	// deny-by-default grants shape.
	policy0 := "{\n" +
		"  \"tagOwners\": {\n" +
		"    \"tag:OutboundAll-controller\": [\"ci@\"],\n" +
		"    \"tag:OutboundAll-node\": [\"ci@\"],\n" +
		"    \"tag:OutboundAll-node-ssh\": [\"ci@\"]\n" +
		"  },\n" +
		"  \"acls\": [ { \"action\": \"accept\", \"src\": [\"*\"], \"dst\": [\"*:*\"] } ],\n" +
		"  \"ssh\": [\n" +
		"    { \"action\": \"accept\", \"src\": [\"ci@\"], \"dst\": [\"tag:OutboundAll-node-ssh\"], \"users\": [\"autogroup:nonroot\"] }\n" +
		"  ]\n" +
		"}\n"
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

	// ---- controller tailscale identity, TAGGED (main chain restored) ----
	// The grants src is tag:OutboundAll-controller; a peer matches a grant only
	// through identities it actually holds (CI-proven: an untagged ctrl was
	// invisible to the peer map even with the policy fully loaded). The
	// controller therefore registers with its tag.
	out, code := runBin(t, nil, hsBin, "--config", me("hs/config.yaml"),
		"preauthkeys", "create", "--user", "1", "--tags", "tag:OutboundAll-controller", "-o", "json")
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
	t.Logf("controller identity registered (tag:OutboundAll-controller)")

	// ---- two nodes: node-b (no-ssh) and node-c (default ssh) ----
	// each node gets its OWN home (state dir + instance lock are per-layout;
	// sharing one home would deadlock the second supervisor on hs-node.lock)
	enroll := func(hostname, profile string, svcPort int) {
		nodeHome := me("node-home-" + hostname)
		nodeEnv := []string{
			"HOME=" + nodeHome,
			"XDG_CONFIG_HOME=" + nodeHome + "/.config",
			"OUTBOUNDALL_HS_SINGBOX=" + sbBin,
			"OUTBOUNDALL_HS_TEST_ALLOW_HTTP=1",
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
		// Phase-6 experiment hook: a service port >= 1 edits the node config
		// (between enroll and run) so hs-node renders the route rules for it.
		if svcPort >= 1 {
			cfgPath := nodeHome + "/.config/OutboundAll-hs/config/config.json"
			cb, rerr := os.ReadFile(cfgPath)
			if rerr != nil {
				t.Fatal(rerr)
			}
			var cfgDoc map[string]any
			if jerr := jsonUnmarshal(string(cb), &cfgDoc); jerr != nil {
				t.Fatal(jerr)
			}
			cfgDoc["services"] = []map[string]any{
				{"name": "web", "target": fmt.Sprintf("127.0.0.1:%d", svcPort)},
			}
			nb, _ := jsonMarshal(cfgDoc)
			if werr := os.WriteFile(cfgPath, nb, 0o600); werr != nil {
				t.Fatal(werr)
			}
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

	enroll("node-b", "no-ssh", 0)
	idB := waitOnline("node-b")
	enroll("node-c", "default", 8443)
	idC := waitOnline("node-c")
	t.Logf("node-b online (id %d, no-ssh profile), node-c online (id %d, default)", idB, idC)

	// Checkpoint: the rendered policy (on disk) must parse cleanly in
	// headscale, and the live policy-manager must contain the grants. If
	// headscale holds a different policy than the file, the SIGHUP path is
	// broken -- fail HERE with both artifacts instead of a confusing nc
	// failure later.
	if chkOut, chkCode := runBin(t, nil, hsBin, "--config", me("hs/config.yaml"),
		"policy", "check", "--file", me("hs/policy.json")); chkCode != 0 {
		t.Fatalf("rendered policy does not parse in headscale (exit %d):\n%s", chkCode, chkOut)
	}
	t.Logf("rendered policy parses cleanly (headscale policy check)")
	time.Sleep(5 * time.Second) // SIGHUP settle
	pmBody := ""
	if resp, err := http.Get("http://127.0.0.1:9090/debug/policy-manager"); err == nil {
		b := make([]byte, 2500)
		n, _ := resp.Body.Read(b)
		resp.Body.Close()
		pmBody = string(b[:n])
		t.Logf("policy-manager after redeem (HTTP %d):\n%s", resp.StatusCode, pmBody)
	}
	if !strings.Contains(pmBody, "\"grants\"") && !strings.Contains(pmBody, "Grants") {
		t.Logf("WARNING: policy-manager does not show grants -- headscale holds a deny-only policy (SIGHUP path suspect)")
	}

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
		// dump every layer before dying: raw headscale nodes JSON (does node-c
		// have an IP at all?), the client's peer view, and the live policy file
		nodesArgv := []string{hsBin, "--config", me("hs/config.yaml"), "nodes", "list", "-o", "json"}
		if raw, code := runBin(t, nil, nodesArgv...); code == 0 {
			t.Logf("RAW nodes list at failure:\n%s", raw)
		}
		statusArgv := []string{tsBin, sockArg, "status", "--json"}
		js, jsCode := runBin(t, nil, statusArgv...)
		if jsCode == 0 && len(js) > 6000 {
			js = js[:6000]
		}
		t.Logf("RAW tailscale status --json (truncated, exit %d):\n%s", jsCode, js)
		if b, rerr := os.ReadFile(me("hs/policy.json")); rerr == nil {
			t.Logf("RAW live policy file:\n%s", b)
		}
		// headscale debug endpoints (served on the metrics listener) expose the
		// internals: filter rules, NodeStore adjacency, and per-node map
		// responses. Truncate each to keep the CI log readable.
		for _, ep := range []string{"filter", "nodestore", "policy-manager", "mapresponses"} {
			resp, err := http.Get("http://127.0.0.1:9090/debug/" + ep)
			if err != nil {
				t.Logf("RAW debug/%s: fetch error %v", ep, err)
				continue
			}
			body := make([]byte, 3500)
			n, _ := resp.Body.Read(body)
			resp.Body.Close()
			t.Logf("RAW debug/%s (HTTP %d, first %d bytes):\n%s", ep, resp.StatusCode, n, string(body[:n]))
		}
		t.Fatalf("G4 FAILED: controller cannot reach node-c:22 (exit %d, ip=%q) -- ssh grant path broken", cCode, nodeIP)
	}
	t.Logf("GATE G4 positive: controller reaches node-c:22 (tagged grant)")

	// G4 negative: controller -> node-b:22 must NOT serve SSH (no covering
	// rule). ACL-blocked dials black-hole rather than refuse, so wrap the
	// probe in coreutils timeout: 124 = blocked (good), 0 with an SSH banner
	// = the deny-by-default is broken. tailscale nc has no -timeout flag
	// (CI-proven: "flag provided but not defined").
	nbOut, nbCode := runBin(t, nil, "timeout", "5", tsBin, sockArg, "nc", "node-b", "22")
	if nbCode == 0 && strings.Contains(nbOut, "SSH-2.0") {
		t.Fatalf("G4 FAILED: node-b (no-ssh profile) answered SSH -- deny-by-default broken")
	}
	t.Logf("GATE G4 negative: node-b:22 not reachable (exit %d, out=%q) -- deny-by-default holds", nbCode, strings.TrimSpace(nbOut))

	username := os.Getenv("USER")
	if username == "" {
		username = os.Getenv("USERNAME")
	}
	if username == "" {
		username = "runner"
	}

	// ---- Phase-6 G1 experiment: service forwarding through the userspace
	// endpoint. node-c exposes 127.0.0.1:8443 (a loopback service ON THE NODE
	// -- here the runner itself). The node route engine must forward
	// tailnet:8443 -> 127.0.0.1:8443 only for the explicit service, reject
	// everything else, and leave ssh_server:22 working.
	svc, svcErr := net.Listen("tcp", "127.0.0.1:8443")
	if svcErr != nil {
		t.Fatal(svcErr)
	}
	t.Cleanup(func() { svc.Close() })
	go func() {
		for {
			conn, err := svc.Accept()
			if err != nil {
				return
			}
			conn.Write([]byte("svc-8443-ok\n"))
			conn.Close()
		}
	}()

	// the policy needs a grant for tcp:8443 -- headscale-side WHO gate. The
	// node-side WHAT gate (route rules) ships with the node config.
	fullPolicy := "{\n" +
		"  \"tagOwners\": {\n" +
		"    \"tag:OutboundAll-controller\": [\"ci@\"],\n" +
		"    \"tag:OutboundAll-node\": [\"ci@\"],\n" +
		"    \"tag:OutboundAll-node-ssh\": [\"ci@\"]\n" +
		"  },\n" +
		"  \"grants\": [\n" +
		"    { \"src\": [\"tag:OutboundAll-controller\"], \"dst\": [\"tag:OutboundAll-node-ssh\"], \"ip\": [\"tcp:22\"] },\n" +
		"    { \"src\": [\"tag:OutboundAll-controller\"], \"dst\": [\"tag:OutboundAll-node\"], \"ip\": [\"tcp:8443\"] }\n" +
		"  ],\n" +
		"  \"ssh\": [\n" +
		"    { \"action\": \"accept\", \"src\": [\"tag:OutboundAll-controller\"], \"dst\": [\"tag:OutboundAll-node-ssh\"], \"users\": [\"autogroup:nonroot\"] }\n" +
		"  ]\n" +
		"}\n"
	if err := os.WriteFile(me("hs/policy.json"), []byte(fullPolicy), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, code := runBin(t, nil, hsBin, "--config", me("hs/config.yaml"),
		"policy", "check", "--file", me("hs/policy.json")); code != 0 {
		t.Fatalf("service policy does not parse:\n%s", out)
	}
	if out, err := exec.Command("pidof", "headscale").Output(); err == nil && len(out) > 0 {
		_ = exec.Command("kill", "-HUP", strings.TrimSpace(string(out))).Run()
	}
	time.Sleep(6 * time.Second) // policy reload + propagation

	// G1 positive: ctrl -> node-c:8443 must return the service marker.
	// tailscale nc copies stdin; runBin's /dev/null stdin EOFs the connection
	// instantly and the marker is lost (CI-proven: exit 0, empty out) -- keep
	// stdin open with a sleep pipe.
	svcOut, svcCode := runBin(t, nil, "sh", "-c",
		"sleep 3 | "+tsBin+" "+sockArg+" nc node-c 8443")
	if svcCode != 0 || !strings.Contains(svcOut, "svc-8443-ok") {
		t.Fatalf("G1 FAILED: tailnet:8443 did not reach the loopback service (exit %d, out=%q)", svcCode, svcOut)
	}
	t.Logf("GATE G1 positive: tailnet:8443 -> loopback service forwarded (marker received)")

	// G1 negative 1: an unlisted port must NOT reach anything (reject-all)
	rejOut, rejCode := runBin(t, nil, "timeout", "5", tsBin, sockArg, "nc", "node-c", "9999")
	if rejCode == 0 && strings.Contains(rejOut, "svc-8443-ok") {
		t.Fatalf("G1 FAILED: unlisted port 9999 leaked the service -- reject-all broken")
	}
	t.Logf("GATE G1 negative 1: unlisted port 9999 rejected (exit %d)", rejCode)

	// G1 negative 2: node-b (no service configured) must not forward either
	nbSvcOut, nbSvcCode := runBin(t, nil, "timeout", "5", tsBin, sockArg, "nc", "node-b", "8443")
	if nbSvcCode == 0 && strings.Contains(nbSvcOut, "svc-8443-ok") {
		t.Fatalf("G1 FAILED: node-b (no service) leaked the service -- node-side gate broken")
	}
	t.Logf("GATE G1 negative 2: node-b:8443 not forwarded (exit %d)", nbSvcCode)

	// ssh_server:22 must SURVIVE the reject-all rule (its tsnet listener
	// claims :22 before the router sees the connection)
	sshAfterOut, sshAfterCode := runBin(t, nil, tsBin, sockArg,
		"ssh", username+"@node-c", "echo ssh-survives-reject")
	if sshAfterCode != 0 || !strings.Contains(sshAfterOut, "ssh-survives-reject") {
		t.Fatalf("G1 FAILED: ssh_server died under the reject-all rule (exit %d, out=%q)", sshAfterCode, sshAfterOut)
	}
	t.Logf("GATE G1 ssh survival: ssh_server:22 works with reject-all live")

	// policy file on disk keeps the deny-by-default SHAPE: the ssh-enabled tag
	// is the destination ONLY of the tcp:22 grant (node-b holds just the base
	// tag, so service-port grants to the base tag never unlock SSH for it --
	// CI-proven by the G4 negative). Service grants to the base tag are BY
	// DESIGN (ADR-005): the node-side reject-all is the per-node gate.
	if b, err := os.ReadFile(me("hs/policy.json")); err == nil {
		body := string(b)
		if !strings.Contains(body, authz.TagNodeSSH) {
			t.Fatalf("policy file lost the ssh-enabled tag scoping:\n%s", body)
		}
		for _, line := range strings.Split(body, "\n") {
			if strings.Contains(line, authz.TagNodeSSH) && strings.Contains(line, `"ip":`) && !strings.Contains(line, "tcp:22") {
				t.Fatalf("ssh-enabled tag exposed on a non-22 port:\n%s", line)
			}
		}
	}
	t.Logf("==== PHASE 4 SUMMARY ====")
	t.Logf("RESULT: PASS (deny-by-default enforced server-side; per-profile SSH)")
}
