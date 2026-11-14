//go:build windows

package cli

import (
	"os/exec"
	"syscall"
)

// hideWindow sets CREATE_NO_WINDOW on background children (spec §7.1).
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000} // CREATE_NO_WINDOW
}
