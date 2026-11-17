package cli

import (
	cryptorand "crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/takomitkm/dialhome/internal/crypto"
	"github.com/takomitkm/dialhome/internal/errs"
	"github.com/takomitkm/dialhome/internal/platform"
	"github.com/takomitkm/dialhome/internal/relay"
)

// relayPaths resolves the on-disk locations for the relay role.
func relayPaths() (store, ak, helperBin string, err error) {
	base := relayDataDir()
	if err := os.MkdirAll(base, 0o700); err != nil {
		return "", "", "", err
	}
	bin, err := os.Executable()
	if err != nil {
		bin = "dialhome"
	}
	home, herr := os.UserHomeDir()
	if herr != nil {
		return "", "", "", herr
	}
	akPath := filepath.Join(home, ".ssh", "authorized_keys")
	// Tests (and operator-side dry runs) can redirect the AK to a sandbox file.
	// Production never sets this; the default is always the system AK.
	if custom := os.Getenv("DIALHOME_RELAY_AK"); custom != "" {
		akPath = custom
	}
	return base, akPath, bin, nil
}

func runRelayImpl(args []string) error {
	if len(args) == 0 {
		return usage()
	}
	base, akPath, helperBin, err := relayPaths()
	if err != nil {
		return err
	}
	_ = base
	store := relay.NewStore(base+string(os.PathSeparator)+"registry.json", base+string(os.PathSeparator)+"registry.lock")

	switch args[0] {
	case "init":
		fsInit := flag.NewFlagSet("relay init", flag.ContinueOnError)
		advertise := fsInit.String("advertise", "", "HOST:PORT nodes dial for enrollment (default: documented site address 10.101.54.192:18322; use 127.0.0.1:22 for co-located)")
		relayUser := fsInit.String("relay-user", "", "SSH account on the relay that nodes dial (default: current user)")
		if err := fsInit.Parse(args[1:]); err != nil {
			return err
		}
		// registry.json default + helper path + advertising config
		err = store.WithLock(func(r *relay.Registry) error {
			r.HelperPath = helperBin
			if *advertise != "" {
				r.Advertise = *advertise
			}
			if *relayUser != "" {
				r.AdvertiseUser = *relayUser
			}
			return nil
		})
		if err != nil {
			return err
		}
		_ = os.MkdirAll(filepath.Join(base, "backups"), 0o700)
		if _, err := os.Stat(akPath); errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("system authorized_keys missing at %s (create it first)", akPath)
		}
		fmt.Println("relay init ok: " + base)
		fmt.Println("managing system authorized_keys: " + akPath)
		return nil

	case "credentials":
		if len(args) < 3 {
			return fmt.Errorf("%w: credentials issue <name> [--manager-pub FILE] | credentials revoke <id>", errs.ErrUsage)
		}
		switch args[1] {
		case "issue":
			fs := flag.NewFlagSet("credentials issue", flag.ContinueOnError)
			managerPub := fs.String("manager-pub", "", "path to the controller manager public key line")
			if err := fs.Parse(args[3:]); err != nil {
				return err
			}
			return relayCredentialIssue(store, akPath, helperBin, args[2], *managerPub)
		case "revoke":
			return relayCredentialRevoke(store, akPath, args[2])
		default:
			return fmt.Errorf("%w: unknown credentials verb %q", errs.ErrUsage, args[1])
		}

	case "enroll":
		// `relay enroll <name>` issues a credential for <name> (spec §5.2 uses this form too)
		return relayCredentialIssue(store, akPath, helperBin, args[1], "")

	case "list":
		r, err := store.Load()
		if err != nil {
			return err
		}
		fmt.Printf("%-24s %-8s %-6s %-6s %-20s %s\n", "NODE", "STATE", "APP", "SSH", "TUNNEL-FP", "HW")
		for name, n := range r.Nodes {
			hw := "-"
			if n.Hardware.UUID != "" {
				hw = "uuid✓"
			}
			fmt.Printf("%-24s %-8s %-6d %-6d %-20s %s\n", name, n.State, n.Ports.App, n.Ports.SSH, short(n.TunnelKeyFP, 18), hw)
		}
		return nil

	case "inspect":
		if len(args) < 2 {
			return fmt.Errorf("%w: inspect <name>", errs.ErrUsage)
		}
		r, err := store.Load()
		if err != nil {
			return err
		}
		n, ok := r.Nodes[args[1]]
		if !ok {
			return fmt.Errorf("node %q not found", args[1])
		}
		raw, _ := json.MarshalIndent(n, "", "  ")
		fmt.Println(string(raw))
		return nil

	case "migrate-legacy":
		// import legacy magpie-node-* / magpie-tunnel-node-* AK lines + old
		// machines.json into the new registry, rewriting ONLY the forced command
		// (same key, same ports -- running tunnels are never disturbed)
		oldAK := ""
		if len(args) > 1 {
			oldAK = args[1]
		}
		return relayMigrateLegacy(store, akPath, helperBin, base, oldAK)

	case "ports":
		r, err := store.Load()
		if err != nil {
			return err
		}
		for _, p := range relay.UsedPorts(r) {
			fmt.Println(p)
		}
		return nil

	case "revoke":
		if len(args) < 2 {
			return fmt.Errorf("%w: revoke <name>", errs.ErrUsage)
		}
		return relayRevoke(store, akPath, args[1])

	case "purge":
		// permanently delete a REVOKED node's tombstone. Active nodes are refused
		// so a typo can never free a live production node's registration.
		if len(args) < 2 {
			return fmt.Errorf("%w: purge <name>", errs.ErrUsage)
		}
		return relayPurge(store, akPath, args[1])

	case "reconcile", "verify":
		return fmt.Errorf("%w: relay %s lands in Phase 6/9 (needs node channel)", errs.ErrNotImplemented, args[0])

	default:
		return fmt.Errorf("%w: unknown relay subcommand %q", errs.ErrUsage, args[0])
	}
}

