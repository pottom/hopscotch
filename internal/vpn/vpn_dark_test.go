package vpn

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pottom/hopscotch/internal/msgs"
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

// First dark session: the normal reconnect delay (a retry after it sometimes
// worked). Second in a row: a two-minute quiet period, because new sessions
// kept the gateway stuck. A manual reconnect skips it and starts over.
func TestDarkSessionsEnforceCooldown(t *testing.T) {
	c := newConnection(testConnConfig("dark-cooldown")) // reconnect delay 1 s
	var starts atomic.Int32
	stop := runWithAttempt(c, darkAttempt(c, &starts))
	defer stop()

	waitUntil(t, 4*time.Second, "a second session after the normal reconnect delay", func() bool { return starts.Load() >= 2 })
	waitUntil(t, 2*time.Second, "a two-minute cooldown after the second dark session", func() bool { return reconnectIn(c) > 100*time.Second })
	if got := starts.Load(); got != 2 {
		t.Fatalf("sessions started = %d, want 2 (no new session during the cooldown)", got)
	}
	if msg := c.Stats().LastError; !strings.Contains(msg, "no traffic") {
		t.Errorf("LastError = %q, want it to say the gateway returned no traffic", msg)
	}

	c.ForceReconnect()
	waitUntil(t, 2*time.Second, "a manual reconnect to start a session during the cooldown", func() bool { return starts.Load() >= 3 })
	waitUntil(t, 2*time.Second, "the normal delay again, since the reconnect reset the streak", func() bool {
		d := reconnectIn(c)
		return d > 0 && d < 5*time.Second
	})
}

// A session that connects in between means the dark streak is over.
func TestConnectedSessionResetsDarkStreak(t *testing.T) {
	c := newConnection(testConnConfig("dark-reset"))
	var starts atomic.Int32
	stop := runWithAttempt(c, func(context.Context) error {
		n := starts.Add(1)
		if n == 2 {
			c.setState(StateConnected)
			return errors.New("dropped after connecting")
		}
		c.lastAttemptDark.Store(true)
		return errors.New("gateway returned no traffic")
	})
	defer stop()

	waitUntil(t, 6*time.Second, "the third session", func() bool { return starts.Load() >= 3 })
	waitUntil(t, 2*time.Second, "a normal delay after the third (first in a row) dark session", func() bool {
		d := reconnectIn(c)
		return d > 0 && d < 5*time.Second
	})
	if got := c.darkStreak.Load(); got != 1 {
		t.Errorf("darkStreak = %d, want 1", got)
	}
}

// Session starts beyond the gate's allowance wait, show a progress message,
// and a reconnect request made meanwhile must not tear down the session that
// finally starts.
func TestSessionGateDelaysStartAndSwallowsReconnect(t *testing.T) {
	c := newConnection(testConnConfig("gated"))
	c.gate = newSessionGate()
	c.gate.window = 2 * time.Second
	c.gate.maxStarts = 1

	var starts atomic.Int32
	secondCancelled := make(chan bool, 1)
	stop := runWithAttempt(c, func(ctx context.Context) error {
		if starts.Add(1) == 1 {
			return errors.New("first attempt fails immediately")
		}
		select {
		case <-ctx.Done():
			secondCancelled <- true
		case <-time.After(500 * time.Millisecond):
			secondCancelled <- false
		}
		<-ctx.Done()
		return ctx.Err()
	})
	defer stop()

	waitUntil(t, 3*time.Second, "the second start to be held back by the gate", func() bool {
		return c.Stats().LastError == msgs.SessionRateLimited
	})
	if got := starts.Load(); got != 1 {
		t.Fatalf("sessions started while rate-limited = %d, want 1", got)
	}
	c.ForceReconnect()

	waitUntil(t, 3*time.Second, "the second session once the window rolled", func() bool { return starts.Load() >= 2 })
	if <-secondCancelled {
		t.Fatal("a reconnect requested during the wait tore down the session that started after it")
	}
}

func TestPauseDuringSessionWait(t *testing.T) {
	c := newConnection(testConnConfig("gated-pause"))
	c.gate = newSessionGate()
	c.gate.window = time.Minute
	c.gate.maxStarts = 1

	var starts atomic.Int32
	stop := runWithAttempt(c, func(context.Context) error {
		starts.Add(1)
		return errors.New("fails")
	})
	defer stop()

	waitUntil(t, 3*time.Second, "the gate to hold back the second start", func() bool {
		return c.Stats().LastError == msgs.SessionRateLimited
	})
	c.Pause()
	waitForVPNState(t, c, StatePaused, 2*time.Second)
	if got := starts.Load(); got != 1 {
		t.Errorf("sessions started = %d, want 1", got)
	}
	if !c.Stats().NextReconnectAt.IsZero() {
		t.Error("paused VPN still shows a countdown")
	}
}

func TestHoldDarkSessionFor(t *testing.T) {
	cfg := testConnConfig("hold")
	c := newConnection(cfg)
	c.darkStreak.Store(1)
	if got := c.holdDarkSessionFor(); got != 0 {
		t.Errorf("hold_dark_session off: hold %v, want 0", got)
	}

	cfg.HoldDarkSession = true
	c = newConnection(cfg)
	if got := c.holdDarkSessionFor(); got != 0 {
		t.Errorf("first dark session: hold %v, want 0 (no cooldown for the first)", got)
	}
	c.darkStreak.Store(1)
	if got := c.holdDarkSessionFor(); got != 2*time.Minute {
		t.Errorf("second dark session in a row: hold %v, want 2m", got)
	}
}
