package node

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// validConfig returns a config passing all invariants (https, deny-forwarding).
func validConfig() *Config {
	return &Config{
		ConfigVersion: ConfigVersion,
		Server:        "https://hs.example.net",
		Controller:    "https://hs.example.net/mc",
		Hostname:      "win-lab",
		SSHServer:     SSHServer{Enabled: true, DisableForwarding: true},
		Forwarding:    false,
		StateDir:      `C:\Users\u\AppData\Local\OutboundAll\state`,
	}
}

// N-cfg-1: a valid config round-trips through Save/Load unchanged.
func TestConfigRoundTrip(t *testing.T) {
	dir := t.TempDir()
	c := validConfig()
	if err := c.Save(dir); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Server != c.Server || got.Hostname != c.Hostname || got.Forwarding != false {
		t.Fatalf("round trip mismatch: %+v", got)
	}
}

// N-cfg-2 (N13' posture): plain-http server URLs are rejected unless the
// explicit CI escape hatch is set.
func TestConfigRejectsPlainHTTP(t *testing.T) {
	c := validConfig()
	c.Server = "http://hs.example.net"
	if err := c.Validate(); err == nil {
		t.Fatal("plain http accepted without TestAllowHTTP -- deny posture broken")
	}
	c.TestAllowHTTP = true
	if err := c.Validate(); err != nil {
		t.Fatalf("http with TestAllowHTTP rejected: %v", err)
	}
}

// N-cfg-3: forwarding cannot be enabled while the SSH server kill-switch is on.
func TestConfigForwardingConflicts(t *testing.T) {
	c := validConfig()
	c.Forwarding = true
	if err := c.Validate(); err == nil {
		t.Fatal("forwarding=true accepted with disable_forwarding=true -- ADR-005 conflict undetected")
	}
	c.SSHServer.DisableForwarding = false
	if err := c.Validate(); err != nil {
		t.Fatalf("forwarding=true with ssh forwarding allowed rejected: %v", err)
	}
}

// N-cfg-4: deny posture is structural -- forwarding=false MUST pair with
// disable_forwarding=true in the rendered sing-box config.
func TestSingBoxConfigDenyForwarding(t *testing.T) {
	c := validConfig()
	doc, err := c.SingBoxConfig()
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(doc, `"disable_forwarding": true`) {
		t.Fatalf("rendered config lost the deny-forwarding posture:\n%s", doc)
	}
	if strings.Contains(doc, "auth_key") {
		t.Fatalf("rendered config contains an auth_key -- secrets must stay out of generated config:\n%s", doc)
	}
}

// N-cfg-5: no secret field ever reaches config.json on disk.
func TestSaveWritesNoSecrets(t *testing.T) {
	dir := t.TempDir()
	c := validConfig()
	if err := c.Save(dir); err != nil {
		t.Fatalf("save: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"auth_key", "resume_token", "private_key", "password"} {
		if strings.Contains(strings.ToLower(string(b)), secret) {
			t.Fatalf("config.json contains secret-shaped field %q:\n%s", secret, b)
		}
	}
}

// N-cfg-6: hostname charset is enforced (node names become headscale
// hostnames and audit keys).
func TestConfigRejectsBadHostname(t *testing.T) {
	for _, bad := range []string{"", "UPPER", "with space", "../escape", "-lead", "a_b"} {
		c := validConfig()
		c.Hostname = bad
		if err := c.Validate(); err == nil {
			t.Fatalf("hostname %q accepted", bad)
		}
	}
}

// N-cfg-7: unsupported config_version refuses to load (forward-compat guard).
func TestConfigRejectsWrongVersion(t *testing.T) {
	dir := t.TempDir()
	c := validConfig()
	c.ConfigVersion = 99
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("config_version 99 accepted")
	}
}
