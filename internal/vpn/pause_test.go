package vpn

import (
	"context"
	"testing"
	"time"
)

// testConnConfig returns a connConfig pointing at a binary that doesn't
// exist, so any connect attempt fails fast without spawning a real
// subprocess — enough to exercise the pause/resume state machine in
// isolation.
func testConnConfig(name string) connConfig {
	return connConfig{
		Name:              name,
		Binary:            "definitely-not-a-real-openconnect-binary",
		ReconnectDelay:    1,
		ReconnectMaxDelay: 1,
	}
}

func waitForVPNState(t *testing.T, c *Connection, want State, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		if got := c.State(); got == want {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("state did not reach %v within %v (last state %v)", want, timeout, c.State())
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// TestStateString covers the new StatePaused case.
func TestStateString(t *testing.T) {
	tests := []struct {
		s    State
		want string
	}{
		{StateConnected, "connected"},
		{StateConnecting, "connecting"},
		{StateDisconnected, "disconnected"},
		{StatePaused, "paused"},
	}
	for _, tc := range tests {
		if got := tc.s.String(); got != tc.want {
			t.Errorf("State(%d).String() = %q, want %q", tc.s, got, tc.want)
		}
	}
}

// TestVPNPauseBeforeRunSkipsConnect verifies Pause() called before Run()
// starts puts the connection straight into StatePaused without ever
// attempting to spawn the subprocess.
func TestVPNPauseBeforeRunSkipsConnect(t *testing.T) {
	conn := newConnection(testConnConfig("pause-before-run"))
	conn.Pause()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		conn.Run(ctx)
		close(done)
	}()
	defer func() {
		cancel()
		<-done
	}()

	waitForVPNState(t, conn, StatePaused, 2*time.Second)
}

// TestVPNResumeAfterPause verifies Resume() clears the paused state and lets
// the reconnect loop proceed again (it will fail fast since the configured
// binary doesn't exist, but it must leave StatePaused).
func TestVPNResumeAfterPause(t *testing.T) {
	conn := newConnection(testConnConfig("resume"))
	conn.Pause()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		conn.Run(ctx)
		close(done)
	}()
	defer func() {
		cancel()
		<-done
	}()

	waitForVPNState(t, conn, StatePaused, 2*time.Second)

	conn.Resume()

	deadline := time.After(2 * time.Second)
	for conn.State() == StatePaused {
		select {
		case <-deadline:
			t.Fatal("connection stayed in StatePaused after Resume()")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// TestVPNPauseDuringConnectingInterruptsRun verifies Pause() interrupts an
// active runOnce attempt (reusing the same cancelRun teardown path as
// ForceReconnect) and transitions to StatePaused without waiting for the
// normal reconnect backoff.
func TestVPNPauseDuringConnectingInterruptsRun(t *testing.T) {
	conn := newConnection(testConnConfig("pause-during-connect"))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		conn.Run(ctx)
		close(done)
	}()
	defer func() {
		cancel()
		<-done
	}()

	waitForVPNState(t, conn, StateConnecting, 2*time.Second)

	conn.Pause()

	waitForVPNState(t, conn, StatePaused, 2*time.Second)
}
