package openssh

import (
	"fmt"
	"os"
	"path/filepath"
)

// EnsureWindowsPackage makes sure the pinned zip exists at dir and, when not yet
// extracted, verifies its hash and unzips it (PowerShell Expand-Archive only exists
// on Windows; on unix this path is never taken).
func EnsureWindowsPackage(dir, zipPath string, cred any) error {
	exe := filepath.Join(dir, "OpenSSH-Win64", "sshd.exe")
	if _, err := os.Stat(exe); err == nil {
		return nil // already installed
	}
	cur := Current()
	needFetch := false
	if _, err := os.Stat(zipPath); err != nil {
		needFetch = true
	} else if err := VerifyFile(zipPath, cur.SHA256); err != nil {
		// wrong/corrupt zip: replace it
		os.Remove(zipPath)
		needFetch = true
	}
	if needFetch {
		if err := Fetch(zipPath, ""); err != nil {
			return err
		}
	}
	if err := VerifyFile(zipPath, cur.SHA256); err != nil {
		return err
	}
	// extract
	tmpDir, err := os.MkdirTemp(dir, "extract-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)
	// extract with the stdlib archive/zip. PowerShell Expand-Archive is unusable
	// from a console-less parent: its Write-Progress dies with
	// "Win32 internal error Access is denied" reading the console buffer and the
	// cmdlet exits 0 WITHOUT extracting (evidence: docs/PHASE4_ACCEPTANCE.md notes).
	if err := extractZip(zipPath, tmpDir); err != nil {
		return err
	}
	// move OpenSSH-Win64 into place (remove a stale/partial target first --
	// os.Rename fails silently against an existing directory on Windows)
	src := filepath.Join(tmpDir, "OpenSSH-Win64")
	if _, err := os.Stat(src); err != nil {
		return fmt.Errorf("extract produced no OpenSSH-Win64 dir")
	}
	dst := filepath.Join(dir, "OpenSSH-Win64")
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	return os.Rename(src, dst)
}
