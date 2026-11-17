package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/takomitkm/dialhome/internal/config"
	"github.com/takomitkm/dialhome/internal/crypto"
	"github.com/takomitkm/dialhome/internal/errs"
	"github.com/takomitkm/dialhome/internal/identity"
	"github.com/takomitkm/dialhome/internal/openssh"
	"github.com/takomitkm/dialhome/internal/platform"
	"github.com/takomitkm/dialhome/internal/supervisor"
)

// nodePaths resolves the node data layout (docs/CONFIG_SCHEMA.md §1).
type nodePaths struct {
	Data      string
	Config    string
	Identity  string
	Keys      string
	Logs      string
	State     string
	OpenSSH   string
	SSHDLog   string
	TunnelLog string
}

func nodeLayout() nodePaths {
	data := platform.DataDir("node")
	return nodePaths{
		Data:      data,
		Config:    filepath.Join(data, "config.json"),
		Identity:  filepath.Join(data, "identity.json"),
		Keys:      filepath.Join(data, "keys"),
		Logs:      filepath.Join(data, "logs"),
		State:     filepath.Join(data, "state"),
		OpenSSH:   filepath.Join(data, "openssh"),
		SSHDLog:   filepath.Join(data, "logs", "sshd.log"),
		TunnelLog: filepath.Join(data, "logs"),
	}
}

// credentialDoc is the enrollment credential the node consumes (CONFIG_SCHEMA §5).
type credentialDoc struct {
	CredentialVersion    int    `json:"credential_version"`
	NodeName             string `json:"node_name"`
	PrivateKeyOpenSSHB64 string `json:"private_key_openssh_b64"`
	ManagerPubkey        string `json:"manager_pubkey"`
	ExpiresAt            string `json:"expires_at"`
	PortsSSH             int    `json:"ports_ssh,omitempty"`
	PortsApp             int    `json:"ports_app,omitempty"`
	RelayAddress         string `json:"relay_address,omitempty"`
	RelayPort            int    `json:"relay_port,omitempty"`
	RelayUser            string `json:"relay_user,omitempty"`
}

func runNodeImpl(args []string) error {
	if len(args) == 0 {
		return usage()
	}
	np := nodeLayout()
	switch args[0] {
	case "install":
		return nodeInstall(np, args[1:])
	case "run":
		return nodeRun(np, args[1:])
	case "status":
		return nodeStatus(np)
	case "stop":
		return nodeStop(np)
	case "uninstall":
		return nodeUninstall(np)
	case "logs":
		return nodeLogs(np, args[1:])
	default:
		return fmt.Errorf("%w: unknown node subcommand %q", errs.ErrUsage, args[0])
	}
}

