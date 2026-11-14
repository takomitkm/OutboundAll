//go:build !windows

package cli

import "os/exec"

func hideWindow(_ *exec.Cmd) {}