func relayCredentialIssue(store *relay.Store, akPath, helperBin, name string, managerPubPath string) error {
	kp, err := crypto.Generate("dialhome-credential")
	if err != nil {
		return err
	}
	// Refuse to publish a self-inconsistent key: the registry fingerprint, the
	// authorized_keys line and the shipped container all come from this one kp,
	// and if its halves disagree the node can only fail at the relay with
	// "Permission denied (publickey)" after the single-use credential is spent.
	if err := crypto.VerifyKeyPair(kp); err != nil {
		return fmt.Errorf("credential self-check: %w", err)
	}
	credID := "cred-" + randHex(6)
	exp := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)
	managerPub := ""
	if managerPubPath != "" {
		raw, err := os.ReadFile(managerPubPath)
		if err != nil {
			return fmt.Errorf("manager-pub: %w", err)
		}
		managerPub = strings.TrimSpace(string(raw))
	} else if raw, err := os.ReadFile(filepath.Join(platform.DataDir("relay"), "manager.pub")); err == nil {
		managerPub = strings.TrimSpace(string(raw))
	}
	var credB64 string
	err = store.WithLock(func(r *relay.Registry) error {
		// idempotent: an existing node keeps its ports
		ports, err := relay.AllocatePair(r, relay.UsedPorts(r), livePorts())
		if err != nil {
			return err
		}
		n := r.Nodes[name]
		if n == nil {
			n = &relay.NodeRecord{Ports: ports, RegisteredAt: time.Now().UTC().Format(time.RFC3339)}
			r.Nodes[name] = n
		} else {
			n.CredentialID = credID
		}
		if n.State == "" || n.State == "revoked" {
			n.State = "pending"
		}
		n.CredentialID = credID
		r.Credentials[credID] = &relay.Credential{
			Node:      name,
			PubKeyFP:  kp.Public.Fingerprint(),
			ExpiresAt: exp,
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
		}
		raw, rerr := os.ReadFile(akPath)
		if rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
			return rerr
		}
		entries := relay.Parse(string(raw))
		entries = append(entries, relay.Entry{Raw: relay.CredentialLine(helperBin, credID, kp.Public), Kind: "credential", Name: credID})
		if err := relay.WriteWithBackup(akPath, relay.Render(entries)); err != nil {
			return err
		}
		doc := map[string]any{
			"credential_version":      1,
			"node_name":               name,
			"private_key_openssh_b64": base64.StdEncoding.EncodeToString([]byte(kp.Private)),
			"manager_pubkey":          managerPub,
			"ports_ssh":               n.Ports.SSH,
			"ports_app":               n.Ports.App,
			"relay_address":           advertiseAddr(r),
			"relay_port":              advertisePort(r),
			"relay_user":              advertiseUser(r),
			"expires_at":              exp,
		}
		docB, _ := json.MarshalIndent(doc, "", "  ")
		credB64 = base64.StdEncoding.EncodeToString(docB)
		return nil
	})
	if err != nil {
		return err
	}
	fmt.Println("CREDENTIAL-B64 = " + credB64)
	fmt.Println("feed it to: dialhome node install --credential <that b64 or file>")
	return nil
}