func nodeInstall(np nodePaths, args []string) error {
	fs := flag.NewFlagSet("node install", flag.ContinueOnError)
	mode := fs.String("mode", "persistent", "persistent | temporary")
	credPath := fs.String("credential", "", "enrollment credential file (or @b64 literal)")
	relayStr := fs.String("relay", "user@10.101.54.192:18322", "user@host:port")
	name := fs.String("name", "", "node name (defaults to credential's node_name)")
	opensshSrc := fs.String("openssh-zip", "", "local OpenSSH-Win64.zip path (Windows; skips download)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *mode != "persistent" && *mode != "temporary" {
		return fmt.Errorf("%w: --mode must be persistent or temporary", errs.ErrUsage)
	}
	cred, err := loadCredential(*credPath)
	if err != nil {
		return err
	}
	nodeName := *name
	if nodeName == "" {
		nodeName = cred.NodeName
	}
	if nodeName == "" {
		return fmt.Errorf("%w: node name missing (pass --name)", errs.ErrUsage)
	}

	relayAddr, relayPort, relayUser := parseRelay(*relayStr, cred)

	// 1) data dirs
	for _, d := range []string{np.Data, np.Keys, np.Logs, np.State, np.OpenSSH} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}

	// 2) portable OpenSSH (Windows needs it; unix uses the system binaries but we
	//    still record the paths in config)
	sshExe, sshdExe := "", ""
	if runtimeIsWindows() {
		zip := *opensshSrc
		if zip == "" {
			zip = filepath.Join(np.OpenSSH, "OpenSSH-Win64.zip")
		}
		// sshArgs is "" on purpose: before enrollment this node holds only the
		// one-shot credential, whose forced command is `enroll`, so nothing can
		// serve `get openssh` yet. Fetch's error names the two ways out
		// (--openssh-zip, or a mirror via openssh.MirrorEnv).
		if err := openssh.EnsureWindowsPackage(np.OpenSSH, zip, ""); err != nil {
			return fmt.Errorf("openssh package: %w", err)
		}
		sshExe = filepath.Join(np.OpenSSH, "OpenSSH-Win64", "ssh.exe")
		sshdExe = filepath.Join(np.OpenSSH, "OpenSSH-Win64", "sshd.exe")
	} else {
		sshExe = "/usr/bin/ssh"
		sshdExe = "/usr/sbin/sshd"
		if _, err := os.Stat(sshdExe); err != nil {
			// conservative fallbacks
			for _, cand := range []string{"/usr/sbin/sshd", "/usr/bin/sshd"} {
				if _, e := os.Stat(cand); e == nil {
					sshdExe = cand
					break
				}
			}
		}
	}

	// 3) local keys: host key + tunnel key (private keys never leave this machine)
	hostKey := filepath.Join(np.Keys, "host_ed25519")
	tunnelKey := filepath.Join(np.Keys, "tunnel.key")
	if err := ensureHostKey(sshdExe, hostKey); err != nil {
		return err
	}
	tunnelPubLine := ""
	if raw, err := os.ReadFile(tunnelKey); err == nil && len(raw) > 0 {
		tunnelPubLine = pubOf(tunnelKey, sshExe)
	} else {
		kp, kerr := crypto.Generate("dialhome-node-" + nodeName)
		if kerr != nil {
			return kerr
		}
		if err := os.WriteFile(tunnelKey, []byte(kp.Private), 0o600); err != nil {
			return err
		}
		if err := platform.HardenFile(tunnelKey); err != nil {
			return err
		}
		tunnelPubLine = kp.Public.Line()
	}
	if err := supervisor.HardenKeys(np.Data); err != nil {
		return err
	}

	// 9b) enroll: submit the tunnel PUBLIC key through the credential's forced
	// command (PROTOCOL §1 step 2). The credential private key lives only in the
	// credential file; it is deleted right after this one-shot use.
	if cred.PrivateKeyOpenSSHB64 != "" {
		credKey := filepath.Join(np.State, "cred-use.key")
		raw, derr := base64.StdEncoding.DecodeString(cred.PrivateKeyOpenSSHB64)
		if derr != nil {
			return fmt.Errorf("credential key: %w", derr)
		}
		// Self-check BEFORE the one-shot ssh: a container whose public half does
		// not match its seed is offered by ssh and rejected by the relay as a bare
		// "Permission denied (publickey)", which costs the credential and names
		// neither half. Fail locally, with something actionable, instead.
		if err := crypto.VerifyPrivateKey(string(raw), nil); err != nil {
			return fmt.Errorf("%w: credential private key is self-inconsistent (%v) — ask the controller to re-issue it",
				errs.ErrUsage, err)
		}
		if err := os.WriteFile(credKey, raw, 0o600); err != nil {
			return err
		}
		platform.HardenFile(credKey)
		enrollCmd := exec.Command(sshExe, "-p", itoaPort(relayPort), "-i", credKey,
			"-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=accept-new",
			"-o", "ConnectTimeout=10",
			relayUserOf(*relayStr, cred)+"@"+relayAddr,
			"enroll "+tunnelPubLine)
		if out, eerr := enrollCmd.CombinedOutput(); eerr != nil {
			os.Remove(credKey)
			return fmt.Errorf("enroll: %v: %s", eerr, strings.TrimSpace(string(out)))
		}
		os.Remove(credKey)
		fmt.Println("ENROLL     = tunnel pubkey bound to " + nodeName)
	}

	// 4) authorized_keys for THIS node's sshd: the controller management key
	nodeAK := filepath.Join(np.Keys, "authorized_keys")
	if cred.ManagerPubkey != "" && !fileContains(nodeAK, cred.ManagerPubkey) {
		line := "no-agent-forwarding,no-X11-forwarding " + cred.ManagerPubkey + "\n"
		if err := os.WriteFile(nodeAK, []byte(line), 0o600); err != nil {
			return err
		}
		if err := platform.HardenFile(nodeAK); err != nil {
			return err
		}
	}

	// 5) config
	cfg := config.Defaults()
	cfg.Node.Name = nodeName
	cfg.Node.Mode = *mode
	cfg.Node.Relay = config.RelayAddr{Address: relayAddr, Port: relayPort, User: relayUser}
	cfg.Node.SSHD.OpenSSHDir = np.OpenSSH
	cfg.Node.SSHD.HostKey = rel(np.Data, hostKey)
	cfg.Node.SSHD.AuthorizedKeys = rel(np.Data, nodeAK)
	cfg.Node.SSHD.Log = rel(np.Data, np.SSHDLog)
	cfg.Node.SSHD.PidFile = rel(np.Data, "state", "sshd.pid")
	// channel remotes come from the enrollment (ports baked into the credential)
	if err := applyEnrolledPorts(cfg, cred); err != nil {
		return err
	}
	cfgPath := np.Config
	if err := config.Save(cfgPath, cfg); err != nil {
		return err
	}

	// 6) identity (hardware facts)
	if err := writeIdentity(np.Identity); err != nil {
		return err
	}

	fmt.Println("MODE       = " + *mode)
	fmt.Println("CONFIG     = " + cfgPath)
	fmt.Println("SSH-EXE    = " + sshExe)
	fmt.Println("KEYS       = " + np.Keys)
	fmt.Println("TUNNEL-PUB = " + tunnelPubLine)
	if *mode == "persistent" {
		if err := autostartEnsure(np, nodeName); err != nil {
			return err
		}
		fmt.Println("AUTOSTART  = registered (user scope)")
	} else {
		fmt.Println("AUTOSTART  = none (temporary)")
		fmt.Println("RUN        = dialhome node run   (foreground; Ctrl+C tears everything down)")
	}
	return nil
}

