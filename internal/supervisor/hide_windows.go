//go:build windows

package supervisor

import (
	"os/exec"
	"syscall"
)

// hideWindow sets CREATE_NO_WINDOW on background children so no console flashes
// (spec §7.1). Note: stdout/stderr must still be wired by the caller -- a child
// with no redirected pipes would silently write into the invisible console.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000} // CREATE_NO_WINDOW
}

// dialTimeout is duplicated from runner for the windows build to keep imports tight.
