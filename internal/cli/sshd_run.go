// sshd_run.go -- start the local sshd as a supervised child (`sshd -D`).
package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/takomitkm/outboundall/internal/config"
)

// startSSHDD launches the node's local sshd in daemon-attached foreground mode
// (-D) so the Go supervisor owns its lifecycle (PHASE4 evidence: a daemonized sshd
// survives the session anyway, but -D gives us crash detection and clean teardown).
// Only 127.0.0.1 and key auth are configured by install; the config test runs first.
func startSSHDD(np nodePaths, cfg *config.NodeConfig) (*exec.Cmd, error) {
	sshdExe := "/usr/sbin/sshd"
	if runtimeIsWindows() {
		sshdExe = filepath.Join(cfg.Node.SSHD.OpenSSHDir, "OpenSSH-Win64", "sshd.exe")
	}
	cfgPath := filepath.Join(np.Data, "sshd_config")
	if _, err := os.Stat(cfgPath); err != nil {
		// synthesize the runtime config from our own config (rootless, loopback-only)
		hostKey := filepath.Join(np.Data, cfg.Node.SSHD.HostKey)
		ak := filepath.Join(np.Data, cfg.Node.SSHD.AuthorizedKeys)
		port := localPortOf(cfg)
		lines := []string{
			"Port " + port,
			"ListenAddress 127.0.0.1",
			"HostKey " + filepath.ToSlash(hostKey),
			"PidFile " + filepath.ToSlash(filepath.Join(np.Data, cfg.Node.SSHD.PidFile)),
			"AuthorizedKeysFile " + filepath.ToSlash(ak),
			"PubkeyAuthentication yes",
			"PasswordAuthentication no",
			"PermitEmptyPasswords no",
			"PermitRootLogin no",
			"StrictModes no",
			"MaxAuthTries 3",
			"X11Forwarding no",
			"AllowAgentForwarding no",
			"PermitTunnel no",
			"AllowUsers " + currentUser(),
			"LogLevel INFO",
		}
		if runtime.GOOS != "windows" {
			lines = append(lines, "UsePAM no", "Subsystem sftp internal-sftp")
		} else {
			lines = append(lines, "Subsystem sftp "+filepath.ToSlash(filepath.Join(cfg.Node.SSHD.OpenSSHDir, "OpenSSH-Win64", "sftp-server.exe")))
		}
		body := ""
		for _, l := range lines {
			body += l + "\n"
		}
		if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
			return nil, err
		}
	}
	// config test
	if out, err := exec.Command(sshdExe, "-t", "-f", cfgPath).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("sshd config test failed: %v: %s", err, string(out))
	}
	logPath := np.SSHDLog
	_ = os.MkdirAll(filepath.Dir(logPath), 0o700)
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(sshdExe, "-D", "-f", cfgPath, "-E", logPath)
	cmd.Stdout = nil
	cmd.Stderr = logFile
	hideWindow(cmd)
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return nil, err
	}
	// record pid for `node stop`
	if cmd.Process != nil {
		_ = os.WriteFile(filepath.Join(np.State, "sshd.pid"), []byte(fmt.Sprint(cmd.Process.Pid)), 0o600)
	}
	// detach the log handle: the child owns its lifetime
	go func() { _ = cmd.Wait() }()
	return cmd, nil
}

func localPortOf(cfg *config.NodeConfig) string {
	// ListenAddress port comes from the ssh channel's LOCAL side
	local := "19022"
	for _, ch := range cfg.Node.Channels {
		if ch.Name == "ssh" {
			if i := len(ch.Local) - 1; i > 0 {
				for j := len(ch.Local) - 1; j >= 0; j-- {
					if ch.Local[j] == ':' {
						local = ch.Local[j+1:]
						break
					}
				}
			}
		}
	}
	return local
}

func currentUser() string {
	if u := os.Getenv("USERNAME"); u != "" {
		return u
	}
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return "nobody"
}

var _ = runtime.GOOS
