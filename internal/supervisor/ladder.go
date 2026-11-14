// Package supervisor runs one tunnel child per channel and owns the retry ladder.
//
// Behavior contract (docs/REFACTOR_BASELINE.md §3.3, spec §3.4):
//   - boot-wait: right after a reboot the network may not be up; probe the relay for
//     up to BootWaitMaxSeconds before entering the retry ladder
//   - retry ladder: 5 -> 10 -> 20 -> 40 -> 60s (doubling, capped at 60);
//     after FailsToHourly consecutive failures the cadence becomes HourlySeconds
//   - success resets the failure counter
//   - auth failures (OpenSSH 10 per-source penalty, PHASE4_ACCEPTANCE §4) must back
//     off HARD: they jump straight to the hourly cadence
//   - channels are independent: one channel's failure never touches another
package supervisor

import (
	"time"
)

// RetrySpec is the tuning knobs (mirrors config.Supervisor).
type RetrySpec struct {
	BaseSeconds   int
	MaxSeconds    int
	FailsToHourly int
	HourlySeconds int
	BootWaitMax   time.Duration
}

// DefaultSpec is the documented default ladder.
func DefaultSpec() RetrySpec {
	return RetrySpec{
		BaseSeconds:   5,
		MaxSeconds:    60,
		FailsToHourly: 10,
		HourlySeconds: 3600,
		BootWaitMax:   10 * time.Minute,
	}
}

// ChannelState tracks one channel's progress through the ladder.
type ChannelState struct {
	Name         string
	Fails        int
	Wait         time.Duration
	Connected    bool
	ConnectedAt  time.Time
	LastError    string
	bootAttempts int
}

// NewChannel creates the initial state.
func NewChannel(name string) *ChannelState {
	return &ChannelState{Name: name}
}

// NextDelayAfterFailure computes the wait before the next attempt.
// authFailure marks a rejected KEY (not a network problem): jump to the hourly
// cadence immediately because retrying quickly only deepens the OpenSSH 10
// per-source penalty (PHASE4_ACCEPTANCE §4).
func (c *ChannelState) NextDelayAfterFailure(spec RetrySpec, authFailure bool) time.Duration {
	c.Fails++
	c.Connected = false
	if authFailure || c.Fails >= spec.FailsToHourly {
		c.Wait = time.Duration(spec.HourlySeconds) * time.Second
		return c.Wait
	}
	// 5 -> 10 -> 20 -> 40 -> 60 (double, capped); all math in seconds
	if c.Wait == 0 {
		c.Wait = time.Duration(spec.BaseSeconds) * time.Second
	} else {
		nextSec := int(c.Wait.Seconds()) * 2
		if nextSec > spec.MaxSeconds {
			nextSec = spec.MaxSeconds
		}
		c.Wait = time.Duration(nextSec) * time.Second
	}
	return c.Wait
}

// MarkConnected resets the ladder.
func (c *ChannelState) MarkConnected(now time.Time) {
	c.Fails = 0
	c.Wait = 0
	c.Connected = true
	c.ConnectedAt = now
	c.bootAttempts = 0
}

// BootWait returns how long to sleep during the post-reboot network probe and
// whether probing is still allowed. Probes happen every 30s, max ~10 min total
// (baseline: 20 x 30s), then the normal ladder takes over.
func (c *ChannelState) BootWait(spec RetrySpec, probeOK bool) (time.Duration, bool) {
	if probeOK {
		c.bootAttempts = 0
		return 0, true
	}
	c.bootAttempts++
	if time.Duration(c.bootAttempts)*30*time.Second >= spec.BootWaitMax {
		return 0, false
	}
	return 30 * time.Second, true
}

// Describe renders the state line written to tunnel-<name>.log.
func (c *ChannelState) Describe() string {
	state := "disconnected"
	if c.Connected {
		state = "connected"
	}
	return state + " fails=" + itoa(c.Fails)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		b[pos] = '-'
	}
	return string(b[pos:])
}
