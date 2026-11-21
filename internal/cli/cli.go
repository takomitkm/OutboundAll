// Package cli routes subcommands. Keep it thin: parse, dispatch, report.
// Exit codes: 0 ok; 1 generic failure; 2 usage error; 10+ domain codes (see internal/errs).
package cli

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/takomitkm/outboundall/internal/config"
	"github.com/takomitkm/outboundall/internal/crypto"
	"github.com/takomitkm/outboundall/internal/errs"
	"github.com/takomitkm/outboundall/internal/platform"
	"github.com/takomitkm/outboundall/internal/relay"
)

// Run executes argv (without the program name).
func Run(argv []string) error {
	if len(argv) == 0 {
		return usage()
	}
	role, rest := argv[0], argv[1:]
	switch role {
	case "-h", "--help", "help":
		return usage()
	case "node":
		return runNodeImpl(rest)
	case "controller":
		return runControllerImpl(rest)
	case "relay":
		return runRelayImpl(rest)
	case "relay-helper":
		return runRelayHelperImpl(rest)
	case "autostart":
		return runAutostart(rest)
	case "service":
		return runService(rest)
	case "migrate":
		return runMigrate(rest)
	case "doctor":
		return runDoctor(rest)
	case "keygen":
		return runKeygen(rest)
	case "version":
		fmt.Println("OutboundAll " + version + " (" + commit + ")")
		return nil
	default:
		return fmt.Errorf("%w: unknown command %q", errs.ErrUsage, role)
	}
}

func usage() error {
	fmt.Print(`OutboundAll -- reverse SSH access for NAT-ed, admin-less machines

  OutboundAll node install [--mode temporary|persistent] [--credential FILE] [--relay USER@HOST:PORT] [--name NAME] [--openssh-zip FILE]
  OutboundAll node run | status | stop | uninstall | logs [channel]
  OutboundAll controller init | list | status | anchors <node> | shell <node> | exec <node> <command...>
  OutboundAll relay init [--advertise HOST:PORT] [--relay-user USER] | list | inspect <name> | revoke <name> | purge <name> | ports
  OutboundAll relay credentials issue <name> [--manager-pub FILE] | revoke <id>
  OutboundAll relay migrate-legacy [LEGACY-DIR]   (idempotent: re-registers surviving AK lines)
  OutboundAll relay-helper --port <p> tunnel | --credential <id> enroll   (internal forced command)
  OutboundAll autostart install | uninstall | status       (user scope, no admin)
  OutboundAll service install | uninstall | status --scope system   (OPTIONAL, needs root, never default)
  OutboundAll migrate --from-legacy [--legacy-dir DIR]
  OutboundAll doctor
  OutboundAll keygen --out FILE [--comment TEXT]
  OutboundAll version

Three identities: node tunnel key (local-only), controller manager key (controller-only),
short-lived enrollment credential (one-shot). All channels are literal 127.0.0.1 loopback.
Docs: docs/README.md (manual index), docs/ARCHITECTURE.md, docs/SECURITY_MODEL.md,
docs/NODE_MANUAL.md, docs/CONTROLLER_MANUAL.md, docs/RELAY_MANUAL.md, docs/man-OutboundAll.md
`)
	return nil
}

// version/commit are set by the release build via -ldflags; keep dev defaults.
var version = "dev"
var commit = "none"

// SetVersion injects build metadata from main (ldflags cannot target this package
// directly when the binary is built from ./cmd/OutboundAll).
func SetVersion(v, c string) {
	if v != "" {
		version = v
	}
	if c != "" {
		commit = c
	}
}

// runKeygen writes a fresh ed25519 pair: private key -> --out (hardened), public
// line -> stdout. Used by node install internally and by tests.
func runKeygen(args []string) error {
	fs := flag.NewFlagSet("keygen", flag.ContinueOnError)
	out := fs.String("out", "", "private key output path (created 0600-equivalent)")
	comment := fs.String("comment", "OutboundAll-node", "key comment")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" {
		return fmt.Errorf("%w: keygen requires --out", errs.ErrUsage)
	}
	kp, err := crypto.Generate(*comment)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(*out, []byte(kp.Private), 0o600); err != nil {
		return err
	}
	if err := platform.HardenFile(*out); err != nil {
		return err
	}
	fmt.Println(kp.Public.Line())
	return nil
}

func runAutostart(args []string) error {
	if len(args) == 0 {
		return usage()
	}
	np := nodeLayout()
	switch args[0] {
	case "install":
		cfg, err := config.Load(np.Config)
		name := "node"
		if err == nil && cfg.Node.Name != "" {
			name = cfg.Node.Name
		}
		if err := autostartEnsure(np, name); err != nil {
			return err
		}
		fmt.Println("AUTOSTART = registered (user scope, no elevation)")
		return nil
	case "uninstall":
		autostartRemove()
		fmt.Println("AUTOSTART = removed (user scope)")
		return nil
	case "status":
		return autostartStatus()
	default:
		return fmt.Errorf("%w: unknown autostart subcommand %q", errs.ErrUsage, args[0])
	}
}

