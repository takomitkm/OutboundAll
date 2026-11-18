//go:build hs_integration

// Phase-7 chain (PLAN step 9 / ADR-004): control-plane outage and recovery
// with identity persistence -- failure classes 4/7 of the nine-class matrix.
//
//	H4a  headscale down  -> the node process stays alive (WireGuard/netmap
//	                      holds; no crash, no restart storm)
//	H4b  headscale back  -> the node reconnects to the control plane with the
//	                      SAME node identity (no duplicate registration)
//
// Evidence for the other classes is cited in ADR-004 from earlier gates:
// class 1 (direct path) phase1b ping, class 8 (crash/restart) phase3 resume,
// class 9 (session drop) phase1b G3 observation.
package hstest

import (
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"
)

func TestPhase7Chain(t *testing.T) {
	if os.Getenv("HS_BIN") == "" {
		t.Skip("HS_BIN not set (source .e2e-deps/paths.env)")
	}
	hsBin := envBin(t, "HS_BIN")
	sbBin := envBin(t, "SB_BIN")

	ws := t.TempDir()
	me := func(sub string) string { return ws + "/" + sub }
	if err := os.MkdirAll(me("hs"), 0o755); err != nil {
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
    region_id: 906
    region_code: ci7
    region_name: CI7 Embedded DERP
    verify_clients: false
    stun_listen_addr: 127.0.0.1:3483
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
		me("hs/noise.key"), me("hs/derp.key"), me("hs/db.sqlite"), me("hs/headscale.sock"))
	if err := os.WriteFile(me("hs/config.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	// ---- headscale up; node registers via a preauth key ----
	hs := startBin(t, "headscale7", nil, hsBin, "serve", "--config", me("hs/config.yaml"))
	if err := waitForErr(60*time.Second, "headscale7 /health", func() error {
		return httpHealth("http://127.0.0.1:8080/health")
	}); err != nil {
		t.Fatal(err)
	}
	if out, code := runBin(t, nil, hsBin, "--config", me("hs/config.yaml"), "users", "create", "ci"); code != 0 {
		t.Fatalf("users create failed: %s", out)
	}
	keyOut, keyCode := runBin(t, nil, hsBin, "--config", me("hs/config.yaml"),
		"preauthkeys", "create", "--user", "1", "--expiration", "1h", "-o", "json")
	if keyCode != 0 {
		t.Fatalf("preauthkey create failed: %s", keyOut)
	}
	nodeKey := jsonKey(t, keyOut, "key")

	nodeCfg := fmt.Sprintf(`{
  "log": { "level": "info" },
  "endpoints": [{
    "type": "tailscale",
    "tag": "ts",
    "control_url": "http://127.0.0.1:8080",
    "hostname": "node-f",
    "auth_key": %q,
    "state_directory": %q,
    "system_interface": false
  }]
}`, nodeKey, me("node/state"))
	if err := os.WriteFile(me("node/config.json"), []byte(nodeCfg), 0o644); err != nil {
		t.Fatal(err)
	}
	node := startBin(t, "singbox7", nil, sbBin, "run", "-c", me("node/config.json"))

	id := uint64(0)
	if err := waitForErr(120*time.Second, "node-f registered", func() error {
		for _, n := range hsNodesSafe(t, ws) {
			if (strings.HasPrefix(n.Hostname, "node-f") || strings.HasPrefix(n.Name, "node-f")) && n.Online {
				id = n.ID
				return nil
			}
		}
		return fmt.Errorf("not online yet")
	}); err != nil {
		dumpLog(t, node, "node during registration")
		t.Fatal(err)
	}
	t.Logf("node-f online (headscale id %d)", id)

	// ---- H4a: headscale DIES. The node must keep running (no crash, no
	// restart storm; a live WireGuard session would hold) ----
	hs.kill(t)
	if err := waitForErr(10*time.Second, "headscale to stop serving", func() error {
		return httpHealth("http://127.0.0.1:8080/health")
	}); err == nil {
		t.Fatal("headscale still healthy after kill")
	}
	time.Sleep(15 * time.Second) // outage window: ladder would show restarts if buggy
	if !node.alive() {
		t.Fatal("H4a FAILED: node process died during the control-plane outage")
	}
	t.Logf("GATE H4a PASS: node process survived a %d s control-plane outage", 15)

	// ---- H4b: headscale restarts (same state). The node reconnects with the
	// SAME identity -- no duplicate node ----
	hs2 := startBin(t, "headscale7b", nil, hsBin, "serve", "--config", me("hs/config.yaml"))
	_ = hs2
	if err := waitForErr(60*time.Second, "headscale7b /health", func() error {
		return httpHealth("http://127.0.0.1:8080/health")
	}); err != nil {
		t.Fatal(err)
	}
	if err := waitForErr(120*time.Second, "node-f back online with same id", func() error {
		nodes := hsNodesSafe(t, ws)
		count := 0
		for _, n := range nodes {
			if strings.HasPrefix(n.Hostname, "node-f") || strings.HasPrefix(n.Name, "node-f") {
				count++
				if n.ID != id {
					return fmt.Errorf("identity changed: %d -> %d", id, n.ID)
				}
				if !n.Online {
					return fmt.Errorf("present but offline")
				}
			}
		}
		if count != 1 {
			return fmt.Errorf("expected exactly 1 node-f, got %d", count)
		}
		return nil
	}); err != nil {
		dumpLog(t, node, "node during recovery")
		t.Fatal(err)
	}
	t.Logf("GATE H4b PASS: node reconnected to the restarted control plane with identity %d intact", id)
	t.Logf("==== PHASE 7 SUMMARY ====")
	t.Logf("RESULT: PASS (control-plane outage survival + identity-persistent recovery)")
}
