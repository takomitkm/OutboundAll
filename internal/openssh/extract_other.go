//go:build !windows

package openssh

import "fmt"

// extractZip is windows-only (unix uses system ssh/sshd; no package to unpack).
func extractZip(src, dst string) error {
	return fmt.Errorf("openssh: zip extraction is a windows-only path (called on %s)", runtimeGOOS())
}

func runtimeGOOS() string { return "non-windows" }
