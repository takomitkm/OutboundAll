// layout.go defines the user-level directory layout for hs-node and creates
// it with hardened ACLs (no admin, no system locations).
//
//	Windows: %LOCALAPPDATA%\OutboundAll\{bin,config,state,logs,tmp}
//	Linux:   $XDG_DATA_HOME/OutboundAll-hs/{...}   (default ~/.local/share)
//	darwin:  ~/Library/Application Support/OutboundAll-hs/{...}
package node

import (
	"os"
	"path/filepath"
	"runtime"

	"github.com/takomitkm/outboundall/internal/platform"
)

// Layout holds the resolved absolute paths for one node installation.
type Layout struct {
	Root     string
	BinDir   string
	CfgDir   string
	StateDir string
	LogsDir  string
	TmpDir   string
}

// DefaultLayout resolves the per-platform user-level root.
func DefaultLayout() (Layout, error) {
	var root string
	var err error
	switch runtime.GOOS {
	case "windows":
		root, err = localAppData()
		root = filepath.Join(root, "OutboundAll")
	case "darwin":
		home, e := os.UserHomeDir()
		if e != nil {
			return Layout{}, e
		}
		root = filepath.Join(home, "Library", "Application Support", "OutboundAll-hs")
	default:
		root, err = os.UserConfigDir() // XDG_DATA_HOME is data; config dir fits our writable state
		if err == nil {
			root = filepath.Join(root, "OutboundAll-hs")
		}
	}
	if err != nil {
		return Layout{}, err
	}
	return Layout{
		Root:     root,
		BinDir:   filepath.Join(root, "bin"),
		CfgDir:   filepath.Join(root, "config"),
		StateDir: filepath.Join(root, "state"),
		LogsDir:  filepath.Join(root, "logs"),
		TmpDir:   filepath.Join(root, "tmp"),
	}, nil
}

// Create materializes the layout and hardens the state directory (identity
// lives there). No elevation, no system locations.
func (l Layout) Create() error {
	for _, d := range []string{l.Root, l.BinDir, l.CfgDir, l.StateDir, l.LogsDir, l.TmpDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}
	// The state dir carries the tailscale identity: only the current user may
	// enter it. A DIRECTORY needs 0700 on unix -- HardenFile's 0600 would
	// strip the execute bit and make it untraversable (CI LIVE-RED: the
	// instance lock could not be created). Windows keeps HardenFile (icacls).
	if runtime.GOOS == "windows" {
		return platform.HardenFile(l.StateDir)
	}
	return os.Chmod(l.StateDir, 0o700)
}

func localAppData() (string, error) {
	if v := os.Getenv("LOCALAPPDATA"); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "AppData", "Local"), nil
}
