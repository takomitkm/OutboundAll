// headscale.go -- the thin client hs-controller uses to drive the local
// headscale CLI (stdlib exec, no gRPC dependency). The interface exists so
// unit tests can run against a fake; the real CLI is exercised end-to-end in
// the CI hs-e2e chain.
package controller

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// Headscale drives the local headscale binary.
type Headscale struct {
	BinPath    string
	ConfigPath string
	UserID     uint64
	ControlURL string

	// realID resolves the numeric user id from a name once at startup.
	resolved bool
}

// Only Key is needed; the remaining fields vary in shape across headscale
// versions (e.g. expiration may be null) and are ignored on purpose.
type preauthKeyJSON struct {
	Key string `json:"key"`
}

func (h *Headscale) cli(args ...string) (string, int, error) {
	argv := append([]string{h.BinPath, "--config", h.ConfigPath}, args...)
	cmd := exec.Command(argv[0], argv[1:]...)
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	}
	return out.String(), code, err
}

// CreatePreAuthKey mints a single-use key with a short expiry for the
// configured user.
func (h *Headscale) CreatePreAuthKey() (string, error) {
	if h.BinPath == "" {
		return "", ErrNotImplemented
	}
	out, code, err := h.cli("preauthkeys", "create",
		"--user", fmt.Sprint(h.UserID),
		"--expiration", "1h",
		"-o", "json")
	if code != 0 || err != nil {
		return "", fmt.Errorf("preauthkeys create (exit %d): %s", code, out)
	}
	i := strings.Index(out, "{")
	if i < 0 {
		return "", fmt.Errorf("no JSON in preauthkeys output: %s", out)
	}
	var parsed preauthKeyJSON
	if err := json.Unmarshal([]byte(out[i:]), &parsed); err != nil {
		return "", fmt.Errorf("parse preauthkeys output: %v: %s", err, out)
	}
	if parsed.Key == "" {
		return "", fmt.Errorf("empty preauth key: %s", out)
	}
	return parsed.Key, nil
}

// ExpireNode blocks reconnection for a node by hostname (best effort: the
// node may not exist in headscale yet -- that is not an error here).
func (h *Headscale) ExpireNode(hostname string) error {
	if h.BinPath == "" {
		return ErrNotImplemented
	}
	out, code, err := h.cli("nodes", "expire", "--identifier", hostname)
	if code != 0 {
		if strings.Contains(out, "not found") {
			return nil
		}
		return fmt.Errorf("nodes expire %s (exit %d): %s", hostname, code, out)
	}
	_ = err
	return nil
}

// ListNodes returns the raw headscale nodes JSON (used by health/status).
func (h *Headscale) ListNodes() (string, error) {
	if h.BinPath == "" {
		return "", ErrNotImplemented
	}
	out, code, err := h.cli("nodes", "list", "-o", "json")
	if code != 0 || err != nil {
		return "", fmt.Errorf("nodes list (exit %d): %s", code, out)
	}
	return out, nil
}

// ResolveUserID maps a headscale user name to its numeric id (done once).
func (h *Headscale) ResolveUserID(name string) error {
	out, code, err := h.cli("users", "list", "-o", "json")
	if code != 0 || err != nil {
		return fmt.Errorf("users list (exit %d): %s", code, out)
	}
	i := strings.Index(out, "[")
	if i < 0 {
		return fmt.Errorf("no JSON in users output: %s", out)
	}
	var users []struct {
		ID   uint64 `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(out[i:]), &users); err != nil {
		return fmt.Errorf("parse users output: %v: %s", err, out)
	}
	for _, u := range users {
		if u.Name == name {
			h.UserID = u.ID
			h.resolved = true
			return nil
		}
	}
	return fmt.Errorf("headscale user %q not found", name)
}

// FakeHeadscale is the unit-test double: preauth keys are deterministic and
// counted; expiration is recorded.
type FakeHeadscale struct {
	ControlURL string
	Keys       []string
	FailNext   error
	Expired    []string
}

func (f *FakeHeadscale) CreatePreAuthKey() (string, error) {
	if f.FailNext != nil {
		err := f.FailNext
		f.FailNext = nil
		return "", err
	}
	f.Keys = append(f.Keys, fmt.Sprintf("fake-key-%d", len(f.Keys)+1))
	return f.Keys[len(f.Keys)-1], nil
}

func (f *FakeHeadscale) ExpireNode(hostname string) error {
	f.Expired = append(f.Expired, hostname)
	return nil
}
