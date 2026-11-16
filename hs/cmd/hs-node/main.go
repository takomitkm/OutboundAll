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
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/takomitkm/dialhome/hs/internal/node"
	"github.com/takomitkm/dialhome/internal/errs"
	"github.com/takomitkm/dialhome/internal/lockfile"
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
		return cmdEnroll(args[1:])
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
  hs-node enroll --code CODE --controller URL [--hostname NAME]
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
		if errors.Is(err, lockfile.ErrLocked) {
			fmt.Fprintln(os.Stderr, "another hs-node supervisor owns this node (instance lock)")
			return errs.ExitGeneric
		}
		fatal("instance lock: %v", err)
	}
	defer lock.Release()

	logOut, logClose, err := logging.New(logging.Options{Dir: l.LogsDir, Name: "hs-node", AlsoStderr: true})
	if err != nil {
		fatal("open log: %v", err)
	}
	defer logClose.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// boot-wait: resume with the controller for up to 10 minutes (30s probes)
	// before entering the retry ladder (reference NODE_MANUAL §8 semantics).
	var sbCfgPath string
	deadline := time.Now().Add(10 * time.Minute)
	for {
		sbCfgPath, err = node.PrepareRunConfig()
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			fatal("resume did not succeed within boot-wait window: %v", err)
		}
		logOut.Info("resume pending; retrying", "err", err.Error())
		select {
		case <-ctx.Done():
			return 0
		case <-time.After(30 * time.Second):
		}
	}

	bin, err := node.Ensure(l.BinDir, "")
	if err != nil {
		fatal("provision sing-box: %v", err)
	}

	mgr := node.NewManager(bin, sbCfgPath, logOut)
	mgr.AuthFailureHook = func() bool {
		// the resume key was rejected / never consumed: back off HARD
		return strings.Contains(mgr.LogTail(), "Waiting for authentication")
	}
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

func cmdEnroll(args []string) int {
	fs := flag.NewFlagSet("enroll", flag.ExitOnError)
	code := fs.String("code", "", "one-time invitation code")
	controllerURL := fs.String("controller", "", "hs-controller base URL (https)")
	hostname := fs.String("hostname", defaultHostname(), "node hostname [a-z0-9-]")
	if err := fs.Parse(args); err != nil {
		return errs.ExitUsage
	}
	if *code == "" || *controllerURL == "" || *hostname == "" {
		usage()
		return errs.ExitUsage
	}
	msg, err := node.Enroll(*controllerURL, *code, *hostname)
	if err != nil {
		fatal("enroll: %v", err)
	}
	fmt.Println(msg)
	fmt.Println("next: hs-node run   (supervisor starts; autostart install registers login startup)")
	return 0
}

func defaultHostname() string {
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return strings.ToLower(regexp.MustCompile(`[^a-z0-9-]+`).ReplaceAllString(h, "-"))
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
