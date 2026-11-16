// procman.go supervises the sing-box process for hs-node.
//
// Contract (mirrors the reference supervisor, NODE_MANUAL §8):
//   - crash -> restart via the shared retry ladder (5/10/20/40/60s, >=10
//     consecutive failures -> hourly; success resets)
//   - an auth-class failure (resume key rejected: the log shows
//     "Waiting for authentication" persisting or exit right after it)
//     backs off HARD to the hourly cadence
//   - graceful stop: SIGTERM, escalate to SIGKILL after StopGracePeriod
//   - single instance per node via a file lock (internal/lockfile)
package node

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/takomitkm/dialhome/internal/lockfile"
	"github.com/takomitkm/dialhome/internal/supervisor"
)

// StopGracePeriod is how long a SIGTERM'd sing-box gets before SIGKILL.
const StopGracePeriod = 15 * time.Second

// logTailMax bounds the in-memory tail kept for failure classification.
const logTailMax = 8192

// Manager owns one running sing-box process.
type Manager struct {
	BinPath string
	// ConfigFn is the sing-box config json path.
	ConfigFn string
	Log      *slog.Logger

	mu      sync.Mutex
	cmd     *exec.Cmd
	ladder  *supervisor.ChannelState
	spec    supervisor.RetrySpec
	stopped bool
	// tail keeps the last logTailMax bytes of child output for the auth hook.
	tailMu sync.Mutex
	tail   []byte
	// AuthFailureHook lets callers classify failures (e.g. scan the log for
	// rejected resume keys); default: not an auth failure.
	AuthFailureHook func() bool
}

// NewManager builds a Manager; the lock file lives in stateDir and must be
// held by the caller for the lifetime of the run (see Manager.Run).
func NewManager(binPath, configFn string, log *slog.Logger) *Manager {
	return &Manager{
		BinPath:  binPath,
		ConfigFn: configFn,
		Log:      log,
		ladder:   supervisor.NewChannel("sing-box"),
		spec:     supervisor.DefaultSpec(),
	}
}

// Run supervises sing-box until ctx is cancelled or the process manager is
// stopped. It restarts the child with the shared retry ladder semantics.
func (m *Manager) Run(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return nil // graceful stop requested
		}
		m.mu.Lock()
		if m.stopped {
			m.mu.Unlock()
			return nil
		}
		cmd := exec.Command(m.BinPath, "run", "-c", m.ConfigFn)
		cmd.Stdout = ioWriterFunc(func(p []byte) (int, error) {
			m.tailMu.Lock()
			m.tail = append(m.tail, p...)
			if len(m.tail) > logTailMax {
				m.tail = m.tail[len(m.tail)-logTailMax:]
			}
			m.tailMu.Unlock()
			m.Log.Info(strings.TrimRight(string(p), "\r\n"))
			return len(p), nil
		})
		cmd.Stderr = cmd.Stdout
		if runtime.GOOS == "windows" {
			hideWindow(cmd)
		}
		err := cmd.Start()
		if err != nil {
			m.mu.Unlock()
			m.Log.Error("start sing-box: " + err.Error())
			wait := m.ladder.NextDelayAfterFailure(m.spec, false)
			if !sleepCtx(ctx, wait) {
				return nil
			}
			continue
		}
		m.cmd = cmd
		m.mu.Unlock()

		// Wait for exit (or ctx cancel -> terminate).
		exitCh := make(chan error, 1)
		go func() { exitCh <- cmd.Wait() }()
		var exitErr error
		select {
		case exitErr = <-exitCh:
		case <-ctx.Done():
			m.stop()
			<-exitCh
			return nil
		}

		authFailure := m.AuthFailureHook != nil && m.AuthFailureHook()
		m.Log.Error(fmt.Sprintf("sing-box exited: %v (auth_failure=%v)", exitErr, authFailure))
		wait := m.ladder.NextDelayAfterFailure(m.spec, authFailure)
		m.Log.Info(fmt.Sprintf("restart in %s (fails=%d)", wait, m.ladder.Fails))
		if !sleepCtx(ctx, wait) {
			return nil
		}
	}
}

// stop terminates the current child: SIGTERM then SIGKILL after grace period.
func (m *Manager) stop() {
	m.mu.Lock()
	cmd := m.cmd
	m.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		_, _ = cmd.Process.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(StopGracePeriod):
		_ = cmd.Process.Kill()
		<-done
	}
}

// Stop asks the manager to stop the current child and quit Run.
func (m *Manager) Stop() {
	m.mu.Lock()
	m.stopped = true
	cmd := m.cmd
	m.mu.Unlock()
	if cmd != nil {
		m.stop()
	}
}

// LogTail returns the recent child output (bounded) for failure hooks.
func (m *Manager) LogTail() string {
	m.tailMu.Lock()
	defer m.tailMu.Unlock()
	return string(m.tail)
}

// Status renders the supervisor line for `hs-node status`.
func (m *Manager) Status() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	running := m.cmd != nil && m.cmd.Process != nil
	state := "stopped"
	if running {
		state = "running"
	}
	return state + " " + m.ladder.Describe()
}

// AcquireInstanceLock takes the single-instance lock for a node data dir.
// Non-blocking: returns lockfile.ErrLocked when another supervisor already
// owns the node (the caller prints that and exits, matching the reference's
// "another supervisor owns this node" behavior). Caller must Release().
func AcquireInstanceLock(stateDir string) (*lockfile.Lock, error) {
	return lockfile.TryAcquire(stateDir + string(os.PathSeparator) + "hs-node.lock")
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// ioWriterFunc adapts a func to an io.Writer without a named type elsewhere.
type ioWriterFunc func(p []byte) (int, error)

func (f ioWriterFunc) Write(p []byte) (int, error) { return f(p) }
