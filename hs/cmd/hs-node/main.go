// Command hs-node is the Windows-node-side wrapper: it owns the user-level
// layout, config.json, sing-box acquisition and supervision, autostart and
// diagnostics. Enrollment (invitation redeem) lands in Phase 3.
//
// Usage:
//
//	hs-node run                          # supervise sing-box (foreground)
//	hs-node status                       # read-only supervisor/process status
//	hs-node stop                         # stop a running supervisor
//	hs-node install-singbox [--archive F]  # fetch/verify pinned sing-box
//	hs-node autostart install|uninstall|status
//	hs-node doctor                       # read-only diagnostics
//	hs-node version
//	hs-node enroll --code CODE --server URL   # Phase 3
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"

	"github.com/takomitkm/dialhome/hs/internal/node"
	"github.com/takomitkm/dialhome/internal/errs"
	"github.com/takomitkm/dialhome/internal/logging"
)

var version = "dev"

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		usage()
		return errs.ExitUsage
	}
	switch args[0] {
	case "run":
		return cmdRun(args[1:])
	case "status":
		return cmdStatus(args[1:])
	case "stop":
		return cmdStop(args[1:])
	case "install-singbox":
		return cmdInstallSingbox(args[1:])
	case "autostart":
		return cmdAutostart(args[1:])
	case "doctor":
		return cmdDoctor(args[1:])
	case "enroll":
		fmt.Fprintln(os.Stderr, "enroll: not implemented until phase 3 (invitation service)")
		return errs.ExitNotImplemented
	case "version":
		fmt.Printf("hs-node %s (sing-box pin %s)\n", version, node.VersionPin)
		return 0
	default:
		usage()
		return errs.ExitUsage
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `usage:
  hs-node run
  hs-node status | stop | doctor | version
  hs-node install-singbox [--archive FILE]
  hs-node autostart install | uninstall | status
  hs-node enroll --code CODE --server URL   (phase 3)
`)
}

func mustLayout() node.Layout {
	l, err := node.DefaultLayout()
	if err != nil {
		fatal("resolve layout: %v", err)
	}
	return l
}

func cmdRun([]string) int {
	l := mustLayout()
	if err := l.Create(); err != nil {
		fatal("create layout: %v", err)
	}
	lock, err := node.AcquireInstanceLock(l.StateDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "another hs-node supervisor owns this node (instance lock)")
		return errs.ExitGeneric
	}
	defer lock.Release()

	cfg, err := node.Load(l.CfgDir)
	if err != nil {
		fatal("load config: %v", err)
	}
	sbCfg, err := cfg.SingBoxConfig()
	if err != nil {
		fatal("render sing-box config: %v", err)
	}
	sbCfgPath := filepath.Join(l.CfgDir, "sing-box.json")
	if err := os.WriteFile(sbCfgPath, []byte(sbCfg), 0o600); err != nil {
		fatal("write sing-box config: %v", err)
	}

	logOut, logClose, err := logging.New(logging.Options{Dir: l.LogsDir, Name: "hs-node", AlsoStderr: true})
	if err != nil {
		fatal("open log: %v", err)
	}
	defer logClose.Close()

	bin, err := node.Ensure(l.BinDir, "")
	if err != nil {
		fatal("provision sing-box: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	mgr := node.NewManager(bin, sbCfgPath, logOut)
	logOut.Info("hs-node supervisor starting", "bin", bin, "config", sbCfgPath)
	err = mgr.Run(ctx)
	logOut.Info("hs-node supervisor exiting", "err", fmt.Sprint(err))
	return 0
}

func cmdStatus([]string) int {
	l := mustLayout()
	cfg, err := node.Load(l.CfgDir)
	if err != nil {
		fmt.Println("config: invalid:", err)
		return errs.ExitGeneric
	}
	b, _ := json.MarshalIndent(cfg, "", "  ")
	fmt.Println("config: ok")
	fmt.Println(string(b))
	return 0
}

func cmdStop([]string) int {
	fmt.Println("stop: the supervisor exits on SIGTERM; send one to the hs-node process (or `hs-node run` window)")
	return 0
}

func cmdInstallSingbox(args []string) int {
	archive := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "--archive" && i+1 < len(args) {
			archive = args[i+1]
		}
	}
	l := mustLayout()
	bin, err := node.Ensure(l.BinDir, archive)
	if err != nil {
		fatal("install sing-box: %v", err)
	}
	fmt.Println("SINGBOX-OK", bin, "pin", node.VersionPin)
	return 0
}

func cmdAutostart(args []string) int {
	if len(args) == 0 {
		usage()
		return errs.ExitUsage
	}
	l := mustLayout()
	switch args[0] {
	case "install":
		bin := filepath.Join(l.BinDir, hsNodeExe())
		msg, err := node.InstallAutostart(bin, l.LogsDir)
		if err != nil {
			fatal("autostart install: %v", err)
		}
		fmt.Println("AUTOSTART", msg)
		return 0
	case "uninstall":
		msg, err := node.RemoveAutostart()
		if err != nil {
			fatal("autostart uninstall: %v", err)
		}
		fmt.Println("AUTOSTART", msg)
		return 0
	case "status":
		fmt.Println("autostart status: inspect HKCU Run / systemd --user / LaunchAgent manually (doctor also prints it)")
		return 0
	default:
		usage()
		return errs.ExitUsage
	}
}

func cmdDoctor([]string) int {
	l := mustLayout()
	fmt.Println("platform   :", osRuntime())
	fmt.Println("root       :", l.Root)
	if _, err := os.Stat(l.StateDir); err == nil {
		entries, _ := os.ReadDir(l.StateDir)
		fmt.Println("state dir  :", l.StateDir, fmt.Sprintf("(%d entries)", len(entries)))
	} else {
		fmt.Println("state dir  : missing (not enrolled)")
	}
	if _, err := node.Load(l.CfgDir); err != nil {
		fmt.Println("config     : invalid/missing:", err)
	} else {
		fmt.Println("config     : ok")
	}
	fmt.Println("singbox pin:", node.VersionPin)
	return 0
}

func hsNodeExe() string {
	if os.PathSeparator == '\\' {
		return "hs-node.exe"
	}
	return "hs-node"
}

func osRuntime() string {
	return runtime.GOOS + "/" + runtime.GOARCH
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "hs-node: "+format+"\n", args...)
	os.Exit(1)
}
