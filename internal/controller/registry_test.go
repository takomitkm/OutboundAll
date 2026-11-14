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
