// Command hs-admin is the operator CLI (source spec §8 phase-5 command set):
// it talks to hs-controller for management-plane state and uses the OFFICIAL
// tailscale client as the only transport for shell/exec/sftp.
//
// Usage:
//
//	hs-admin server status
//	hs-admin invite create [--label L] [--profile P] [--ttl-hours H]
//	hs-admin invite revoke <id> | invite list
//	hs-admin node list | node revoke <id>
//	hs-admin shell <node> [--user U] [--socket S]
//	hs-admin exec <node> [--user U] [--socket S] -- <cmd...>
//	hs-admin sftp <node> [--user U] [--socket S]
//	hs-admin doctor [--api URL] [--socket S]
//	hs-admin version
package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/takomitkm/dialhome/hs/internal/admincli"
	"github.com/takomitkm/dialhome/internal/errs"
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
	case "server":
		return cmdServerStatus(args[1:])
	case "invite":
		return cmdInvite(args[1:])
	case "node":
		return cmdNode(args[1:])
	case "shell":
		return cmdShell(args[1:])
	case "exec":
		return cmdExec(args[1:])
	case "sftp":
		return cmdSFTP(args[1:])
	case "doctor":
		return cmdDoctor(args[1:])
	case "version":
		fmt.Printf("hs-admin %s\n", version)
		return 0
	default:
		usage()
		return errs.ExitUsage
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `usage:
  hs-admin server status
  hs-admin invite create [--label L] [--profile P] [--ttl-hours H]
  hs-admin invite revoke <id> | invite list
  hs-admin node list | node revoke <id> | node purge <id>
  hs-admin shell <node> [--user U] [--socket S]
  hs-admin exec <node> [--user U] [--socket S] -- <cmd...>
  hs-admin sftp <node> [--user U] [--socket S]
  hs-admin doctor [--api URL] [--socket S]
  hs-admin version

token: --token T | DIALHOME_HS_ADMIN_TOKEN | ~/.dialhome-hs/admin-token
`)
}

// apiURL resolves the controller base (flag > env > loopback default).
func apiURL(flagVal string) string {
	if flagVal != "" {
		return flagVal
	}
	if v := os.Getenv("DIALHOME_HS_API"); v != "" {
		return v
	}
	return "http://127.0.0.1:8800"
}

// clientFor builds the client or exits 2 with an actionable message.
func clientFor(api, tokenFlag string) *admincli.Client {
	tok := admincli.TokenFromSources(tokenFlag)
	if tok == "" {
		fmt.Fprintln(os.Stderr, "hs-admin: admin token not found -- pass --token, set DIALHOME_HS_ADMIN_TOKEN, or run `hs-controller init` on the VPS")
		os.Exit(errs.ExitUsage)
	}
	return admincli.New(api, tok)
}

func cmdServerStatus(args []string) int {
	_, _, api, _, _ := targetFlags(args)
	c := clientFor(apiURL(api), "")
	h, err := c.Health()
	if err != nil {
		fmt.Fprintf(os.Stderr, "hs-admin: controller unhealthy: %v\n", err)
		return errs.ExitGeneric
	}
	fmt.Println("CONTROLLER-OK", strings.TrimSpace(h))
	return 0
}

func cmdInvite(args []string) int {
	if len(args) == 0 {
		usage()
		return errs.ExitUsage
	}
	switch args[0] {
	case "create":
		// [--label L] [--profile P] [--ttl-hours H] [--api URL] [--token T]
		label, profile, ttlHours := "", "default", 24.0
		api, token := "", ""
		rest := args[1:]
		i := 0
		for i < len(rest) {
			switch rest[i] {
			case "--label":
				label = rest[i+1]
				i += 2
			case "--profile":
				profile = rest[i+1]
				i += 2
			case "--ttl-hours":
				fmt.Sscanf(rest[i+1], "%f", &ttlHours)
				i += 2
			case "--api":
				api = rest[i+1]
				i += 2
			case "--token":
				token = rest[i+1]
				i += 2
			default:
				i++
			}
		}
		c := clientFor(apiURL(api), token)
		code, id, err := c.CreateInvite(label, profile, ttlHours)
		if err != nil {
			return failInvite(err)
		}
		fmt.Println("INVITATION-CODE (one-time, shown once):")
		fmt.Println(code)
		fmt.Println("ID", id)
		return 0
	case "revoke":
		if len(args) < 2 {
			usage()
			return errs.ExitUsage
		}
		_, _, api, token, rest := targetFlags(args[1:])
		if len(rest) < 1 {
			usage()
			return errs.ExitUsage
		}
		c := clientFor(apiURL(api), token)
		if err := c.RevokeInvite(rest[0]); err != nil {
			return failInvite(err)
		}
		fmt.Println("REVOKED", rest[0])
		return 0
	case "list":
		_, _, api, token, _ := targetFlags(args[1:])
		c := clientFor(apiURL(api), token)
		_, invs, err := c.ListNodes()
		if err != nil {
			return failInvite(err)
		}
		fmt.Print(admincli.FormatInvitations(invs))
		return 0
	default:
		usage()
		return errs.ExitUsage
	}
}

