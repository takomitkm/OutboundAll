//go:build !windows

package node

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// InstallAutostart writes the platform entry; UNVERIFIED platforms return an
// explicit error rather than pretending.
func InstallAutostart(hsNodeBin, logDir string) (string, error) {
	switch runtime.GOOS {
	case "linux":
		return installLinuxUser(hsNodeBin, logDir)
	case "darwin":
		return installDarwinAgent(hsNodeBin, logDir)
	default:
		return "", fmt.Errorf("autostart unsupported on %s", runtime.GOOS)
	}
}

// RemoveAutostart deletes the platform entry.
func RemoveAutostart() (string, error) {
	switch runtime.GOOS {
	case "linux":
		return removeLinuxUser()
	case "darwin":
		return removeDarwinAgent()
	default:
		return "", fmt.Errorf("autostart unsupported on %s", runtime.GOOS)
	}
}

// installLinuxUser prefers systemd --user; falls back to XDG autostart when
// no user bus is reachable (headless ssh sessions, reference NODE_MANUAL §6).
func installLinuxUser(hsNodeBin, logDir string) (string, error) {
	if userBusAvailable() {
		unitDir := userHome(".config/systemd/user")
		if err := os.MkdirAll(unitDir, 0o755); err != nil {
			return "", err
		}
		unit := filepath.Join(unitDir, AutostartKey+".service")
		if err := os.WriteFile(unit, []byte(SystemdUnit(hsNodeBin, logDir)), 0o644); err != nil {
			return "", err
		}
		out, err := runOut("systemctl", "--user", "daemon-reload")
		if err != nil {
			return "", fmt.Errorf("systemctl daemon-reload: %v: %s", err, out)
		}
		out, err = runOut("systemctl", "--user", "enable", "--now", AutostartKey+".service")
		if err != nil {
			return "", fmt.Errorf("systemctl enable: %v: %s", err, out)
		}
		return unit + " (systemd --user, enabled)", nil
	}
	xdgDir := userHome(".config/autostart")
	if err := os.MkdirAll(xdgDir, 0o755); err != nil {
		return "", err
	}
	entry := filepath.Join(xdgDir, AutostartKey+".desktop")
	if err := os.WriteFile(entry, []byte(XDGDesktopEntry(hsNodeBin)), 0o644); err != nil {
		return "", err
	}
	return entry + " (XDG autostart fallback)", nil
}

func removeLinuxUser() (string, error) {
	if userBusAvailable() {
		out, err := runOut("systemctl", "--user", "disable", "--now", AutostartKey+".service")
		if err != nil {
			return "", fmt.Errorf("systemctl disable: %v: %s", err, out)
		}
		unit := userHome(".config/systemd/user/" + AutostartKey + ".service")
		if err := os.Remove(unit); err != nil && !os.IsNotExist(err) {
			return "", err
		}
		return "removed " + unit, nil
	}
	entry := userHome(".config/autostart/" + AutostartKey + ".desktop")
	if err := os.Remove(entry); err != nil && !os.IsNotExist(err) {
		return "", err
	}
	return "removed " + entry, nil
}

func userBusAvailable() bool {
	_, err := runOut("systemctl", "--user", "is-system-running")
	return err == nil
}

func userHome(rel string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return rel
	}
	return filepath.Join(home, rel)
}

func runOut(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	b, err := cmd.CombinedOutput()
	return string(b), err
}

// installDarwinAgent is UNVERIFIED (no macOS machine in this workstream).
func installDarwinAgent(hsNodeBin, logDir string) (string, error) {
	if runtime.GOOS != "darwin" {
		return "", fmt.Errorf("darwin autostart only on darwin")
	}
	agents := userHome("Library/LaunchAgents")
	if err := os.MkdirAll(agents, 0o755); err != nil {
		return "", err
	}
	plist := filepath.Join(agents, "net.OutboundAll.hs-node.plist")
	if err := os.WriteFile(plist, []byte(DarwinPlist(hsNodeBin, logDir)), 0o644); err != nil {
		return "", err
	}
	return plist + " (LaunchAgent, UNVERIFIED)", nil
}

func removeDarwinAgent() (string, error) {
	plist := userHome("Library/LaunchAgents/net.OutboundAll.hs-node.plist")
	if err := os.Remove(plist); err != nil && !os.IsNotExist(err) {
		return "", err
	}
	return "removed " + plist, nil
}
