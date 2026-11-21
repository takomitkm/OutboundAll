// Command OutboundAll is the single binary for all three roles (node / controller / relay).
//
// Usage:
//
//	OutboundAll node install|run|status|stop|uninstall|enroll|migrate
//	OutboundAll controller list|status|shell|exec
//	OutboundAll relay init|enroll|list|inspect|revoke|reconcile|ports|verify|credentials
//	OutboundAll relay-helper --port <p>          (forced-command entry, one process per connection)
//	OutboundAll autostart install|uninstall|status
//	OutboundAll service install|uninstall|status --scope system
//	OutboundAll doctor
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
		fmt.Fprintln(os.Stderr, "OutboundAll:", err)
		os.Exit(errs.ExitCode(err))
	}
}
