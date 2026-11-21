package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// autostart.go -- user-level autostart (spec §6.1: never admin, never a wrapper).
// Windows: HKCU Run entry pointing at OutboundAll.exe itself (single mechanism, no lnk
// duplication). Linux: systemd --user unit (XDG autostart fallback later).
// macOS: ~/Library/LaunchAgents plist.

const runKeyName = "OutboundAll-node"

func selfExe() (string, error) {
	return os.Executable()
}

func autostartEnsure(np nodePaths, nodeName string) error {
	exe, err := selfExe()
	if err != nil {
		return err
	}
	switch runtime.GOOS {
	case "windows":
		return winRunKeySet(runKeyName, fmt.Sprintf(`"%s" node run`, exe))
	case "darwin":
		return darwinLaunchAgent(exe, np)
	default:
		return linuxUserUnit(exe, np, nodeName)
	}
}

func autostartRemove() {
	switch runtime.GOOS {
	case "windows":
		_ = winRunKeyDelete(runKeyName)
	case "darwin":
		_ = os.Remove(filepath.Join(launchAgentsDir(), "OutboundAll-node.plist"))
	default:
		_ = os.Remove(userUnitPath())
	}
}

// --- windows -------------------------------------------------------------

func winRunKeySet(name, value string) error {
	return execSilent("reg", "add", `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`,
		"/v", name, "/t", "REG_SZ", "/d", value, "/f")
}

func winRunKeyDelete(name string) error {
	return execSilent("reg", "delete", `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`,
		"/v", name, "/f")
}

// winRunKeyGet reads the current HKCU Run value for autostart status. A missing
// value is NOT an error: it means "absent".
func winRunKeyGet(name string) (bool, string, error) {
	cmd := exec.Command("reg", "query", `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`, "/v", name)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false, "", nil
	}
	s := string(out)
	i := strings.LastIndex(s, "REG_SZ")
	if i < 0 {
		return false, "", nil
	}
	return true, strings.TrimSpace(s[i+len("REG_SZ"):]), nil
}

// --- optional system-scope service (spec §6.2) ---------------------------
// Never installed by default. On Linux it writes a system unit (needs sudo);
// on Windows/macOS we refuse with a clear message rather than silently trying
// to elevate (the whole design is admin-less by default).

const systemUnitPath = "/etc/systemd/system/OutboundAll-node.service"

func systemServiceInstall() error {
	exe, err := selfExe()
	if err != nil {
		return err
	}
	switch runtime.GOOS {
	case "linux":
		unit := `[Unit]
Description=OutboundAll node supervisor (system scope, optional)
After=network-online.target

[Service]
Type=simple
User=` + currentUser() + `
ExecStart=` + exe + ` node run
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
`
		// writing to /etc requires elevation; report that explicitly
		if err := os.WriteFile(systemUnitPath, []byte(unit), 0o644); err != nil {
			return fmt.Errorf("system service install needs root: %w", err)
		}
		if err := execSilent("systemctl", "daemon-reload"); err != nil {
			return err
		}
		if err := execSilent("systemctl", "enable", "OutboundAll-node.service"); err != nil {
			return err
		}
		fmt.Println("system service enabled (OPTIONAL; user-level autostart is the default)")
		return nil
	default:
		return fmt.Errorf("system-scope service is a Linux systemd option only; on %s use 'OutboundAll autostart' (user scope, no admin)", runtime.GOOS)
	}
}

func systemServiceUninstall() error {
	switch runtime.GOOS {
	case "linux":
		_ = execSilent("systemctl", "disable", "OutboundAll-node.service")
		if err := os.Remove(systemUnitPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("system service uninstall needs root: %w", err)
		}
		_ = execSilent("systemctl", "daemon-reload")
		fmt.Println("system service removed")
		return nil
	default:
		return fmt.Errorf("no system service on %s (nothing to remove)", runtime.GOOS)
	}
}

func systemServiceStatus() error {
	if runtime.GOOS != "linux" {
		fmt.Println("system service = not applicable on " + runtime.GOOS + " (user autostart is the default)")
		return nil
	}
	if _, err := os.Stat(systemUnitPath); err != nil {
		fmt.Println("system service = absent (optional; not installed)")
		return nil
	}
	fmt.Println("system service = installed at " + systemUnitPath)
	return nil
}

// --- darwin --------------------------------------------------------------

func launchAgentsDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents")
}

func darwinLaunchAgent(exe string, np nodePaths) error {
	if err := os.MkdirAll(launchAgentsDir(), 0o755); err != nil {
		return err
	}
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>OutboundAll-node</string>
  <key>ProgramArguments</key><array>
    <string>` + exe + `</string><string>node</string><string>run</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
</dict></plist>`
	return os.WriteFile(filepath.Join(launchAgentsDir(), "OutboundAll-node.plist"), []byte(plist), 0o644)
}

// --- linux (systemd --user) ----------------------------------------------

func userUnitPath() string {
	home, _ := os.UserHomeDir()
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "systemd", "user", "OutboundAll-node.service")
}

func linuxUserUnit(exe string, np nodePaths, nodeName string) error {
	if err := os.MkdirAll(filepath.Dir(userUnitPath()), 0o755); err != nil {
		return err
	}
	unit := `[Unit]
Description=OutboundAll node supervisor (` + nodeName + `)
After=network-online.target

[Service]
ExecStart=` + exe + ` node run
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
`
	if err := os.WriteFile(userUnitPath(), []byte(unit), 0o644); err != nil {
		return err
	}
	// best-effort enable; report but do not fail install when systemctl is absent
	if err := execSilent("systemctl", "--user", "daemon-reload"); err != nil {
		// no user bus (ssh session without a desktop): fall back to XDG autostart,
		// which the desktop session launches at login (spec §6.1 alternative)
		return writeXDGAutostart(exe)
	}
	if err := execSilent("systemctl", "--user", "enable", "OutboundAll-node.service"); err != nil {
		return writeXDGAutostart(exe)
	}
	return nil
}

// writeXDGAutostart installs the XDG autostart entry (desktop-session fallback).
func writeXDGAutostart(exe string) error {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".config", "autostart")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	desktop := `[Desktop Entry]
Type=Application
Name=OutboundAll node
Exec=` + exe + ` node run
Hidden=false
X-GNOME-Autostart-enabled=true
`
	if err := os.WriteFile(filepath.Join(dir, "OutboundAll-node.desktop"), []byte(desktop), 0o644); err != nil {
		return err
	}
	fmt.Println("AUTOSTART = XDG autostart (no systemd user bus; desktop session will launch it)")
	return nil
}

// --- shared --------------------------------------------------------------

func execSilent(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
