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
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NO_WINDOW,
		// Keep parent stdio plumbed through: without explicit Stdout/Stderr
		// the child writes into the hidden console and the caller sees
		// nothing (2026-11-14 lesson, AGENTS.md 提交前自查 #1).
	}
}
