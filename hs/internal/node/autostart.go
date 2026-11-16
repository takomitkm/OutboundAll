// autostart.go registers hs-node as a user-level startup entry.
//
//	Windows: HKCU\Software\Microsoft\Windows\CurrentVersion\Run  ("hs-node")
//	Linux:   systemd --user unit; XDG autostart fallback when no user bus
//	darwin:  LaunchAgent (UNVERIFIED -- no macOS machine in this workstream)
//
// All mechanisms are per-user, never elevated (RUNBOOK 无提权实测矩阵).
package node

import "fmt"

// AutostartKey is the registry value / unit name across platforms.
const AutostartKey = "hs-node"

// RunCommand renders the command line the autostart entry points at.
// Pure function so tests can assert it byte-for-byte on every platform.
func RunCommand(hsNodeBin string) string {
	return fmt.Sprintf("%q run", hsNodeBin)
}

// WindowsRunValue renders the HKCU Run value data (plain command string).
func WindowsRunValue(hsNodeBin string) string {
	return RunCommand(hsNodeBin)
}

// WindowsRunKey is the HKCU key path (user-level; no elevation needed).
const WindowsRunKey = `Software\Microsoft\Windows\CurrentVersion\Run`

// SystemdUnit renders the user-level systemd unit body.
func SystemdUnit(hsNodeBin string, logDir string) string {
	return fmt.Sprintf(`[Unit]
Description=dialhome hs-node (tailscale userspace endpoint)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%s run
Restart=on-failure
RestartSec=5
WorkingDirectory=%s

[Install]
WantedBy=default.target
`, hsNodeBin, logDir)
}

// XDGDesktopEntry renders the fallback autostart entry (no user bus).
func XDGDesktopEntry(hsNodeBin string) string {
	return fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=hs-node
Exec=%s run
Terminal=false
X-GNOME-Autostart-enabled=true
`, hsNodeBin)
}

// DarwinPlist renders the LaunchAgent body (UNVERIFIED platform).
func DarwinPlist(hsNodeBin string, logDir string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>net.dialhome.hs-node</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>run</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>StandardOutPath</key><string>%s/hs-node.log</string>
  <key>StandardErrorPath</key><string>%s/hs-node.log</string>
</dict>
</plist>
`, hsNodeBin, logDir, logDir)
}


