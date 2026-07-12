package vpn

import (
	"context"
	"sync"
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

func TestVPNAutoResumeAfterCooldown(t *testing.T) {
	cfg := testConnConfig("autoresume")
	cfg.AutoPauseThreshold = 2
	cfg.AutoResumeAfter = 1
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
	if !conn.Stats().AutoPaused {
		t.Fatal("expected an auto-pause before testing auto-resume")
	}

	deadline := time.After(5 * time.Second)
	for {
		if !conn.Stats().AutoPaused {
			return
		}
		select {
		case <-deadline:
			t.Fatal("vpn never auto-resumed after the cooldown")
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func TestVPNAutoResumeDisabledByDefault(t *testing.T) {
	cfg := testConnConfig("autoresume-disabled")
	cfg.AutoPauseThreshold = 2
	// AutoResumeAfter left at zero value (disabled).
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

	deadline := time.After(2 * time.Second)
	for {
		select {
		case <-deadline:
			return
		case <-time.After(50 * time.Millisecond):
			if conn.State() != StatePaused {
				t.Fatal("vpn left paused state despite auto_resume_after being unset (0)")
			}
		}
	}
}

func TestVPNManualPauseDuringCooldownIsNotOverridden(t *testing.T) {
	cfg := testConnConfig("autoresume-manual-override")
	cfg.AutoPauseThreshold = 2
	cfg.AutoResumeAfter = 1
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
	if !conn.Stats().AutoPaused {
		t.Fatal("expected an auto-pause before testing the manual-override race")
	}

	// A human explicitly re-pauses while the cooldown is armed — this must
	// stick; the timer firing afterward must not silently resume it.
	conn.Pause()

	time.Sleep(2 * time.Second) // > AutoResumeAfter, so the stale timer fires during this window

	st := conn.Stats()
	if st.State != StatePaused {
		t.Errorf("State = %v, want %v (manual pause must survive the auto-resume timer firing)", st.State, StatePaused)
	}
	if st.AutoPaused {
		t.Error("AutoPaused = true, want false (Pause() marks it manual)")
	}
}

// TestVPNPauseResumeRaceWithAutoResume hammers Pause()/Resume() from several
// goroutines concurrently with Run()'s own auto-pause/auto-resume cycling.
// Regression test for the pauseMu fix (mirrors
// tunnel.TestTunnelPauseResumeRaceWithAutoResume): confirms the new locking
// doesn't deadlock under concurrent callers and gives `go test -race` many
// chances to flag anything the fix missed.
func TestVPNPauseResumeRaceWithAutoResume(t *testing.T) {
	cfg := testConnConfig("pause-resume-race")
	cfg.AutoPauseThreshold = 1
	cfg.AutoResumeAfter = 1
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

	stopAt := time.Now().Add(2 * time.Second)

	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for time.Now().Before(stopAt) {
				conn.Pause()
				conn.Resume()
				_ = conn.Stats()
			}
		})
	}

	wg.Wait()
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