func relayCredentialRevoke(store *relay.Store, akPath, credID string) error {
	return store.WithLock(func(r *relay.Registry) error {
		if c, ok := r.Credentials[credID]; ok {
			delete(r.Credentials, credID)
			_ = c
		} else {
			return fmt.Errorf("credential %s not found", credID)
		}
		raw, err := os.ReadFile(akPath)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		entries := relay.Parse(string(raw))
		entries, removed := relay.RemoveCredential(entries, credID)
		if !removed {
			fmt.Println("credential AK line already absent")
		}
		return relay.WriteWithBackup(akPath, relay.Render(entries))
	})
}

func relayRevoke(store *relay.Store, akPath, name string) error {
	return store.WithLock(func(r *relay.Registry) error {
		n, ok := r.Nodes[name]
		if !ok {
			return fmt.Errorf("node %s not found", name)
		}
		n.State = "revoked"
		// P1: a still-unused enrollment credential bound to this node could
		// otherwise resurrect it (EnrollCommand used to set State=active without
		// checking). Retire every credential of this node alongside the node key,
		// and remove their authorized_keys lines. Both the helper guard and this
		// cleanup are required for defence in depth.
		credIDs := []string{}
		for id, c := range r.Credentials {
			if c.Node == name {
				credIDs = append(credIDs, id)
				delete(r.Credentials, id)
			}
		}
		// invalidate any stale binding left on the tombstone
		n.CredentialID = ""
		raw, err := os.ReadFile(akPath)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		entries := relay.Parse(string(raw))
		entries, removed := relay.RemoveNode(entries, name)
		if !removed {
			fmt.Println("node AK line already absent")
		}
		for _, id := range credIDs {
			entries, _ = relay.RemoveCredential(entries, id)
		}
		if err := relay.WriteWithBackup(akPath, relay.Render(entries)); err != nil {
			return err
		}
		fmt.Printf("revoked %s (ports %d/%d freed; %d enrollment credential(s) retired)\n",
			name, n.Ports.App, n.Ports.SSH, len(credIDs))
		return nil
	})
}

// relayPurge removes a revoked node's registry tombstone AND every credential
// still bound to it -- including the corresponding authorized_keys lines, so no
// dead public key is left trusted by sshd (review finding P2). It refuses
// active/pending nodes deliberately: purging a live node would silently free its
// port pair for another enrollment.
func relayPurge(store *relay.Store, akPath, name string) error {
	return store.WithLock(func(r *relay.Registry) error {
		n, ok := r.Nodes[name]
		if !ok {
			return fmt.Errorf("node %s not found", name)
		}
		if n.State != "revoked" {
			return fmt.Errorf("refusing to purge node %s in state %q (revoke it first)", name, n.State)
		}
		delete(r.Nodes, name)
		credIDs := []string{}
		for id, c := range r.Credentials {
			if c.Node == name {
				credIDs = append(credIDs, id)
				delete(r.Credentials, id)
			}
		}
		// remove the credential AK lines too: registry and sshd must agree
		if len(credIDs) > 0 {
			raw, err := os.ReadFile(akPath)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			entries := relay.Parse(string(raw))
			for _, id := range credIDs {
				entries, _ = relay.RemoveCredential(entries, id)
			}
			if err := relay.WriteWithBackup(akPath, relay.Render(entries)); err != nil {
				return err
			}
		}
		fmt.Printf("purged %s (tombstone + %d credential(s) + their AK lines removed; ports %d/%d free for reuse)\n",
			name, len(credIDs), n.Ports.App, n.Ports.SSH)
		return nil
	})
}

