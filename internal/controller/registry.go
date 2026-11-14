// Package controller manages the node registry and multi-anchor failover
// (spec §4): the Controller is the only machine holding management keys.
package controller

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const RegistryVersion = 1

// ErrNoAnchor marks a machine whose every anchor failed.
var ErrNoAnchor = errors.New("controller: no reachable anchor for node")

// Anchor is one way to reach a node: an existing ssh config alias, or dialhome's
// own via-relay path. The Controller never forces the user to migrate ~/.ssh/config.
type Anchor struct {
	Alias     string `json:"alias,omitempty"`      // ~/.ssh/config Host alias
	Node      string `json:"node,omitempty"`       // dialhome node name (kind=dialhome)
	RelayAddr string `json:"via_relay,omitempty"`  // host:port (kind=dialhome)
	LocalPort int    `json:"local_port,omitempty"` // optional pre-built -L anchor
	Note      string `json:"note,omitempty"`
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
// sshArgs: the argument vector addressing THIS anchor (never shell-interpolated).
func ProbeAnchorSSH(sshBinary string, sshArgs []string, timeout time.Duration) (bool, string) {
	args := append(append([]string{}, sshArgs...),
		"-o", "BatchMode=yes",
		"-o", fmt.Sprintf("ConnectTimeout=%d", int(timeout.Seconds())),
		"echo dialhome-probe-ok")
	cmd := execCommand(sshBinary, args...)
	out, err := cmd.Output()
	if err != nil {
		return false, firstLine(errString(err, out))
	}
	if !containsLine(string(out), "dialhome-probe-ok") {
		return false, "probe command produced no marker"
	}
	return true, "ssh ok"
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
		return string(out)
	}
	return err.Error()
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
