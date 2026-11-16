// Package node implements the hs-node runtime: configuration, directory
// layout, sing-box acquisition, process supervision and user-level autostart.
//
// Invariants (PLAN.md §7, docs/hs/SECURITY_MODEL.md):
//   - config.json never contains secrets (resume token stays out of the file)
//   - forwarding is OFF unless explicitly enabled (ADR-005/006 deny posture)
//   - the control plane URL is https:// in production; plain http:// is only
//     allowed through an explicit test escape hatch
//   - the node identity lives in state/tailscale (sing-box state_directory)
package node

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ConfigVersion is bumped on incompatible config.json changes.
const ConfigVersion = 1

// DefaultForwarding is the ADR-005/006 posture: OFF.
const DefaultForwarding = false

var hostnameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// SSHServer mirrors the sing-box ssh_server object we generate.
type SSHServer struct {
	Enabled           bool `json:"enabled"`
	DisablePTY        bool `json:"disable_pty,omitempty"`
	DisableSFTP       bool `json:"disable_sftp,omitempty"`
	DisableForwarding bool `json:"disable_forwarding"`
}

// Config is the persisted node configuration (config.json). It must stay
// free of secrets: anything sensitive is injected at runtime, not stored.
type Config struct {
	ConfigVersion int `json:"config_version"`
	// control plane base, e.g. https://hs.example.net
	Server string `json:"server"`
	// hs-controller API base (enroll/resume)
	Controller string `json:"controller"`
	Hostname   string `json:"hostname"`

	SSHServer SSHServer `json:"ssh_server"`
	// Forwarding enables the sing-box SSH forwarding surface. Default OFF
	// (ADR-005): turning it on is an explicit per-node profile decision.
	Forwarding bool `json:"forwarding"`

	StateDir string `json:"state_dir"`
	// TestAllowHTTP permits plain-http control plane URLs; CI-only escape
	// hatch, never written by production enroll.
	TestAllowHTTP bool `json:"test_allow_http,omitempty"`
}

// Validate enforces the config invariants; a rejected config must never run.
func (c *Config) Validate() error {
	if c.ConfigVersion != ConfigVersion {
		return fmt.Errorf("config_version %d not supported (want %d)", c.ConfigVersion, ConfigVersion)
	}
	if !strings.HasPrefix(c.Server, "https://") {
		if !(c.TestAllowHTTP && strings.HasPrefix(c.Server, "http://")) {
			return fmt.Errorf("server must be https:// (got %q); plain http needs TestAllowHTTP", c.Server)
		}
	}
	if !hostnameRe.MatchString(c.Hostname) {
		return fmt.Errorf("hostname %q invalid (want [a-z0-9][a-z0-9-]{0,62})", c.Hostname)
	}
	if c.StateDir == "" {
		return fmt.Errorf("state_dir required")
	}
	if c.Forwarding {
		// Explicitly allowed by the profile, but the SSH server's own
		// forwarding kill-switch must then stay consistent (ADR-005).
		if c.SSHServer.DisableForwarding {
			return fmt.Errorf("forwarding=true conflicts with ssh_server.disable_forwarding=true")
		}
	} else if !c.SSHServer.DisableForwarding {
		return fmt.Errorf("forwarding=false requires ssh_server.disable_forwarding=true (deny posture)")
	}
	return nil
}

// Load reads and validates config.json from dir.
func Load(dir string) (*Config, error) {
	b, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parse config.json: %w", err)
	}
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config.json: %w", err)
	}
	return &c, nil
}

// Save writes config.json atomically (tmp + rename in the same directory).
func (c *Config) Save(dir string) error {
	if err := c.Validate(); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	final := filepath.Join(dir, "config.json")
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, final)
}

// SingBoxConfig renders the sing-box JSON for this node. disable_forwarding
// follows the deny posture (ADR-005/006); resume auth keys are passed on the
// command line / environment by the supervisor, never written here.
func (c *Config) SingBoxConfig() (string, error) {
	if err := c.Validate(); err != nil {
		return "", err
	}
	doc := map[string]any{
		"log": map[string]any{"level": "info"},
		"endpoints": []any{map[string]any{
			"type":             "tailscale",
			"tag":              "ts",
			"control_url":      c.Server,
			"hostname":         c.Hostname,
			"state_directory":  c.StateDir,
			"system_interface": false,
			"ssh_server": map[string]any{
				"enabled":            c.SSHServer.Enabled,
				"disable_forwarding": c.SSHServer.DisableForwarding,
			},
		}},
	}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b) + "\n", nil
}
