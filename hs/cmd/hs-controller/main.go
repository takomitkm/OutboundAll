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
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
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
  hs-controller serve [--listen 127.0.0.1:8800] [--policy-file PATH]
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
	// CLI convenience: the same token in a 0600 file (the CLI is a thin API
	// client -- single writer is the serve process; H6-class split state is
	// what happens when two processes edit the registry directly).
	tokPath := dir + string(os.PathSeparator) + "admin-token"
	if err := os.WriteFile(tokPath, []byte(adminToken+"\n"), 0o600); err != nil {
		fatal("write admin-token: %v", err)
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
	policyFile := fs.String("policy-file", "", "headscale policy path hs-controller owns (deny-by-default ADR-003); empty disables policy management")
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
	reload := func(policyJSON string) error { return nil }
	if *policyFile != "" {
		reload = func(policyJSON string) error {
			tmp := *policyFile + ".tmp"
			if err := os.WriteFile(tmp, []byte(policyJSON), 0o644); err != nil {
				return err
			}
			if err := os.Rename(tmp, *policyFile); err != nil {
				return err
			}
			// headscale re-reads the policy on SIGHUP; same-user deployment
			// (deploy docs pair headscale + hs-controller under one account)
			out, err := runHUP()
			if err != nil {
				return fmt.Errorf("signal headscale: %v: %s", err, out)
			}
			return nil
		}
	}
	srv := controller.NewServer(s, controller.Options{
		AdminTokenSHA: s.AdminTokenSHA(),
		Server:        *serverURL,
		Headscale:     hs,
		ReloadPolicy:  reload,
	})
	if err := listenAndServe(*listen, srv.Handler()); err != nil {
		fatal("serve: %v", err)
	}
	return 0
}

// apiURL resolves the serve base URL for CLI calls.
func apiURL(flagVal string) string {
	if flagVal != "" {
		return flagVal
	}
	if v := os.Getenv("DIALHOME_HS_API"); v != "" {
		return v
	}
	return "http://127.0.0.1:8800"
}

// adminTokenForCLI reads the token file written by init.
func adminTokenForCLI() string {
	p := dataDir() + string(os.PathSeparator) + "admin-token"
	b, err := os.ReadFile(p)
	if err != nil {
		fatal("read admin token (%s): %v -- run `hs-controller init` first", p, err)
	}
	return strings.TrimSpace(string(b))
}

// apiCall performs an authenticated CLI->serve request and prints the body.
func apiCall(method, url, token string, body any) int {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, rd)
	if err != nil {
		fatal("request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fatal("api %s %s: %v -- is hs-controller serve running?", method, url, err)
	}
	defer resp.Body.Close()
	var buf strings.Builder
	_, _ = io.Copy(&buf, resp.Body)
	if resp.StatusCode >= 300 {
		fmt.Fprintf(os.Stderr, "hs-controller: %s %s -> HTTP %d: %s", method, url, resp.StatusCode, buf.String())
		return errs.ExitGeneric
	}
	fmt.Println(strings.TrimSpace(buf.String()))
	return 0
}

func cmdInvite(args []string) int {
	if len(args) == 0 {
		usage()
		return errs.ExitUsage
	}
	api := ""
	rest := args[1:]
	if len(args) >= 2 && args[1] == "--api" && len(args) >= 3 {
		api = args[2]
		rest = args[3:]
	}
	tok := adminTokenForCLI()
	switch args[0] {
	case "create":
		fs := flag.NewFlagSet("invite create", flag.ExitOnError)
		label := fs.String("label", "", "node label")
		profile := fs.String("profile", "default", "authorization profile")
		ttl := fs.Int("ttl-hours", 24, "validity in hours")
		if err := fs.Parse(rest); err != nil {
			return errs.ExitUsage
		}
		fmt.Println("INVITATION-CODE (one-time, shown once):")
		return apiCall("POST", apiURL(api)+"/api/v1/invitations", tok,
			map[string]any{"label": *label, "profile": *profile, "ttl_hours": *ttl})
	case "revoke":
		if len(rest) < 1 {
			usage()
			return errs.ExitUsage
		}
		return apiCall("DELETE", apiURL(api)+"/api/v1/invitations/"+rest[0], tok, nil)
	case "list":
		return apiCall("GET", apiURL(api)+"/api/v1/nodes", tok, nil)
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
	api := ""
	rest := args[1:]
	if len(args) >= 2 && args[1] == "--api" && len(args) >= 3 {
		api = args[2]
		rest = args[3:]
	}
	tok := adminTokenForCLI()
	switch args[0] {
	case "list":
		return apiCall("GET", apiURL(api)+"/api/v1/nodes", tok, nil)
	case "revoke":
		if len(rest) < 1 {
			usage()
			return errs.ExitUsage
		}
		return apiCall("POST", apiURL(api)+"/api/v1/nodes/"+rest[0]+"/revoke", tok, nil)
	default:
		usage()
		return errs.ExitUsage
	}
}

func cmdAudit(args []string) int {
	api := ""
	if len(args) >= 2 && args[0] == "--api" {
		api = args[1]
	}
	tok := adminTokenForCLI()
	return apiCall("GET", apiURL(api)+"/api/v1/audit", tok, nil)
}

// runHUP sends SIGHUP to the headscale process (best-effort process lookup).
func runHUP() (string, error) {
	out, err := exec.Command("pidof", "headscale").CombinedOutput()
	if err != nil {
		return string(out), err
	}
	pid := strings.TrimSpace(string(out))
	if pid == "" {
		return string(out), fmt.Errorf("headscale pid not found")
	}
	return "", exec.Command("kill", "-HUP", pid).Run()
}

func listenAndServe(listen string, h http.Handler) error {
	srv := &http.Server{Addr: listen, Handler: h, ReadHeaderTimeout: 5 * time.Second}
	return srv.ListenAndServe()
}
