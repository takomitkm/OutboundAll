//go:build windows

package openssh

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// extractZip unpacks src into dst using the stdlib archive/zip -- no PowerShell.
// Evidence (2026-11-15): spawning powershell Expand-Archive from a console-less
// parent hits Write-Progress -> "Win32 internal error Access is denied" while
// reading the console buffer, and the cmdlet exits 0 WITHOUT extracting. The
// stdlib reader is deterministic and silent, and works on every Windows host.
func extractZip(src, dst string) error {
	zr, err := zip.OpenReader(src)
	if err != nil {
		return fmt.Errorf("zip open: %w", err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		// zip-slip guard: reject paths escaping dst
		clean := filepath.Clean(f.Name)
		if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
			return fmt.Errorf("zip-slip entry rejected: %s", f.Name)
		}
		target := filepath.Join(dst, clean)
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o700); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fileModeOf(f.Mode()))
		if err != nil {
			rc.Close()
			return err
		}
		if _, err := io.Copy(out, rc); err != nil {
			out.Close()
			rc.Close()
			return err
		}
		out.Close()
		rc.Close()
	}
	return nil
}

// fileModeOf strips setuid/setgid bits that confuse Windows ACL inheritance.
func fileModeOf(m os.FileMode) os.FileMode {
	return m & 0o777
}
