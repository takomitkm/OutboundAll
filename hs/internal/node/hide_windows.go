//go:build windows

package node

import (
	"os/exec"
	"syscall"
)

// hideWindow applies CREATE_NO_WINDOW so a spawned sing-box never pops a
// console (reference: internal/cli/hide_windows.go, WCIS 无窗 SOP; the flag is
// only referenced on windows builds).
func hideWindow(cmd *exec.Cmd) {
	// stdlib syscall has no CREATE_NO_WINDOW constant; the literal matches
	// internal/cli/hide_windows.go (0x08000000). cmd.Stdout/Stderr are set by
	// the caller (procman) -- without them the child would write into the
	// hidden console and the caller would see nothing (2026-11-14 lesson).
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000}
}