func runRelayHelperImpl(args []string) error {
	fs := flag.NewFlagSet("relay-helper", flag.ContinueOnError)
	port := fs.Int("port", 0, "ssh port baked into the caller's authorized_keys line")
	cred := fs.String("credential", "", "credential id baked into the caller's authorized_keys line")
	akPath := fs.String("ak", "", "system authorized_keys path (defaults to ~/.ssh/authorized_keys)")
	base := relayDataDir()
	fs.StringVar(&base, "base", base, "relay data dir")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *akPath == "" {
		home, herr := os.UserHomeDir()
		if herr != nil {
			return herr
		}
		*akPath = filepath.Join(home, ".ssh", "authorized_keys")
	}
	original := os.Getenv("SSH_ORIGINAL_COMMAND")
	store := relay.NewStore(base+string(os.PathSeparator)+"registry.json", base+string(os.PathSeparator)+"registry.lock")
	h := &relay.Helper{
		Store:      store,
		AKPath:     *akPath,
		OpenSSHZip: base + string(os.PathSeparator) + "openssh/OpenSSH-Win64.zip",
		Now:        time.Now,
	}
	verb := ""
	if fs.NArg() > 0 {
		verb = fs.Arg(0) // positional form: relay-helper enroll|tunnel ...
	}
	switch {
	case *cred != "" || verb == "enroll":
		out, code := h.EnrollCommand(*cred, original)
		fmt.Print(out)
		if code != 0 {
			os.Exit(code)
		}
	case *port != 0 || verb == "tunnel":
		out, code := h.TunnelCommand(*port, original)
		if out != "" {
			fmt.Print(out)
		}
		if code != 0 {
			os.Exit(code)
		}
	default:
		return fmt.Errorf("%w: relay-helper needs enroll --credential <id> | tunnel --port <p>", errs.ErrUsage)
	}
	return nil
}

func livePorts() []int {
	out, err := exec.Command("ss", "-ltn").Output()
	if err != nil {
		return nil
	}
	// single source of truth: relay.LivePortsFromSS handles wildcard/IPv6 binds
	var ports []int
	for _, s := range relay.LivePortsFromSS(string(out)) {
		p := 0
		for _, r := range s {
			p = p*10 + int(r-'0')
		}
		if p > 0 {
			ports = append(ports, p)
		}
	}
	return ports
}

// advertiseUser is the SSH account on the relay that nodes dial. Configurable
// via `relay init --relay-user`; falls back to the process user (which is the
// relay account on a normal deployment), then to the legacy "user" default.
func advertiseUser(r *relay.Registry) string {
	if r != nil && r.AdvertiseUser != "" {
		return r.AdvertiseUser
	}
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	if u := os.Getenv("USERNAME"); u != "" {
		return u
	}
	return "user"
}

// advertiseAddr returns the HOST part only (port comes from advertisePort).
// The default is the documented site address; override with `relay init --advertise`
// (e.g. 127.0.0.1:22 when controller/relay/node share a host).
func advertiseAddr(r *relay.Registry) string {
	full := "10.101.54.192"
	if r != nil && r.Advertise != "" {
		full = r.Advertise
	}
	if i := strings.LastIndex(full, ":"); i >= 0 {
		port := full[i+1:]
		numeric := true
		for _, c := range port {
			if c < '0' || c > '9' {
				numeric = false
				break
			}
		}
		if numeric {
			return full[:i]
		}
	}
	return full
}

// advertisePort derives the ssh port from the advertised address (default 18322
// for the public address, 22 for a bare loopback co-located relay).
func advertisePort(r *relay.Registry) int {
	addr := advertiseAddr(r)
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		p := 0
		for _, c := range addr[i+1:] {
			if c < '0' || c > '9' {
				p = 0
				break
			}
			p = p*10 + int(c-'0')
		}
		if p > 0 {
			return p
		}
	}
	if addr == "127.0.0.1" {
		return 22
	}
	return 18322
}

// relayDataDir is ALWAYS ~/.dialhome on the relay machine -- it must not honor
// DIALHOME_DATA_DIR (that override exists for node/controller testing; the helper
// runs under sshd whose env never matches the caller's shell, and a mismatch makes
// credentials "unknown"). Explicit --base still wins for tests.
func relayDataDir() string {
	if custom := os.Getenv("DIALHOME_RELAY_DIR"); custom != "" {
		return custom
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return platform.DataDir("relay")
	}
	return filepath.Join(home, ".dialhome")
}

func randHex(n int) string {
	const hex = "0123456789abcdef"
	b := make([]byte, n)
	_, _ = cryptorand.Read(b)
	for i := range b {
		b[i] = hex[int(b[i])%16]
	}
	return string(b)
}

func short(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
