package supervisor

import (
	"testing"
	"time"
)

func spec() RetrySpec {
	return RetrySpec{BaseSeconds: 5, MaxSeconds: 60, FailsToHourly: 10, HourlySeconds: 3600, BootWaitMax: 10 * time.Minute}
}

func TestLadderDoubling(t *testing.T) {
	c := NewChannel("ssh")
	s := spec()
	var got []int
	for i := 0; i < 4; i++ {
		d := c.NextDelayAfterFailure(s, false)
		got = append(got, int(d.Seconds()))
	}
	// 5 -> 10 -> 20 -> 40
	want := []int{5, 10, 20, 40}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ladder step %d: got %ds want %ds", i, got[i], want[i])
		}
	}
	// fifth failure caps at 60
	d := c.NextDelayAfterFailure(s, false)
	if int(d.Seconds()) != 60 {
		t.Fatalf("fifth failure: got %ds want 60", int(d.Seconds()))
	}
}

func TestHourlyAfterTenFailures(t *testing.T) {
	c := NewChannel("ssh")
	s := spec()
	for i := 0; i < s.FailsToHourly-1; i++ {
		c.NextDelayAfterFailure(s, false)
	}
	d := c.NextDelayAfterFailure(s, false) // 10th failure
	if int(d.Seconds()) != 3600 {
		t.Fatalf("10th failure: got %ds want 3600", int(d.Seconds()))
	}
}

func TestAuthFailureJumpsToHourly(t *testing.T) {
	c := NewChannel("ssh")
	s := spec()
	d := c.NextDelayAfterFailure(s, true)
	if int(d.Seconds()) != 3600 {
		t.Fatalf("auth failure: got %ds want 3600 (per-source penalty)", int(d.Seconds()))
	}
}

func TestSuccessResets(t *testing.T) {
	c := NewChannel("ssh")
	s := spec()
	c.NextDelayAfterFailure(s, false)
	c.NextDelayAfterFailure(s, false)
	c.MarkConnected(time.Now())
	if c.Fails != 0 || c.Wait != 0 || !c.Connected {
		t.Fatalf("reset failed: %+v", c)
	}
	d := c.NextDelayAfterFailure(s, false)
	if int(d.Seconds()) != 5 {
		t.Fatalf("post-reset first wait: got %ds want 5", int(d.Seconds()))
	}
}

func TestBootWaitCycle(t *testing.T) {
	c := NewChannel("ssh")
	s := spec()
	// network down: 30s probes until ~10min budget is spent
	total := time.Duration(0)
	for i := 0; i < 100; i++ {
		d, cont := c.BootWait(s, false)
		if !cont {
			break
		}
		total += d
	}
	if total < 9*time.Minute || total > 10*time.Minute+30*time.Second {
		t.Fatalf("boot wait budget: got %v", total)
	}
	// network up: probe passes immediately
	d, cont := c.BootWait(s, true)
	if d != 0 || !cont {
		t.Fatalf("healthy probe must pass instantly, got %v %v", d, cont)
	}
}
