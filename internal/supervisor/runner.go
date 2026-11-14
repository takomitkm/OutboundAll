// runner.go -- the actual child management: one ssh per channel, independent
// processes, single-instance guard, foreground (temporary) or background (persistent).
package supervisor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/takomitkm/dialhome/internal/config"
	"github.com/takomitkm/dialhome/internal/lockfile"
	"github.com/takomitkm/dialhome/internal/logging"
	"github.com/takomitkm/dialhome/internal/platform"
)

// Tunnel describes one reverse-forward channel as the ssh child must see it.
// The 127.0.0.1 literals are constructed HERE and are not configurable text:
// the relay's permitlisten matches them exactly (PITFALLS B1).
type Tunnel struct {
	Name      string
	Remote    string // 127.0.0.1:<relayPort>
	Local     string // 127.0.0.1:<nodePort>
	KeyPath   string
	RelayAddr string // host:port
	RelayUser string
}

// BuildArgs renders the ssh argument vector (exec.Command style: no shell).
func (t Tunnel) BuildArgs(knownHosts string, keepalive config.Keepalive, timeout int) []string {
	return []string{
		"-N",
		"-R", t.Remote + ":" + t.Local,
		"-p", portOf(t.RelayAddr),
		"-i", t.KeyPath,
		"-o", "BatchMode=yes",
		"-o", "ExitOnForwardFailure=yes",
		"-o", fmt.Sprintf("ConnectTimeout=%d", timeout),
		"-o", fmt.Sprintf("ServerAliveInterval=%d", keepalive.IntervalSeconds),
		"-o", fmt.Sprintf("ServerAliveCountMax=%d", keepalive.CountMax),
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "UserKnownHostsFile=" + knownHosts,
		userAtHost(t.RelayUser, t.RelayAddr),
	}
}

func portOf(addr string) string {
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		return addr[i+1:]
	}
	return "22"
}

func userAtHost(user, addr string) string {
	host := addr
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		host = addr[:i]
	}
	return user + "@" + host
}

// AcquireInstanceLock guards against two supervisors managing the same node
// (spec §3.4 "防止两个 Supervisor 同时管理同一个 Node").
func AcquireInstanceLock(dataDir string) (*lockfile.Lock, error) {
	return lockfile.Acquire(filepath.Join(dataDir, "state", "supervisor.lock"))
}

// ProbeRelay returns true when the relay's SSH port answers a TCP connect.
func ProbeRelay(addr string, timeout time.Duration) bool {
	d := dialTimeout(addr, timeout)
	if d == nil {
		return false
	}
	defer d.Close()
	return true
}

// Run executes the supervisor loop for the given channels until ctx is cancelled.
// sshBinary is the portable ssh.exe path; logDir receives per-channel logs.
func Run(ctx context.Context, sshBinary, logDir, knownHosts string, cfg *config.NodeConfig, tunnels []Tunnel) error {
	if len(tunnels) == 0 {
		return config.ErrSSHChannelNeeded
	}
	spec := RetrySpec{
		BaseSeconds:   cfg.Supervisor.Retry.BaseSeconds,
		MaxSeconds:    cfg.Supervisor.Retry.MaxSeconds,
		FailsToHourly: cfg.Supervisor.Retry.FailsToHourly,
		HourlySeconds: cfg.Supervisor.Retry.HourlySeconds,
		BootWaitMax:   time.Duration(cfg.Supervisor.BootWaitMaxSeconds) * time.Second,
	}
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return err
	}

	// boot-wait phase: probe the relay once (shared across channels -- the relay is
	// one host); up to BootWaitMax, 30s between probes (baseline behavior)
	probeDeadline := time.Now().Add(spec.BootWaitMax)
	for {
		if ProbeRelay(tunnels[0].RelayAddr, time.Duration(cfg.Supervisor.ConnectTimeout)*time.Second) {
			break
		}
		if time.Now().After(probeDeadline) {
			break // enter the ladder anyway; it will keep trying hourly
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(30 * time.Second):
		}
	}

	states := map[string]*ChannelState{}
	for _, t := range tunnels {
		states[t.Name] = NewChannel(t.Name)
	}
	procs := map[string]*exec.Cmd{}
	cancelled := map[string]bool{}

	for _, t := range tunnels { // per-channel loggers
		_, closer, err := logging.New(logging.Options{Dir: logDir, Name: "tunnel-" + t.Name})
		if err != nil {
			return err
		}
		defer closer.Close()
	}

	for {
		if ctx.Err() != nil {
			stopAll(procs, cancelled)
			return ctx.Err()
		}
		for _, t := range tunnels {
			st := states[t.Name]
			ch := channelByName(cfg, t.Name)
			if ch == nil || !ch.Enabled {
				continue
			}
			if p, ok := procs[t.Name]; ok && p.Process != nil {
				if p.ProcessState == nil {
					continue // still running
				}
			}
			// (re)start this channel
			logPath := filepath.Join(logDir, "tunnel-"+t.Name+".err")
			args := t.BuildArgs(knownHosts, cfg.Supervisor.Keepalive, cfg.Supervisor.ConnectTimeout)
			cmd := exec.Command(sshBinary, args...)
			errFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
			if err != nil {
				st.LastError = err.Error()
				continue
			}
			cmd.Stderr = errFile
			cmd.Stdout = nil
			hideWindow(cmd)
			startErr := cmd.Start()
			errFile.Close()
			if startErr != nil {
				wait := st.NextDelayAfterFailure(spec, false)
				st.LastError = startErr.Error()
				sleepCtx(ctx, wait)
				continue
			}
			procs[t.Name] = cmd

			// ExitOnForwardFailure: a quick exit means the relay refused the forward
			// (or auth failed). Distinguish by reading the tail of the err file.
			time.Sleep(1500 * time.Millisecond)
			if cmd.ProcessState != nil {
				// exited immediately: auth failure gets the hourly cadence
				// (OpenSSH 10 per-source penalty), anything else follows the ladder
				authFailure := strings.Contains(readTail(logPath, 400), "Permission denied")
				wait := st.NextDelayAfterFailure(spec, authFailure)
				st.LastError = readTail(logPath, 200)
				delete(procs, t.Name)
				sleepCtx(ctx, wait)
			} else {
				st.MarkConnected(time.Now())
				st.LastError = ""
			}
		}
		sleepCtx(ctx, 10*time.Second)
	}
}

func channelByName(cfg *config.NodeConfig, name string) *config.Channel {
	for i := range cfg.Node.Channels {
		if cfg.Node.Channels[i].Name == name {
			return &cfg.Node.Channels[i]
		}
	}
	return nil
}

func readTail(path string, n int) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	s := string(raw)
	if len(s) > n {
		s = s[len(s)-n:]
	}
	return strings.TrimSpace(s)
}

func stopAll(procs map[string]*exec.Cmd, cancelled map[string]bool) {
	for name, p := range procs {
		if p.Process != nil {
			_ = p.Process.Kill()
			cancelled[name] = true
		}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// hideWindow is declared per-platform (hide_windows.go / hide_other.go).

// HardenKeys hardens every key file under keys/ (platform-specific).
func HardenKeys(dataDir string) error {
	keysDir := filepath.Join(dataDir, "keys")
	entries, err := os.ReadDir(keysDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if err := platform.HardenFile(filepath.Join(keysDir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}
