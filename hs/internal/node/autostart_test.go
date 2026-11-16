package node

import (
	"strings"
	"testing"
)

// N-as-1: the autostart command line is exactly "BIN run" -- a drifted
// command (wrong binary path, extra shell) would silently change what starts.
func TestRunCommand(t *testing.T) {
	got := RunCommand(`/X\Y\hs-node.exe`)
	if got != `"/X\Y\hs-node.exe" run` {
		t.Fatalf("RunCommand = %q", got)
	}
	if got := RunCommand("/usr/local/bin/hs-node"); got != `"/usr/local/bin/hs-node" run` {
		t.Fatalf("RunCommand = %q", got)
	}
}

// N-as-2: the HKCU Run key is the user-level hive path (never HKLM).
func TestWindowsRunKeyUserLevel(t *testing.T) {
	if !strings.HasPrefix(WindowsRunKey, `Software\Microsoft\Windows\CurrentVersion\Run`) {
		t.Fatalf("unexpected run key %q", WindowsRunKey)
	}
	if strings.Contains(strings.ToLower(WindowsRunKey), "hklm") {
		t.Fatal("HKLM leaked into the user-level autostart key")
	}
}

// N-as-3: the systemd unit enables restart-on-failure with a 5s cadence and
// never references root or system paths.
func TestSystemdUnit(t *testing.T) {
	u := SystemdUnit("/usr/local/bin/hs-node", "/home/u/.local/share/dialhome-hs/logs")
	for _, want := range []string{"ExecStart=/usr/local/bin/hs-node run", "Restart=on-failure", "RestartSec=5", "WantedBy=default.target"} {
		if !strings.Contains(u, want) {
			t.Fatalf("unit missing %q:\n%s", want, u)
		}
	}
	if strings.Contains(u, "WantedBy=multi-user.target") {
		t.Fatal("system target (root scope) leaked into the user unit")
	}
}

// N-as-4: XDG fallback is a desktop autostart entry, not a service.
func TestXDGDesktopEntry(t *testing.T) {
	e := XDGDesktopEntry("/usr/bin/hs-node")
	if !strings.Contains(e, "Exec=/usr/bin/hs-node run") {
		t.Fatalf("entry missing exec line:\n%s", e)
	}
	if strings.Contains(e, "user") && strings.Contains(e, "root") {
		t.Fatal("entry references root")
	}
}

// N-as-5: the darwin plist keeps RunAtLoad and logs to the user log dir
// (platform stays UNVERIFIED; the builder is still pinned by tests).
func TestDarwinPlist(t *testing.T) {
	p := DarwinPlist("/Applications/hs-node", "/Users/u/logs")
	for _, want := range []string{"<string>/Applications/hs-node</string>", "<string>run</string>", "RunAtLoad", "/Users/u/logs/hs-node.log"} {
		if !strings.Contains(p, want) {
			t.Fatalf("plist missing %q:\n%s", want, p)
		}
	}
}
