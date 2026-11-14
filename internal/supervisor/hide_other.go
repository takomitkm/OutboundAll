//go:build !windows

package supervisor

import "os/exec"

// hideWindow is a no-op on unix: background children are detached by the
// service/autostart mechanism, not by console flags.
func hideWindow(_ *exec.Cmd) {}