func nodeRun(np nodePaths, args []string) error {
	cfg, err := config.Load(np.Config)
	if err != nil {
		return fmt.Errorf("run: %w (install first)", err)
	}
	lock, err := supervisor.AcquireInstanceLock(np.Data)
	if err != nil {
		return fmt.Errorf("another supervisor owns this node: %w", err)
	}
	defer lock.Release()

	// start local sshd as OUR child (-D: foreground process we can watch/kill)
	sshdd, err := startSSHDD(np, cfg)
	if err != nil {
		return err
	}
	defer func() {
		if sshdd != nil && sshdd.Process != nil {
			_ = sshdd.Process.Kill()
		}
	}()

	// tunnel supervisor context: SIGINT/TERM tears down children (temporary contract)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var tunnels []supervisor.Tunnel
	for _, ch := range cfg.Node.Channels {
		if !ch.Enabled {
			continue
		}
		tunnels = append(tunnels, supervisor.Tunnel{
			Name:      ch.Name,
			Remote:    ch.Remote,
			Local:     ch.Local,
			KeyPath:   filepath.Join(np.Data, "keys", "tunnel.key"),
			RelayAddr: cfg.Node.Relay.Address + ":" + itoaPort(cfg.Node.Relay.Port),
			RelayUser: cfg.Node.Relay.User,
		})
	}
	sshExe := "/usr/bin/ssh"
	if runtimeIsWindows() {
		sshExe = filepath.Join(cfg.Node.SSHD.OpenSSHDir, "OpenSSH-Win64", "ssh.exe")
	}
	knownHosts := filepath.Join(np.Data, "known_hosts")
	// remote ports live in cfg.Node.Channels; supervisor.Tunnel carries them
	for i := range tunnels {
		for _, ch := range cfg.Node.Channels {
			if ch.Name == tunnels[i].Name && ch.Enabled {
				tunnels[i].Remote = ch.Remote
				tunnels[i].Local = ch.Local
			}
		}
	}
	return supervisor.Run(ctx, sshExe, np.TunnelLog, knownHosts, cfg, tunnels)
}

func nodeStatus(np nodePaths) error {
	cfg, err := config.Load(np.Config)
	if err != nil {
		fmt.Println("CONFIG    = missing (not installed)")
		return nil
	}
	fmt.Println("NODE      = " + cfg.Node.Name)
	fmt.Println("MODE      = " + cfg.Node.Mode)
	for _, ch := range cfg.Node.Channels {
		state := "disabled"
		if ch.Enabled {
			if supervisor.ProbeRelay(ch.Remote, 2*time.Second) {
				state = "LISTENING on relay"
			} else {
				state = "tunnel down"
			}
		}
		fmt.Printf("CHANNEL   = %-12s %s -> %s  [%s]\n", ch.Name, ch.Local, ch.Remote, state)
	}
	return nil
}

func nodeStop(np nodePaths) error {
	// stop = kill our recorded pids; the supervisor exits when children die
	pidFile := filepath.Join(np.State, "sshd.pid")
	if raw, err := os.ReadFile(pidFile); err == nil {
		var pid int
		if _, e := fmt.Sscanf(strings.TrimSpace(string(raw)), "%d", &pid); e == nil && pid > 0 {
			if p, e := os.FindProcess(pid); e == nil {
				_ = p.Kill()
			}
		}
	}
	fmt.Println("stop: local sshd killed; tunnel supervisor exits on next tick")
	return nil
}

func nodeUninstall(np nodePaths) error {
	autostartRemove()
	fmt.Println("autostart removed; data kept at " + np.Data + " (delete manually to purge keys)")
	return nil
}

