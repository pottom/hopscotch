package vpn

import (
	"context"
	"testing"
	"time"
)

func TestVPNAutoPauseAfterConsecutiveFailures(t *testing.T) {
	cfg := testConnConfig("autopause")
	cfg.AutoPauseThreshold = 2
	conn := newConnection(cfg)

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

	waitForVPNState(t, conn, StatePaused, 5*time.Second)

	st := conn.Stats()
	if st.ConsecutiveFailures < 2 {
		t.Errorf("ConsecutiveFailures = %d, want >= 2 (the configured threshold)", st.ConsecutiveFailures)
	}
	if !st.AutoPaused {
		t.Error("AutoPaused = false, want true (this pause was triggered by the threshold, not a manual Pause())")
	}
}

func TestVPNManualPauseIsNotMarkedAuto(t *testing.T) {
	cfg := testConnConfig("manual-pause-not-auto")
	cfg.AutoPauseThreshold = 2
	conn := newConnection(cfg)
	conn.Pause()

	if st := conn.Stats(); st.AutoPaused {
		t.Error("AutoPaused = true after a manual Pause(), want false")
	}
}

func TestVPNAutoPauseResumeResetsCounter(t *testing.T) {
	cfg := testConnConfig("autopause-resume")
	cfg.AutoPauseThreshold = 2
	conn := newConnection(cfg)

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

	waitForVPNState(t, conn, StatePaused, 5*time.Second)

	conn.Resume()

	// Resume() resets the counter (and the auto-pause flag) synchronously, so
	// this holds immediately with no timing dependency on the reconnect loop.
	st := conn.Stats()
	if st.ConsecutiveFailures != 0 {
		t.Errorf("ConsecutiveFailures after Resume() = %d, want 0", st.ConsecutiveFailures)
	}
	if st.AutoPaused {
		t.Error("AutoPaused after Resume() = true, want false")
	}
}

func TestVPNAutoPauseDisabledByDefault(t *testing.T) {
	cfg := testConnConfig("autopause-disabled")
	// AutoPauseThreshold left at zero value (disabled).
	conn := newConnection(cfg)

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

	deadline := time.After(3 * time.Second)
	for {
		select {
		case <-deadline:
			return // success: never paused
		case <-time.After(50 * time.Millisecond):
			if conn.State() == StatePaused {
				t.Fatal("vpn auto-paused despite auto_pause_threshold being unset (0)")
			}
		}
	}
}
