// Package hstest hosts the Headscale + sing-box integration harness.
//
// The helpers in this file compile on every build so that `go vet ./...`
// and `go test ./...` see a valid package; the actual E2E chain lives in
// e2e_test.go behind the `hs_integration` build tag (it needs the pinned
// headscale / sing-box / tailscale binaries downloaded by
// hs/scripts/e2e-deps.sh).
package hstest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------- helpers --

func envBin(t *testing.T, name string) string {
	t.Helper()
	v := os.Getenv(name)
	if v == "" {
		t.Fatalf("missing env %s (source .e2e-deps/paths.env first)", name)
	}
	if _, err := os.Stat(v); err != nil {
		t.Fatalf("env %s points to missing file %s: %v", name, v, err)
	}
	return v
}

type proc struct {
	cmd     *exec.Cmd
	logPath string
	done    chan error
}

// startProc launches a background process with its output captured to a log
// file inside the test's temp dir; the process is killed on test cleanup.
func startProc(t *testing.T, logName string, argv []string) *proc {
	t.Helper()
	p := &proc{cmd: exec.Command(argv[0], argv[1:]...), done: make(chan error, 1)}
	p.cmd.Env = os.Environ()
	logFile := filepath.Join(t.TempDir(), logName)
	f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatalf("open %s: %v", logFile, err)
	}
	p.logPath = logFile
	p.cmd.Stdout = f
	p.cmd.Stderr = f
	if err := p.cmd.Start(); err != nil {
		f.Close()
		t.Fatalf("start %s: %v", argv[0], err)
	}
	go func() { p.done <- p.cmd.Wait(); f.Close() }()
	t.Cleanup(func() {
		if p.cmd.Process != nil {
			_ = p.cmd.Process.Kill()
		}
		// done may already be drained (e.g. the test consumed it after an
		// explicit SIGTERM) -- never block cleanup on it.
		select {
		case <-p.done:
		case <-time.After(10 * time.Second):
			t.Logf("cleanup: %s did not report exit within 10s", filepath.Base(p.logPath))
		}
	})
	return p
}

// dumpLog prints the tail of a process log into the test output (CI-visible).
func dumpLog(t *testing.T, p *proc, why string) {
	t.Helper()
	b, err := os.ReadFile(p.logPath)
	if err != nil {
		t.Logf("log %s unreadable: %v", p.logPath, err)
		return
	}
	s := string(b)
	if len(s) > 4000 {
		s = s[len(s)-4000:]
	}
	t.Logf("---- %s (%s) ----\n%s", filepath.Base(p.logPath), why, s)
}

func runCmd(t *testing.T, timeout time.Duration, argv []string) (string, int, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var out strings.Builder
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.String(), exitCodeOf(err), err
}

func exitCodeOf(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if ok := asExit(err, &ee); ok {
		return ee.ExitCode()
	}
	return -1
}

func asExit(err error, target **exec.ExitError) bool {
	ee, ok := err.(*exec.ExitError)
	if ok {
		*target = ee
	}
	return ok
}

// waitForErr polls until fn returns nil or the deadline passes.
func waitForErr(timeout time.Duration, what string, fn func() error) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := fn(); err == nil {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for %s after %v", what, timeout)
}

func httpHealth(url string) error {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != 200 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

// ------------------------------------------------------- headscale client --

type hsUser struct {
	ID   uint64 `json:"id"`
	Name string `json:"name"`
}

type hsNode struct {
	ID       uint64 `json:"id"`
	Hostname string `json:"hostname"`
	// 0.29 protobuf-json shape may use "name" instead of "hostname"
	Name   string `json:"name"`
	Online bool   `json:"online"`
}

// dumpDir logs a directory listing (names + sizes) for state forensics.
func dumpDir(t *testing.T, dir string, why string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Logf("dir %s unreadable (%s): %v", dir, why, err)
		return
	}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			t.Logf("  %s (%s) [stat err]", e.Name(), why)
			continue
		}
		t.Logf("  %s size=%d dir=%v (%s)", e.Name(), info.Size(), e.IsDir(), why)
	}
}

func hsCLI(t *testing.T, ws string, args ...string) string {
	t.Helper()
	argv := append([]string{os.Getenv("HS_BIN"), "--config", filepath.Join(ws, "hs", "config.yaml")}, args...)
	out, code, _ := runCmd(t, 30*time.Second, argv)
	if code != 0 {
		t.Fatalf("headscale %s failed (exit %d): %s", strings.Join(args, " "), code, out)
	}
	return out
}

// hsNodes parses `headscale nodes list -o json`, tolerating log lines before
// the JSON array. Fatal on CLI errors (for assertions).
func hsNodes(t *testing.T, ws string) []hsNode {
	out := hsCLI(t, ws, "nodes", "list", "-o", "json")
	return parseNodes(t, out)
}

// hsNodesSafe is the polling variant: nil on CLI/parse errors (headscale may
// still be initializing while a poll is in flight).
func hsNodesSafe(t *testing.T, ws string) []hsNode {
	argv := []string{os.Getenv("HS_BIN"), "--config", filepath.Join(ws, "hs", "config.yaml"), "nodes", "list", "-o", "json"}
	out, code, _ := runCmd(t, 30*time.Second, argv)
	if code != 0 {
		return nil
	}
	return parseNodes(t, out)
}

func parseNodes(t *testing.T, out string) []hsNode {
	i := strings.Index(out, "[")
	if i < 0 {
		return nil
	}
	var nodes []hsNode
	if err := json.Unmarshal([]byte(out[i:]), &nodes); err != nil {
		t.Logf("parse nodes list failed (%v); raw: %s", err, out)
		return nil
	}
	return nodes
}

func jsonKey(t *testing.T, out, field string) string {
	t.Helper()
	i := strings.Index(out, "{")
	if i < 0 {
		t.Fatalf("no JSON in output: %s", out)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out[i:]), &m); err != nil {
		t.Fatalf("parse json: %v\nraw: %s", err, out)
	}
	v, ok := m[field].(string)
	if !ok || v == "" {
		t.Fatalf("field %q missing in output: %v", field, m)
	}
	return v
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func jsonUnmarshal(data string, v any) error {
	return json.Unmarshal([]byte(data), v)
}
