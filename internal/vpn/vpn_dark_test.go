package vpn

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// runWithAttempt runs c with its connection attempts replaced by attempt and
// returns a func that stops Run and waits for it to return.
func runWithAttempt(c *Connection, attempt func(ctx context.Context) error) (stop func()) {
	c.attempt = attempt
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		c.Run(ctx)
		close(done)
	}()
	return func() {
		cancel()
		<-done
	}
}

func waitUntil(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %v waiting for %s", timeout, what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func reconnectIn(c *Connection) time.Duration {
	at := c.Stats().NextReconnectAt
	if at.IsZero() {
		return 0
	}
	return time.Until(at)
}

// darkAttempt ends every session as dark, counting starts.
func darkAttempt(c *Connection, starts *atomic.Int32) func(context.Context) error {
	return func(context.Context) error {
		starts.Add(1)
		c.lastAttemptDark.Store(true)
		return errors.New("gateway returned no traffic")
	}
}

// With no history the wait after a dark session is the default, the reason
// says so, and every attempt lands in the history with its context.
func TestDarkSessionUsesPolicyAndIsRecorded(t *testing.T) {
	c := newConnection(testConnConfig("dark-policy")) // reconnect delay 1 s: must not be used for dark sessions
	c.history = newSessionHistory("")
	c.rnd = never
	var starts atomic.Int32
	stop := runWithAttempt(c, darkAttempt(c, &starts))
	defer stop()

	waitUntil(t, 2*time.Second, "the wait after the first dark session", func() bool { return reconnectIn(c) > 0 })
	if d := reconnectIn(c); d < 10*time.Second || d > darkRetryDefault {
		t.Errorf("wait after a dark session = %v, want the %v default, not the 1 s reconnect delay", d, darkRetryDefault)
	}
	if msg := c.Stats().LastError; !strings.Contains(msg, "no traffic") || !strings.Contains(msg, "learning") {
		t.Errorf("LastError = %q, want the dark message with the policy's reason", msg)
	}

	c.ForceReconnect()
	waitUntil(t, 2*time.Second, "a manual reconnect to start the second session", func() bool { return starts.Load() >= 2 })
	waitUntil(t, 2*time.Second, "the second session to be recorded", func() bool { return len(c.history.snapshot()) >= 2 })

	recs := c.history.snapshot()
	if recs[0].Outcome != OutcomeDark || recs[0].PrevDark || recs[0].SinceOwn != -1 {
		t.Errorf("first record = %+v, want dark with no previous attempt", recs[0])
	}
	if recs[1].Outcome != OutcomeDark || !recs[1].PrevDark || recs[1].SinceOwn < 0 || recs[1].SinceOwn > 2 {
		t.Errorf("second record = %+v, want dark, after a dark attempt, retried within ~2 s", recs[1])
	}
}

// When the history says quick retries don't work but longer waits do, the VPN
// waits longer.
func TestDarkSessionFollowsLearnedHistory(t *testing.T) {
	c := newConnection(testConnConfig("dark-learned"))
	c.history = newSessionHistory("")
	c.rnd = never
	now := time.Now()
	for _, r := range append(retryAfterDark("dark-learned", 15*time.Second, OutcomeDark, 10, now),
		retryAfterDark("dark-learned", 2*time.Minute, OutcomeOK, 9, now)...) {
		c.history.add(r)
	}
	var starts atomic.Int32
	stop := runWithAttempt(c, darkAttempt(c, &starts))
	defer stop()

	waitUntil(t, 2*time.Second, "a two-minute wait learned from the history", func() bool { return reconnectIn(c) > 100*time.Second })
	if msg := c.Stats().LastError; !strings.Contains(msg, "9/9") {
		t.Errorf("LastError = %q, want the evidence for the chosen wait", msg)
	}
}

// A session that connects in between means the dark streak is over.
func TestConnectedSessionResetsDarkStreak(t *testing.T) {
	c := newConnection(testConnConfig("dark-reset"))
	c.rnd = never
	c.darkStreak.Store(5)
	var starts atomic.Int32
	stop := runWithAttempt(c, func(context.Context) error {
		if starts.Add(1) == 1 {
			c.setState(StateConnected)
			return errors.New("dropped after connecting")
		}
		c.lastAttemptDark.Store(true)
		return errors.New("gateway returned no traffic")
	})
	defer stop()

	waitUntil(t, 4*time.Second, "the dark session after the connected one", func() bool {
		return strings.Contains(c.Stats().LastError, "(1 session(s) in a row)")
	})
	if got := c.darkStreak.Load(); got != 1 {
		t.Errorf("darkStreak = %d, want 1", got)
	}
}

func TestSessionRecordOutcomes(t *testing.T) {
	c := newConnection(testConnConfig("record"))
	c.history = newSessionHistory("")
	var starts atomic.Int32
	stop := runWithAttempt(c, func(ctx context.Context) error {
		if starts.Add(1) == 1 {
			c.sessionIP.Store("10.4.1.126")
			c.setState(StateConnected)
			return errors.New("dropped after connecting")
		}
		<-ctx.Done() // second attempt is cut by a pause
		return ctx.Err()
	})
	defer stop()

	waitUntil(t, 4*time.Second, "the second attempt", func() bool { return starts.Load() >= 2 })
	c.Pause()
	waitForVPNState(t, c, StatePaused, 2*time.Second)

	recs := c.history.snapshot()
	if len(recs) != 2 {
		t.Fatalf("records = %+v, want 2", recs)
	}
	if recs[0].Outcome != OutcomeOK || recs[0].IP != "10.4.1.126" || recs[0].ConnectedAfter < 0 {
		t.Errorf("first record = %+v, want ok with the assigned IP", recs[0])
	}
	if recs[1].Outcome != OutcomeCut {
		t.Errorf("second record outcome = %s, want cut", recs[1].Outcome)
	}
}

func TestHoldDarkSessionFor(t *testing.T) {
	cfg := testConnConfig("hold")
	c := newConnection(cfg)
	c.darkStreak.Store(darkRetryFloorStreak)
	if got := c.holdDarkSessionFor(); got != 0 {
		t.Errorf("hold_dark_session off: hold %v, want 0", got)
	}

	cfg.HoldDarkSession = true
	c = newConnection(cfg)
	c.darkStreak.Store(1)
	if got := c.holdDarkSessionFor(); got != 0 {
		t.Errorf("short streak: hold %v, want 0 (quick retries, nothing to hold)", got)
	}
	c.darkStreak.Store(darkRetryFloorStreak - 1)
	if got := c.holdDarkSessionFor(); got != darkRetryFloor {
		t.Errorf("session reaching the streak floor: hold %v, want %v", got, darkRetryFloor)
	}
}
