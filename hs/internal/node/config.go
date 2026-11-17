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

// Service is one loopback service the node exposes to the tailnet (ADR-005):
// tailnet-side port == target port, target must be a 127.0.0.1 literal.
// Exposure is the AND of (a) this node-side allow and (b) a headscale grant
// for the port (who may dial it).
type Service struct {
	Name   string `json:"name"`
	Target string `json:"target"`
}

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

	// Services lists loopback services exposed to the tailnet (Phase 6).
	// Empty = nothing forwarded (the route engine rejects all tailnet
	// inbound except the ssh_server's own :22).
	Services []Service `json:"services,omitempty"`

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
	seen := map[string]bool{}
	seenPort := map[int]bool{}
	for _, svc := range c.Services {
		if !hostnameRe.MatchString(svc.Name) || seen[svc.Name] {
			return fmt.Errorf("service name %q invalid or duplicate", svc.Name)
		}
		seen[svc.Name] = true
		host, portStr, ok := strings.Cut(svc.Target, ":")
		if !ok || host != "127.0.0.1" {
			return fmt.Errorf("service %s target %q must be a 127.0.0.1:PORT literal (loopback invariant)", svc.Name, svc.Target)
		}
		port := 0
		for _, r := range portStr {
			if r < '0' || r > '9' {
				return fmt.Errorf("service %s target port %q invalid", svc.Name, portStr)
			}
			port = port*10 + int(r-'0')
		}
		if port < 1 || port > 65535 || seenPort[port] {
			return fmt.Errorf("service %s port %d out of range or duplicate", svc.Name, port)
		}
		seenPort[port] = true
	}
	return nil
}

// ServicePorts returns the tailnet-facing ports of the configured services.
func (c *Config) ServicePorts() []int {
	ports := make([]int, 0, len(c.Services))
	for _, svc := range c.Services {
		_, portStr, _ := strings.Cut(svc.Target, ":")
		port := 0
		for _, r := range portStr {
			port = port*10 + int(r-'0')
		}
		ports = append(ports, port)
	}
	return ports
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

// SingBoxConfig renders the sing-box JSON for this node without any auth key
// (display/validation shape). disable_forwarding follows the deny posture
// (ADR-005/006).
func (c *Config) SingBoxConfig() (string, error) {
	return c.singBoxDoc("")
}

// singBoxDoc renders the endpoint config, optionally carrying the single-use
// preauth key for this boot (ADR-002 resume path). The caller owns file
// permissions (0600) whenever authKey != "".
func (c *Config) singBoxDoc(authKey string) (string, error) {
	if err := c.Validate(); err != nil {
		return "", err
	}
	endpoint := map[string]any{
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
	}
	if authKey != "" {
		endpoint["auth_key"] = authKey
	}

	doc := map[string]any{
		"log":       map[string]any{"level": "info"},
		"endpoints": []any{endpoint},
	}
	// The tailnet inbound is deny-by-default ALWAYS: sing-box's bare default
	// forwards any tailnet dial to loopback on the SAME port (CI-observed
	// connect-then-close on :8443), which is arbitrary loopback exposure --
	// a security gap. Reject everything not explicitly allowed; the
	// ssh_server's :22 is claimed by its tsnet listener before the router
	// (verified by the e2e ssh probe with the reject rule live).
	var outbounds []any
	var rules []any
	for _, svc := range c.Services {
		_, portStr, _ := strings.Cut(svc.Target, ":")
		port := 0
		for _, r := range portStr {
			port = port*10 + int(r-'0')
		}
		outbounds = append(outbounds, map[string]any{
			"type": "direct",
			"tag":  "srv-" + svc.Name,
		})
		// the endpoint remaps dials to the node's own tailnet IP to
		// 127.0.0.1 before routing, so the direct outbound reaches the
		// loopback service with no override
		rules = append(rules, map[string]any{
			"inbound":          []string{"ts"},
			"destination_port": []int{port},
			"outbound":         "srv-" + svc.Name,
		})
	}
	rules = append(rules, map[string]any{
		"inbound": []string{"ts"},
		"action":  "reject",
	})
	if len(outbounds) > 0 {
		doc["outbounds"] = outbounds
	}
	doc["route"] = map[string]any{"rules": rules}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b) + "\n", nil
}
