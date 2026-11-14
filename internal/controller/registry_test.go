package controller

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeMachines(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "machines.json")
	os.WriteFile(p, []byte(body), 0o600)
	return p
}

func TestLoadEmpty(t *testing.T) {
	r, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Machines) != 0 || r.Defaults.ConnectTimeout != 10 {
		t.Fatalf("empty load wrong: %+v", r)
	}
}

func TestLoadAndRoundTrip(t *testing.T) {
	p := writeMachines(t, `{
  "config_version": 1,
  "machines": {
    "school": {
      "display": "校机",
      "shell": "cmd",
      "anchors": [
        {"alias": "school-guest", "local_port": 13401},
        {"alias": "school-guest-pc2"}
      ]
    }
  },
  "defaults": {"connect_timeout_seconds": 7}
}`)
	r, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	m := r.Machines["school"]
	if len(m.Anchors) != 2 || m.Anchors[0].LocalPort != 13401 {
		t.Fatalf("anchors wrong: %+v", m)
	}
	if err := r.Save(p + ".out"); err != nil {
		t.Fatal(err)
	}
	r2, err := Load(p + ".out")
	if err != nil {
		t.Fatal(err)
	}
	if r2.Machines["school"].Anchors[0].Alias != "school-guest" {
		t.Fatal("round trip lost anchor")
	}
}

func TestNamesSorted(t *testing.T) {
	r := &Registry{Machines: map[string]Machine{"b": {}, "a": {}, "c": {}}}
	got := strings.Join(r.Names(), ",")
	if got != "a,b,c" {
		t.Fatalf("names: %s", got)
	}
}

func TestProbeAnchorTCP(t *testing.T) {
	// 127.0.0.1:1 is the documented negative anchor
	if ok, detail := ProbeAnchorTCP(1, 300e6); ok {
		t.Fatalf("port 1 must be closed: %s", detail)
	}
	// port 0 = no anchor configured
	if ok, _ := ProbeAnchorTCP(0, 300e6); ok {
		t.Fatal("port 0 must be a no")
	}
}

func TestFailoverNoReachableAnchor(t *testing.T) {
	p := writeMachines(t, `{"machines": {"dead": {"anchors": [{"local_port": 1}, {"alias": "no-such-host-zz"}]}}}`)
	r, _ := Load(p)
	calls := 0
	_, trail, err := Failover(r, "dead", "ssh", func(a Anchor) []string {
		calls++
		if a.Alias != "" {
			return []string{a.Alias}
		}
		return nil
	})
	if err == nil {
		t.Fatal("dead machine must fail")
	}
	// anchor 1: tcp(local_port=1) fails -> ssh probe runs; anchor 2: ssh probe runs
	if calls != 2 {
		t.Fatalf("ssh probes: got %d want 2 (one per anchor)", calls)
	}
	// trail records EVERY attempt: anchor1 tcp-fail + ssh-fail, anchor2 ssh-fail
	if len(trail) != 3 {
		t.Fatalf("trail must record all attempts: %+v", trail)
	}
	if trail[0].OK || trail[1].OK || trail[2].OK {
		t.Fatal("no anchor should pass")
	}
	if !strings.Contains(trail[0].Detail, "refused") {
		t.Fatalf("tcp failure must carry the real reason: %s", trail[0].Detail)
	}
}

// TestBuildProbeArgsOrder locks the regression from 2026-11-15 E2E: connection
// options and the "-o" flags must precede the destination, and the destination
// must precede the remote command. Putting "-o ... echo" after "user@host" turned
// the marker into a remote command and failed the probe with exit 255.
func TestBuildProbeArgsOrder(t *testing.T) {
	// dialhome anchor shape produced by cli.anchorArgs (co-located case):
	//   ["-i", key, "-o", "StrictHostKeyChecking=accept-new", "-o", "UserKnownHostsFile=/x", "-p", "13441", "user@127.0.0.1"]
	sshArgs := []string{"-i", "/k", "-o", "StrictHostKeyChecking=accept-new", "-o", "UserKnownHostsFile=/x", "-p", "13441", "user@127.0.0.1"}
	args := buildProbeArgs("ssh", sshArgs, 8*1e9)

	destIdx := indexOf(args, "user@127.0.0.1")
	markerIdx := indexOf(args, "echo")
	if destIdx < 0 {
		t.Fatalf("destination missing: %v", args)
	}
	if markerIdx < 0 {
		t.Fatalf("marker missing: %v", args)
	}
	// destination before marker == destination before remote command
	if destIdx > markerIdx {
		t.Fatalf("destination must precede remote command: %v", args)
	}
	// "-o" opts before destination
	if idx := indexOf(args, "BatchMode=yes"); idx < 0 || idx > destIdx {
		t.Fatalf("connection options must precede destination: %v", args)
	}
	// ssh-alias shape: bare alias is the destination, no -p
	aliasArgs := buildProbeArgs("ssh", []string{"school-guest"}, 8*1e9)
	if indexOf(aliasArgs, "school-guest") > indexOf(aliasArgs, "echo") {
		t.Fatalf("alias must precede remote command: %v", aliasArgs)
	}
}

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}