// autostartStatus reports which user-level autostart entry exists (if any).
func autostartStatus() error {
	switch runtime.GOOS {
	case "windows":
		present, value, err := winRunKeyGet(runKeyName)
		if err != nil {
			return err
		}
		fmt.Printf("AUTOSTART = %s  [%s]\n", boolWord(present), value)
	case "darwin":
		p := filepath.Join(launchAgentsDir(), "OutboundAll-node.plist")
		_, err := os.Stat(p)
		fmt.Printf("AUTOSTART = %s  [%s]\n", boolWord(err == nil), p)
	default:
		_, unitErr := os.Stat(userUnitPath())
		home, _ := os.UserHomeDir()
		xdg := filepath.Join(home, ".config", "autostart", "OutboundAll-node.desktop")
		_, xdgErr := os.Stat(xdg)
		fmt.Printf("systemd --user unit = %s  [%s]\n", boolWord(unitErr == nil), userUnitPath())
		fmt.Printf("XDG autostart       = %s  [%s]\n", boolWord(xdgErr == nil), xdg)
	}
	return nil
}

func boolWord(b bool) string {
	if b {
		return "registered"
	}
	return "absent"
}

// runService manages the OPTIONAL system-scope service. It is deliberately a
// separate command that is never called by node install (spec §6.2: "optional,
// never default"). Installing it requires elevation and is the operator's explicit
// choice; autostart (user scope) remains the zero-admin default.
func runService(args []string) error {
	fs := flag.NewFlagSet("service", flag.ContinueOnError)
	scope := fs.String("scope", "system", "service scope (system only; user scope is 'autostart')")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *scope != "system" {
		return fmt.Errorf("%w: only --scope system is supported here; use 'OutboundAll autostart' for user scope", errs.ErrUsage)
	}
	verb := ""
	for _, a := range fs.Args() {
		verb = a
		break
	}
	switch verb {
	case "install":
		return systemServiceInstall()
	case "uninstall":
		return systemServiceUninstall()
	case "status":
		return systemServiceStatus()
	default:
		return fmt.Errorf("%w: service install|uninstall|status --scope system", errs.ErrUsage)
	}
}

func runMigrate(args []string) error {
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--from-legacy") {
		return fmt.Errorf("%w: migrate requires --from-legacy", errs.ErrUsage)
	}
	// The legacy state lives on the RELAY (system authorized_keys + old
	// ~/.magpie-connector/machines.json). Delegate to the same idempotent importer
	// used by 'relay migrate-legacy' so there is exactly one migration path.
	base, ak, helper, err := relayPaths()
	if err != nil {
		return err
	}
	legacyDir := ""
	for i, a := range args {
		if a == "--legacy-dir" && i+1 < len(args) {
			legacyDir = args[i+1]
		}
	}
	if legacyDir == "" {
		home, _ := os.UserHomeDir()
		legacyDir = filepath.Join(home, ".magpie-connector")
	}
	store := relay.NewStore(base+string(os.PathSeparator)+"registry.json", base+string(os.PathSeparator)+"registry.lock")
	return relayMigrateLegacy(store, ak, helper, base, legacyDir)
}

func runDoctor(args []string) error {
	np := nodeLayout()
	fmt.Println("OutboundAll doctor")
	fmt.Printf("  platform            = %s/%s\n", runtime.GOOS, runtime.GOARCH)
	fmt.Printf("  node data dir       = %s\n", np.Data)
	cfg, err := config.Load(np.Config)
	if err != nil {
		fmt.Println("  node config         = MISSING (run 'OutboundAll node install' first)")
	} else {
		fmt.Printf("  node config         = ok (name=%s mode=%s channels=%d)\n",
			cfg.Node.Name, cfg.Node.Mode, len(cfg.Node.Channels))
		for _, ch := range cfg.Node.Channels {
			fmt.Printf("    channel %-10s %s -> %s enabled=%v\n", ch.Name, ch.Local, ch.Remote, ch.Enabled)
		}
	}
	if _, err := os.Stat(np.Identity); err == nil {
		fmt.Println("  identity            = ok")
	} else {
		fmt.Println("  identity            = MISSING")
	}
	if _, err := os.Stat(filepath.Join(np.Keys, "tunnel.key")); err == nil {
		fmt.Println("  tunnel key          = ok")
	} else {
		fmt.Println("  tunnel key          = MISSING")
	}
	if _, err := os.Stat(filepath.Join(np.Keys, "manager.pub")); err == nil {
		// controller role co-located
		fmt.Println("  manager key         = ok (controller role present)")
	}
	if err := autostartStatus(); err != nil {
		fmt.Printf("  autostart           = check failed: %v\n", err)
	}
	fmt.Println("  (doctor is read-only; no state is changed)")
	return nil
}