func nodeLogs(np nodePaths, args []string) error {
	name := "node"
	if len(args) > 0 {
		name = args[0]
	}
	raw, err := os.ReadFile(filepath.Join(np.Logs, name+".log"))
	if err != nil {
		return fmt.Errorf("no log %s: %w", name, err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	start := 0
	if len(lines) > 40 {
		start = len(lines) - 40
	}
	for _, l := range lines[start:] {
		fmt.Println(l)
	}
	return nil
}

// --- helpers -------------------------------------------------------------

func loadCredential(spec string) (*credentialDoc, error) {
	var raw []byte
	if strings.HasPrefix(spec, "@") {
		raw, _ = base64.StdEncoding.DecodeString(spec[1:])
	} else {
		var err error
		raw, err = os.ReadFile(spec)
		if err != nil {
			return nil, fmt.Errorf("credential: %w", err)
		}
	}
	var doc credentialDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("credential json: %w", err)
	}
	if doc.NodeName == "" || doc.PrivateKeyOpenSSHB64 == "" {
		return nil, fmt.Errorf("%w: credential missing node_name/private_key", errs.ErrUsage)
	}
	return &doc, nil
}

func parseRelay(s string, cred *credentialDoc) (addr string, port int, user string) {
	if s == "" {
		s = "user@10.101.54.192:18322"
	}
	var rest string
	user, rest, _ = strings.Cut(s, "@")
	host, portStr, _ := strings.Cut(rest, ":")
	p := 0
	for _, r := range portStr {
		p = p*10 + int(r-'0')
	}
	if cred.RelayAddress != "" {
		host = cred.RelayAddress
	}
	if cred.RelayPort > 0 {
		p = cred.RelayPort
	}
	if cred.RelayUser != "" {
		user = cred.RelayUser
	}
	if p == 0 {
		p = 18322
	}
	return host, p, user
}

func relayUserOf(s string, cred *credentialDoc) string {
	if cred.RelayUser != "" {
		return cred.RelayUser
	}
	if i := strings.Index(s, "@"); i >= 0 {
		return s[:i]
	}
	return "user"
}

// applyEnrolledPorts writes the relay-assigned ports into the channel config.
func applyEnrolledPorts(cfg *config.NodeConfig, cred *credentialDoc) error {
	for i := range cfg.Node.Channels {
		switch cfg.Node.Channels[i].Name {
		case "ssh":
			if cred.PortsSSH > 0 {
				cfg.Node.Channels[i].Remote = "127.0.0.1:" + itoaPort(cred.PortsSSH)
			}
		case "application":
			if cred.PortsApp > 0 {
				cfg.Node.Channels[i].Remote = "127.0.0.1:" + itoaPort(cred.PortsApp)
			}
		}
	}
	return nil
}

func ensureHostKey(sshdExe, hostKey string) error {
	if _, err := os.Stat(hostKey); err == nil {
		return nil
	}
	// ssh-keygen (not sshd -t): portable sshd -t refuses key generation arguments.
	keygen := sshdExe
	if i := strings.LastIndex(sshdExe, "sshd"); i >= 0 {
		keygen = sshdExe[:i] + "ssh-keygen"
	}
	if runtimeIsWindows() {
		// portable package ships ssh-keygen next to sshd
		if i := strings.LastIndex(sshdExe, "sshd"); i >= 0 {
			keygen = sshdExe[:i] + "ssh-keygen"
		}
	} else {
		keygen = "/usr/bin/ssh-keygen"
		if _, err := os.Stat(keygen); err != nil {
			keygen = "/bin/ssh-keygen"
		}
	}
	cmd := exec.Command(keygen, "-q", "-t", "ed25519", "-N", "", "-f", hostKey)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("host keygen: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return platform.HardenFile(hostKey)
}

// pubOf renders the public line via ssh-keygen -y (ssh.exe has no -y; it would
// treat the flag as a connection option and fail with an opaque error).
func pubOf(keyPath, sshExe string) string {
	keygen := sshExe
	if i := strings.LastIndex(sshExe, "ssh"); i >= 0 {
		keygen = sshExe[:i] + "ssh-keygen"
	}
	out, err := exec.Command(keygen, "-y", "-f", keyPath).Output()
	if err != nil {
		return "(unable to render public key)"
	}
	return strings.TrimSpace(string(out))
}

func fileContains(path, needle string) bool {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return strings.Contains(string(raw), needle)
}

func writeIdentity(path string) error {
	id, err := collectIdentity()
	if err != nil {
		return err
	}
	raw, _ := json.MarshalIndent(id, "", "  ")
	return os.WriteFile(path, append(raw, '\n'), 0o600)
}

// rel makes p relative to dataDir when possible (config stores portable paths).
func rel(dataDir string, parts ...string) string {
	full := filepath.Join(parts...)
	if r, err := filepath.Rel(dataDir, full); err == nil && !strings.HasPrefix(r, "..") {
		return filepath.ToSlash(r)
	}
	return filepath.ToSlash(full)
}

func collectIdentity() (identity.Facts, error) {
	return identity.Collect()
}

func itoaPort(i int) string {
	if i == 0 {
		return "0"
	}
	var b [12]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}

func runtimeIsWindows() bool {
	return os.PathSeparator == '\\'
}
