package node

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/takomitkm/dialhome/internal/supervisor"
)

// N-pm-1: the ladder contract is exact -- a naive implementation (e.g.
// constant 5s, or forgetting the hourly jump) fails any line of this test.
func TestLadderSequence(t *testing.T) {
	spec := supervisor.DefaultSpec()
	st := supervisor.NewChannel("test")
	var got []time.Duration
	for i := 0; i < 12; i++ {
		got = append(got, st.NextDelayAfterFailure(spec, false))
	}
	want := []time.Duration{
		5 * time.Second, 10 * time.Second, 20 * time.Second,
		40 * time.Second, 60 * time.Second, 60 * time.Second,
		60 * time.Second, 60 * time.Second, 60 * time.Second,
		60 * time.Second,   // 10th consecutive failure ...
		3600 * time.Second, // ... >= FailsToHourly -> hourly
		3600 * time.Second,
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("failure %d: want %v got %v (full=%v)", i+1, want[i], got[i], got)
		}
	}
}

// N-pm-2: auth-class failures jump straight to the hourly cadence (the
// per-source penalty lesson from the reference implementation).
func TestLadderAuthFailureJumpsHourly(t *testing.T) {
	spec := supervisor.DefaultSpec()
	st := supervisor.NewChannel("test")
	d := st.NextDelayAfterFailure(spec, true)
	if d != 3600*time.Second {
		t.Fatalf("auth failure wait = %v, want 1h", d)
	}
}

// N-pm-3: success resets the ladder (a flappy link must not park at 3600s).
func TestLadderSuccessResets(t *testing.T) {
	spec := supervisor.DefaultSpec()
	st := supervisor.NewChannel("test")
	for i := 0; i < 9; i++ {
		st.NextDelayAfterFailure(spec, false)
	}
	st.MarkConnected(time.Now())
	d := st.NextDelayAfterFailure(spec, false)
	if d != 5*time.Second {
		t.Fatalf("after success, wait = %v, want 5s", d)
	}
}

// N-pm-4: the instance lock is exclusive -- a second acquirer must be refused.
// (Calibration: this test ran RED when AcquireInstanceLock pointed at a
// non-locking file -- see analysis/CALIBRATION.md.)
func TestInstanceLockExclusive(t *testing.T) {
	dir := t.TempDir()
	l1, err := AcquireInstanceLock(dir)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer l1.Release()
	l2, err := AcquireInstanceLock(dir)
	if err == nil {
		l2.Release()
		t.Fatal("second instance lock acquired -- single-instance invariant broken")
	}
}

// N-pm-5: releasing the lock allows re-acquisition (restart path).
func TestInstanceLockReleasable(t *testing.T) {
	dir := t.TempDir()
	l1, err := AcquireInstanceLock(dir)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if err := l1.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	l2, err := AcquireInstanceLock(dir)
	if err != nil {
		t.Fatalf("re-acquire after release: %v", err)
	}
	l2.Release()
}

// N-pm-6: Manager.Status never panics before a child has started.
func TestManagerStatusBeforeStart(t *testing.T) {
	m := NewManager("definitely-not-a-real-binary", filepath.Join(t.TempDir(), "c.json"), nil)
	s := m.Status()
	if s == "" {
		t.Fatal("empty status")
	}
}
