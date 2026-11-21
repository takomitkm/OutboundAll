package supervisor

import (
	"testing"
	"time"

	"github.com/takomitkm/outboundall/internal/config"
)

func TestBuildArgsContract(t *testing.T) {
	tun := Tunnel{
		Name:      "ssh",
		Remote:    "127.0.0.1:13441",
		Local:     "127.0.0.1:19022",
		KeyPath:   "keys/tunnel.key",
		RelayAddr: "10.101.54.192:18322",
		RelayUser: "user",
	}
	args := tun.BuildArgs("kh", config.Keepalive{IntervalSeconds: 15, CountMax: 3}, 10)
	joined := ""
	for _, a := range args {
		joined += a + "\x00"
	}
	for _, want := range []string{
		"-N\x00",
		"-R\x00",
		"127.0.0.1:13441:127.0.0.1:19022\x00", // exact literal -- permitlisten matches this (PITFALLS B1)
		"-p\x0018322\x00",
		"-i\x00keys/tunnel.key\x00",
		"BatchMode=yes",
		"ExitOnForwardFailure=yes",
		"ConnectTimeout=10",
		"ServerAliveInterval=15",
		"ServerAliveCountMax=3",
		"user@10.101.54.192",
	} {
		if !contains(joined, want) {
			t.Fatalf("args missing %q: %v", want, args)
		}
	}
	for _, banned := range []string{"localhost:", "0.0.0.0:"} {
		if contains(joined, banned) {
			t.Fatalf("security boundary violated (%q): %v", banned, args)
		}
	}
}

func contains(hay, needle string) bool {
	return len(needle) > 0 && len(hay) >= len(needle) && indexOf(hay, needle) >= 0
}

func indexOf(hay, needle string) int {
	for i := 0; i+len(needle) <= len(hay); i++ {
		match := true
		for j := 0; j < len(needle); j++ {
			if hay[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

func TestProbeRelayRefused(t *testing.T) {
	// port 1 on loopback is our documented negative anchor (permitopen boundary too)
	if ProbeRelay("127.0.0.1:1", 300*time.Millisecond) {
		t.Fatal("nothing listens on 127.0.0.1:1; probe must fail")
	}
}