func failInvite(err error) int {
	if err == admincli.ErrUnauthorized {
		fmt.Fprintln(os.Stderr, "hs-admin: admin token missing or rejected (pass --token or set DIALHOME_HS_ADMIN_TOKEN)")
	} else {
		fmt.Fprintf(os.Stderr, "hs-admin: %v\n", err)
	}
	return errs.ExitGeneric
}

func cmdNode(args []string) int {
	if len(args) == 0 {
		usage()
		return errs.ExitUsage
	}
	api := ""
	_, _, api, token, rest := targetFlags(args[1:])
	switch args[0] {
	case "list":
		c := clientFor(apiURL(api), token)
		nodes, _, err := c.ListNodes()
		if err != nil {
			return failInvite(err)
		}
		fmt.Print(admincli.FormatNodes(nodes))
		return 0
	case "revoke":
		if len(rest) < 1 {
			usage()
			return errs.ExitUsage
		}
		c := clientFor(apiURL(api), token)
		if err := c.RevokeNode(rest[0]); err != nil {
			return failInvite(err)
		}
		fmt.Println("REVOKED", rest[0])
		return 0
	case "purge":
		if len(rest) < 1 {
			usage()
			return errs.ExitUsage
		}
		c := clientFor(apiURL(api), token)
		if err := c.PurgeNode(rest[0]); err != nil {
			return failInvite(err)
		}
		fmt.Println("PURGED", rest[0])
		return 0
	default:
		usage()
		return errs.ExitUsage
	}
}

// targetFlags parses [--user U] [--socket S] [--api URL] [--token T].
func targetFlags(args []string) (user, socket, api, token string, rest []string) {
	i := 0
	for i < len(args) {
		switch args[i] {
		case "--user":
			if i+1 < len(args) {
				user = args[i+1]
				i += 2
				continue
			}
		case "--socket":
			if i+1 < len(args) {
				socket = args[i+1]
				i += 2
				continue
			}
		case "--api":
			if i+1 < len(args) {
				api = args[i+1]
				i += 2
				continue
			}
		case "--token":
			if i+1 < len(args) {
				token = args[i+1]
				i += 2
				continue
			}
		}
		rest = append(rest, args[i])
		i++
	}
	return user, socket, api, token, rest
}

func cmdShell(args []string) int {
	user, socket, _, _, rest := targetFlags(args)
	if len(rest) < 1 {
		usage()
		return errs.ExitUsage
	}
	argv := admincli.SSHArgs(socket, user, rest[0], true, nil)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "hs-admin: tailscale ssh failed: %v\n", err)
		return errs.ExitGeneric
	}
	return 0
}

func cmdExec(args []string) int {
	user, socket, _, _, rest := targetFlags(args)
	// "--" separates the target from the remote command
	sep := -1
	for i, a := range rest {
		if a == "--" {
			sep = i
			break
		}
	}
	if len(rest) < 1 || sep < 0 || sep+2 > len(rest) {
		usage()
		return errs.ExitUsage
	}
	node := rest[:sep][0]
	cmdArgs := rest[sep+1:]
	argv := admincli.SSHArgs(socket, user, node, false, cmdArgs)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "hs-admin: remote command failed: %v\n", err)
		return errs.ExitGeneric
	}
	return 0
}

func cmdSFTP(args []string) int {
	user, socket, _, _, rest := targetFlags(args)
	if len(rest) < 1 {
		usage()
		return errs.ExitUsage
	}
	argv, _ := admincli.SFTPProxy(socket, user, rest[0])
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "hs-admin: sftp failed: %v\n", err)
		return errs.ExitGeneric
	}
	return 0
}

func cmdDoctor(args []string) int {
	_, socket, api, _, _ := targetFlags(args)
	c := admincli.New(apiURL(api), admincli.TokenFromSources(""))
	health, _ := c.Health()
	stOut, _ := tailscaleStatus(socket)
	for _, line := range admincli.DoctorLines(apiURL(api), health, socket, stOut) {
		fmt.Println(line)
	}
	return 0
}

// tailscaleStatus shells out to the official client (read-only).
func tailscaleStatus(socket string) (string, error) {
	argv := []string{"tailscale"}
	if socket != "" {
		argv = append(argv, "--socket="+socket)
	}
	argv = append(argv, "status", "--json")
	out, err := exec.Command(argv[0], argv[1:]...).Output()
	return string(out), err
}
