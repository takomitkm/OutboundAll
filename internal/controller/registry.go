// Package controller manages the node registry and multi-anchor failover
// (spec §4): the Controller is the only machine holding management keys.
package controller

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"time"
)

const RegistryVersion = 1

// ErrNoAnchor marks a machine whose every anchor failed.
var ErrNoAnchor = errors.New("controller: no reachable anchor for node")

// Anchor is one way to reach a node: an existing ssh config alias, or OutboundAll's
// own via-relay path. The Controller never forces the user to migrate ~/.ssh/config.
type Anchor struct {
	Alias      string `json:"alias,omitempty"`       // ~/.ssh/config Host alias
	Node       string `json:"node,omitempty"`        // OutboundAll node name (kind=OutboundAll)
	RelayAddr  string `json:"via_relay,omitempty"`   // host:port (kind=OutboundAll)
	RelayPort  int    `json:"relay_port,omitempty"`  // relay ssh port (kind=OutboundAll)
	RelayUser  string `json:"relay_user,omitempty"`  // relay ssh user (kind=OutboundAll)
	RemotePort int    `json:"remote_port,omitempty"` // node sshd port on the relay loopback (kind=OutboundAll)
	LocalPort  int    `json:"local_port,omitempty"`  // optional pre-built -L anchor
	Note       string `json:"note,omitempty"`
}

// Machine is one controlled node from the Controller's perspective.
type Machine struct {
	Display string   `json:"display,omitempty"`
	Shell   string   `json:"shell,omitempty"` // cmd | bash | powershell (hint only)
	Note    string   `json:"note,omitempty"`
	Anchors []Anchor `json:"anchors"`
}

// Registry is the controller-side machines.json (CONFIG_SCHEMA §3).
type Registry struct {
	ConfigVersion int                `json:"config_version"`
	Machines      map[string]Machine `json:"machines"`
	Defaults      struct {
		ConnectTimeout int `json:"connect_timeout_seconds"`
	} `json:"defaults"`
}

// Load reads machines.json (missing file -> empty registry).
func Load(path string) (*Registry, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			r := &Registry{ConfigVersion: RegistryVersion, Machines: map[string]Machine{}}
			r.Defaults.ConnectTimeout = 10
			return r, nil
		}
		return nil, err
	}
	var r Registry
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("machines %s: %w", path, err)
	}
	if r.Machines == nil {
		r.Machines = map[string]Machine{}
	}
	if r.Defaults.ConnectTimeout <= 0 {
		r.Defaults.ConnectTimeout = 10
	}
	return &r, nil
}

// Save writes atomically with 0600.
func (r *Registry) Save(path string) error {
	raw, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	os.Chmod(tmpName, 0o600)
	return os.Rename(tmpName, path)
}

// Names returns the machine names sorted.
func (r *Registry) Names() []string {
	var out []string
	for name := range r.Machines {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// ProbeResult is one anchor attempt's outcome (ordered failover trail).
type ProbeResult struct {
	Anchor Anchor
	OK     bool
	Detail string
}

// ProbeAnchorTCP checks an optional pre-built local loopback port anchor.
func ProbeAnchorTCP(port int, timeout time.Duration) (bool, string) {
	if port <= 0 {
		return false, "no local port"
	}
	c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), timeout)
	if err != nil {
		return false, err.Error()
	}
	c.Close()
	return true, "tcp ok"
}

// ProbeAnchorSSH runs a cheap ssh health command through the anchor.
// sshArgs: connection args produced by anchorArgs -- connection options (incl. an
// optional "-p" pair) followed by the destination (user@host or a bare ssh-alias).
// The destination MUST stay before the remote command; the "-o" connection options
// are inserted before it (evidence: 2026-11-15 E2E -- placing them after the
// destination turned the marker into a remote command and failed with exit 255).
func ProbeAnchorSSH(sshBinary string, sshArgs []string, timeout time.Duration) (bool, string) {
	if len(sshArgs) == 0 {
		return false, "no ssh args"
	}
	args := buildProbeArgs(sshBinary, sshArgs, timeout)
	cmd := execCommand(args[0], args[1:]...)
	out, err := cmd.Output()
	if err != nil {
		return false, firstLine(errString(err, out))
	}
	if !containsLine(string(out), "OutboundAll-probe-ok") {
		return false, "probe command produced no marker"
	}
	return true, "ssh ok"
}

// buildProbeArgs constructs the ssh argument vector for a connectivity probe.
// sshArgs is connection options (incl. an optional "-p" pair) followed by the
// destination (user@host or a bare ssh-alias); the destination MUST stay before
// the remote command -- the "-o" connection options are inserted before it
// (evidence: 2026-11-15 E2E -- putting them after the destination turned the
// marker into a remote command and failed with exit 255). Exported for tests.
func buildProbeArgs(sshBinary string, sshArgs []string, timeout time.Duration) []string {
	if len(sshArgs) == 0 {
		return []string{sshBinary}
	}
	dest := sshArgs[len(sshArgs)-1]
	opts := append([]string{}, sshArgs[:len(sshArgs)-1]...)
	args := append([]string{sshBinary}, opts...)
	args = append(args,
		"-o", "BatchMode=yes",
		"-o", fmt.Sprintf("ConnectTimeout=%d", int(timeout.Seconds())),
		dest, "echo", "OutboundAll-probe-ok")
	return args
}

// Failover walks a machine's anchors in order; returns the winning sshArgs plus
// the full trail (spec §4.2: "失败时记录实际原因").
func Failover(r *Registry, machineName, sshBinary string, argsFor func(Anchor) []string) ([]string, []ProbeResult, error) {
	m, ok := r.Machines[machineName]
	if !ok {
		return nil, nil, fmt.Errorf("machine %q not registered", machineName)
	}
	timeout := time.Duration(r.Defaults.ConnectTimeout) * time.Second
	var trail []ProbeResult
	for _, a := range m.Anchors {
		args := argsFor(a)
		if a.LocalPort > 0 {
			tcpOK, tcpDetail := ProbeAnchorTCP(a.LocalPort, timeout)
			trail = append(trail, ProbeResult{a, tcpOK, tcpDetail})
			if tcpOK {
				return args, trail, nil
			}
		}
		sshOK, sshDetail := ProbeAnchorSSH(sshBinary, args, timeout)
		trail = append(trail, ProbeResult{a, sshOK, sshDetail})
		if sshOK {
			return args, trail, nil
		}
	}
	return nil, trail, fmt.Errorf("%w: %s", ErrNoAnchor, machineName)
}

func firstLine(s string) string {
	for _, l := range splitLines(s) {
		if l != "" {
			return l
		}
	}
	return ""
}

func errString(err error, out []byte) string {
	if len(out) > 0 {
		return firstNonEmptyLine(string(out))
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
		return firstNonEmptyLine(string(exitErr.Stderr))
	}
	return err.Error()
}

func firstNonEmptyLine(s string) string {
	for _, l := range splitLines(s) {
		t := trim(l)
		if t == "" || isBanner(t) {
			continue
		}
		return t
	}
	return ""
}

// isBanner skips pure-separator lines ("@@@@@") that ssh prints around warnings.
func isBanner(s string) bool {
	if len(s) < 5 {
		return false
	}
	for _, r := range s {
		if r != '@' {
			return false
		}
	}
	return true
}

func containsLine(s, marker string) bool {
	for _, l := range splitLines(s) {
		if trim(l) == marker {
			return true
		}
	}
	return false
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

func trim(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t' || s[0] == '\r') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
