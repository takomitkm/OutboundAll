// Package platform centralises per-OS paths and file hardening.
// Windows uses icacls (strip inheritance, grant only the current user) because
// Go's os.Chmod cannot express NTFS ACLs (it only toggles the read-only bit).
package platform

import (
	"os"
	"path/filepath"
	"runtime"
)

// DataDir returns the per-role data root (docs/ARCHITECTURE.md §2).
// role: "node" | "controller" | "relay"
// OUTBOUNDALL_DATA_DIR overrides every role (tests and custom deployments).
func DataDir(role string) string {
	if custom := os.Getenv("OUTBOUNDALL_DATA_DIR"); custom != "" {
		return custom
	}
	switch runtime.GOOS {
	case "windows":
		home, herr := os.UserHomeDir()
		if herr != nil {
			home = os.Getenv("USERPROFILE")
		}
		if role == "relay" {
			return filepath.Join(home, ".OutboundAll")
		}
		base := os.Getenv("LOCALAPPDATA")
		if base == "" {
			base = filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Local")
		}
		if role == "controller" {
			base = os.Getenv("APPDATA")
			if base == "" {
				base = filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Roaming")
			}
			return filepath.Join(base, "OutboundAll")
		}
		return filepath.Join(base, "OutboundAll")
	case "darwin":
		home, _ := os.UserHomeDir()
		if role == "relay" {
			return filepath.Join(home, ".OutboundAll")
		}
		if role == "node" {
			return filepath.Join(home, "Library", "Application Support", "OutboundAll")
		}
		return filepath.Join(home, ".config", "OutboundAll")
	default: // linux and everything else unix-like: XDG
		if role == "relay" {
			home, _ := os.UserHomeDir()
			return filepath.Join(home, ".OutboundAll")
		}
		if role == "node" {
			base := os.Getenv("XDG_DATA_HOME")
			if base == "" {
				home, _ := os.UserHomeDir()
				base = filepath.Join(home, ".local", "share")
			}
			return filepath.Join(base, "OutboundAll")
		}
		base := os.Getenv("XDG_CONFIG_HOME")
		if base == "" {
			home, _ := os.UserHomeDir()
			base = filepath.Join(home, ".config")
		}
		return filepath.Join(base, "OutboundAll")
	}
}
