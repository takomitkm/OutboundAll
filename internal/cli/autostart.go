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
// Windows: HKCU Run entry pointing at dialhome.exe itself (single mechanism, no lnk
// duplication). Linux: systemd --user unit (XDG autostart fallback later).
// macOS: ~/Library/LaunchAgents plist.

const runKeyName = "dialhome-node"

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
		_ = os.Remove(filepath.Join(launchAgentsDir(), "dialhome-node.plist"))
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
  <key>Label</key><string>dialhome-node</string>
  <key>ProgramArguments</key><array>
    <string>` + exe + `</string><string>node</string><string>run</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
</dict></plist>`
	return os.WriteFile(filepath.Join(launchAgentsDir(), "dialhome-node.plist"), []byte(plist), 0o644)
}

// --- linux (systemd --user) ----------------------------------------------

func userUnitPath() string {
	home, _ := os.UserHomeDir()
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "systemd", "user", "dialhome-node.service")
}

func linuxUserUnit(exe string, np nodePaths, nodeName string) error {
	if err := os.MkdirAll(filepath.Dir(userUnitPath()), 0o755); err != nil {
		return err
	}
	unit := `[Unit]
Description=dialhome node supervisor (` + nodeName + `)
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
	if err := execSilent("systemctl", "--user", "enable", "dialhome-node.service"); err != nil {
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
Name=dialhome node
Exec=` + exe + ` node run
Hidden=false
X-GNOME-Autostart-enabled=true
`
	if err := os.WriteFile(filepath.Join(dir, "dialhome-node.desktop"), []byte(desktop), 0o644); err != nil {
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
