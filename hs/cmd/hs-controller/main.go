// Command hs-controller is the management-plane service on the Linux VPS:
// invitation service, node registry, resume-key channel, admin API.
//
// Usage:
//
//	hs-controller init                          # create store + admin token
//	hs-controller serve [--listen ADDR]         # run the API
//	hs-controller invite create [--label L] [--profile P] [--ttl-hours H]
//	hs-controller invite revoke <id>
//	hs-controller invite list
//	hs-controller node list | revoke <id>
//	hs-controller audit [N]
//	hs-controller version
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/takomitkm/dialhome/hs/internal/controller"
	"github.com/takomitkm/dialhome/internal/errs"
)

var version = "dev"

// dataDir is fixed like the reference relay's ~/.dialhome (H6: env-dependent
// data dirs split the registry).
func dataDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		fatal("resolve home: %v", err)
	}
	return home + string(os.PathSeparator) + ".dialhome-hs"
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		usage()
		return errs.ExitUsage
	}
	switch args[0] {
	case "init":
		return cmdInit(args[1:])
	case "serve":
		return cmdServe(args[1:])
	case "invite":
		return cmdInvite(args[1:])
	case "node":
		return cmdNode(args[1:])
	case "audit":
		return cmdAudit(args[1:])
	case "version":
		fmt.Printf("hs-controller %s\n", version)
		return 0
	default:
		usage()
		return errs.ExitUsage
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `usage:
  hs-controller init
  hs-controller serve [--listen 127.0.0.1:8800]
  hs-controller invite create [--label L] [--profile P] [--ttl-hours H]
  hs-controller invite revoke <id> | list
  hs-controller node list | revoke <id>
  hs-controller audit [N]
  hs-controller version
`)
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "hs-controller: "+format+"\n", args...)
	os.Exit(1)
}

func openStore() *controller.Store {
	s, err := controller.Open(dataDir())
	if err != nil {
		fatal("open store: %v", err)
	}
	return s
}

func cmdInit([]string) int {
	dir := dataDir()
	s := openStore()
	adminToken, err := controller.RandomToken()
	if err != nil {
		fatal("generate admin token: %v", err)
	}
	sum := sha256.Sum256([]byte(adminToken))
	if err := s.SetAdminToken(hex.EncodeToString(sum[:])); err != nil {
		fatal("store admin token: %v", err)
	}
	fmt.Printf("hs-controller init ok: %s\n", dir)
	fmt.Println("ADMIN-TOKEN (shown once, store keeps only its sha256):")
	fmt.Println(adminToken)
	return 0
}

func cmdServe(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	listen := fs.String("listen", "127.0.0.1:8800", "API listen address")
	hsBin := fs.String("headscale-bin", "/usr/bin/headscale", "headscale binary path")
	hsCfg := fs.String("headscale-config", "/etc/headscale/config.yaml", "headscale config path")
	hsUser := fs.String("headscale-user", "ci", "headscale user name")
	serverURL := fs.String("server", "", "control plane URL handed to nodes (default: derive from headscale config)")
	if err := fs.Parse(args); err != nil {
		return errs.ExitUsage
	}
	_ = serverURL

	s := openStore()
	// resolve headscale user id once
	hs := &controller.Headscale{BinPath: *hsBin, ConfigPath: *hsCfg}
	if err := hs.ResolveUserID(*hsUser); err != nil {
		fatal("resolve headscale user: %v", err)
	}
	fmt.Println("CONTROLLER-READY", *listen, "user-id", hs.UserID)
	srv := controller.NewServer(s, controller.Options{
		AdminTokenSHA: s.AdminTokenSHA(),
		Server:        *serverURL,
		Headscale:     hs,
	})
	if err := listenAndServe(*listen, srv.Handler()); err != nil {
		fatal("serve: %v", err)
	}
	return 0
}

func cmdInvite(args []string) int {
	if len(args) == 0 {
		usage()
		return errs.ExitUsage
	}
	s := openStore()
	switch args[0] {
	case "create":
		fs := flag.NewFlagSet("invite create", flag.ExitOnError)
		label := fs.String("label", "", "node label")
		profile := fs.String("profile", "default", "authorization profile")
		ttl := fs.Int("ttl-hours", 24, "validity in hours")
		if err := fs.Parse(args[1:]); err != nil {
			return errs.ExitUsage
		}
		code, inv, err := s.CreateInvitation(*label, *profile, time.Duration(*ttl)*time.Hour, "cli")
		if err != nil {
			fatal("create invitation: %v", err)
		}
		fmt.Println("INVITATION-CODE (one-time, shown once):")
		fmt.Println(code)
		fmt.Println("ID", inv.ID, "EXPIRES", inv.ExpiresAt.Format(time.RFC3339))
		return 0
	case "revoke":
		if len(args) < 2 {
			usage()
			return errs.ExitUsage
		}
		if err := s.RevokeInvitation(args[1], "cli"); err != nil {
			fatal("revoke: %v", err)
		}
		fmt.Println("REVOKED", args[1])
		return 0
	case "list":
		_, invs := s.Snapshot()
		for _, inv := range invs {
			fmt.Printf("%s  %-9s  expires=%s  label=%s\n", inv.ID, inv.State,
				inv.ExpiresAt.Format(time.RFC3339), inv.Label)
		}
		return 0
	default:
		usage()
		return errs.ExitUsage
	}
}

func cmdNode(args []string) int {
	if len(args) == 0 {
		usage()
		return errs.ExitUsage
	}
	s := openStore()
	switch args[0] {
	case "list":
		nodes, _ := s.Snapshot()
		for _, n := range nodes {
			fmt.Printf("%-24s %-9s profile=%s created=%s\n", n.ID, n.State, n.Profile,
				n.CreatedAt.Format(time.RFC3339))
		}
		return 0
	case "revoke":
		if len(args) < 2 {
			usage()
			return errs.ExitUsage
		}
		hs := controller.Headscale{BinPath: "/usr/bin/headscale", ConfigPath: "/etc/headscale/config.yaml"}
		if err := s.RevokeNode(args[1], "cli", &hs); err != nil {
			fatal("revoke node: %v", err)
		}
		fmt.Println("REVOKED", args[1])
		return 0
	default:
		usage()
		return errs.ExitUsage
	}
}

func cmdAudit(args []string) int {
	s := openStore()
	n := 20
	if len(args) > 0 {
		fmt.Sscanf(args[0], "%d", &n)
	}
	events := s.Audit()
	if n > len(events) {
		n = len(events)
	}
	for _, e := range events[len(events)-n:] {
		b, _ := json.Marshal(e)
		fmt.Println(string(b))
	}
	return 0
}

func listenAndServe(listen string, h http.Handler) error {
	srv := &http.Server{Addr: listen, Handler: h, ReadHeaderTimeout: 5 * time.Second}
	return srv.ListenAndServe()
}
