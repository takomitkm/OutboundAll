// Command outboundall is the single binary for all three roles (node / controller / relay).
//
// Usage:
//
//	outboundall node install|run|status|stop|uninstall|enroll|migrate
//	outboundall controller list|status|shell|exec
//	outboundall relay init|enroll|list|inspect|revoke|reconcile|ports|verify|credentials
//	outboundall relay-helper --port <p>          (forced-command entry, one process per connection)
//	outboundall autostart install|uninstall|status
//	outboundall service install|uninstall|status --scope system
//	outboundall doctor
package main

import (
	"fmt"
	"os"

	"github.com/takomitkm/outboundall/internal/cli"
	"github.com/takomitkm/outboundall/internal/errs"
)

// version is injected at release time with
//
//	-ldflags "-X main.version=<tag> -X main.commit=<sha>"
var version = "dev"
var commit = "none"

func main() {
	cli.SetVersion(version, commit)
	if err := cli.Run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "outboundall:", err)
		os.Exit(errs.ExitCode(err))
	}
}
