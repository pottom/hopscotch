package tunnel

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"
)

// closedPort binds a local TCP listener and immediately closes it, returning
// its host/port. Dialing that address afterwards deterministically fails
// with "connection refused" — no real SSH server or timeout needed to
// exercise the dial-failure path.
func closedPort(t *testing.T) (string, int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().(*net.TCPAddr)
	ln.Close()
	return "127.0.0.1", addr.Port
}

func TestTunnelAutoPauseAfterConsecutiveFailures(t *testing.T) {
	host, port := closedPort(t)

	cfg := testTunnelCfg("autopause", 1093)
	cfg.Host = host
	cfg.Port = port
	cfg.DialTimeout = 2
	cfg.ReconnectDelay = 1
	cfg.ReconnectMaxDelay = 1
	cfg.AutoPauseThreshold = 2

	tun := New(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		tun.Run(ctx)
		close(done)
	}()
	defer func() {
		cancel()
		<-done
	}()

	waitForStatus(t, tun, StatusPaused, 5*time.Second)

	st := tun.Stats()
	if st.ConsecutiveFailures < 2 {
		t.Errorf("ConsecutiveFailures = %d, want >= 2 (the configured threshold)", st.ConsecutiveFailures)
	}
	if !st.AutoPaused {
		t.Error("AutoPaused = false, want true (this pause was triggered by the threshold, not a manual Pause())")
	}
}

func TestTunnelManualPauseIsNotMarkedAuto(t *testing.T) {
	cfg := testTunnelCfg("manual-pause-not-auto", 1096)
	cfg.AutoPauseThreshold = 2
	tun := New(cfg)
	tun.Pause()

	if st := tun.Stats(); st.AutoPaused {
		t.Error("AutoPaused = true after a manual Pause(), want false")
	}
}

func TestTunnelAutoPauseResumeResetsCounter(t *testing.T) {
	host, port := closedPort(t)

	cfg := testTunnelCfg("autopause-resume", 1094)
	cfg.Host = host
	cfg.Port = port
	cfg.DialTimeout = 2
	cfg.ReconnectDelay = 1
	cfg.ReconnectMaxDelay = 1
	cfg.AutoPauseThreshold = 2

	tun := New(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		tun.Run(ctx)
		close(done)
	}()
	defer func() {
		cancel()
		<-done
	}()

	waitForStatus(t, tun, StatusPaused, 5*time.Second)

	tun.Resume()

	// Resume() resets the counter (and the auto-pause flag) synchronously, so
	// this holds immediately with no timing dependency on the reconnect loop.
	st := tun.Stats()
	if st.ConsecutiveFailures != 0 {
		t.Errorf("ConsecutiveFailures after Resume() = %d, want 0", st.ConsecutiveFailures)
	}
	if st.AutoPaused {
		t.Error("AutoPaused after Resume() = true, want false")
	}
}

func TestTunnelAutoResumeAfterCooldown(t *testing.T) {
	host, port := closedPort(t)

	cfg := testTunnelCfg("autoresume", 1097)
	cfg.Host = host
	cfg.Port = port
	cfg.DialTimeout = 2
	cfg.ReconnectDelay = 1
	cfg.ReconnectMaxDelay = 1
	cfg.AutoPauseThreshold = 2
	cfg.AutoResumeAfter = 1

	tun := New(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		tun.Run(ctx)
		close(done)
	}()
	defer func() {
		cancel()
		<-done
	}()

	waitForStatus(t, tun, StatusPaused, 5*time.Second)
	if !tun.Stats().AutoPaused {
		t.Fatal("expected an auto-pause before testing auto-resume")
	}

	// After the 1s cooldown it should retry on its own — AutoPaused clears
	// (same reset Resume() does) even though the port is still closed and
	// it'll likely auto-pause again shortly after.
	deadline := time.After(5 * time.Second)
	for {
		if !tun.Stats().AutoPaused {
			return
		}
		select {
		case <-deadline:
			t.Fatal("tunnel never auto-resumed after the cooldown")
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func TestTunnelAutoResumeDisabledByDefault(t *testing.T) {
	host, port := closedPort(t)

	cfg := testTunnelCfg("autoresume-disabled", 1098)
	cfg.Host = host
	cfg.Port = port
	cfg.DialTimeout = 2
	cfg.ReconnectDelay = 1
	cfg.ReconnectMaxDelay = 1
	cfg.AutoPauseThreshold = 2
	// AutoResumeAfter left at zero value (disabled).

	tun := New(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		tun.Run(ctx)
		close(done)
	}()
	defer func() {
		cancel()
		<-done
	}()

	waitForStatus(t, tun, StatusPaused, 5*time.Second)

	// Well past what any cooldown would need if the feature were mistakenly
	// active — confirm it never resumes on its own.
	deadline := time.After(2 * time.Second)
	for {
		select {
		case <-deadline:
			return
		case <-time.After(50 * time.Millisecond):
			if tun.Stats().Status != StatusPaused {
				t.Fatal("tunnel left paused state despite auto_resume_after being unset (0)")
			}
		}
	}
}

func TestTunnelManualPauseDuringCooldownIsNotOverridden(t *testing.T) {
	host, port := closedPort(t)

	cfg := testTunnelCfg("autoresume-manual-override", 1099)
	cfg.Host = host
	cfg.Port = port
	cfg.DialTimeout = 2
	cfg.ReconnectDelay = 1
	cfg.ReconnectMaxDelay = 1
	cfg.AutoPauseThreshold = 2
	cfg.AutoResumeAfter = 1

	tun := New(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		tun.Run(ctx)
		close(done)
	}()
	defer func() {
		cancel()
		<-done
	}()

	waitForStatus(t, tun, StatusPaused, 5*time.Second)
	if !tun.Stats().AutoPaused {
		t.Fatal("expected an auto-pause before testing the manual-override race")
	}

	// A human explicitly re-pauses while the cooldown is armed — this must
	// stick; the timer firing afterward must not silently resume it.
	tun.Pause()

	time.Sleep(2 * time.Second) // > AutoResumeAfter, so the stale timer fires during this window

	st := tun.Stats()
	if st.Status != StatusPaused {
		t.Errorf("Status = %v, want %v (manual pause must survive the auto-resume timer firing)", st.Status, StatusPaused)
	}
	if st.AutoPaused {
		t.Error("AutoPaused = true, want false (Pause() marks it manual)")
	}
}

// TestTunnelPauseResumeRaceWithAutoResume hammers Pause()/Resume() from
// several goroutines concurrently with Run()'s own auto-pause/auto-resume
// cycling (a short AutoResumeAfter keeps that cycle firing throughout the
// test). This is a regression test for the pauseMu fix: the auto-resume
// timer's check-then-mutate of paused/autoPaused/consecutiveFailures used to
// run outside any lock, so a concurrent Pause()/Resume() could interleave
// with it mid-sequence. Run under `go test -race`, this both confirms the
// new locking doesn't deadlock under concurrent callers and gives the race
// detector many chances to flag any unsynchronized access the fix missed.
func TestTunnelPauseResumeRaceWithAutoResume(t *testing.T) {
	host, port := closedPort(t)

	cfg := testTunnelCfg("pause-resume-race", 1100)
	cfg.Host = host
	cfg.Port = port
	cfg.DialTimeout = 1
	cfg.ReconnectDelay = 1
	cfg.ReconnectMaxDelay = 1
	cfg.AutoPauseThreshold = 1
	cfg.AutoResumeAfter = 1

	tun := New(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		tun.Run(ctx)
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
				tun.Pause()
				tun.Resume()
				_ = tun.Stats()
			}
		})
	}

	wg.Wait()
}

func TestTunnelAutoPauseDisabledByDefault(t *testing.T) {
	host, port := closedPort(t)

	cfg := testTunnelCfg("autopause-disabled", 1095)
	cfg.Host = host
	cfg.Port = port
	cfg.DialTimeout = 2
	cfg.ReconnectDelay = 1
	cfg.ReconnectMaxDelay = 1
	// AutoPauseThreshold left at zero value (disabled).

	tun := New(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		tun.Run(ctx)
		close(done)
	}()
	defer func() {
		cancel()
		<-done
	}()

	// Give it several failed-dial cycles worth of time — well past what
	// would trigger an auto-pause if the feature were mistakenly active.
	deadline := time.After(3 * time.Second)
	for {
		select {
		case <-deadline:
			return // success: never paused
		case <-time.After(50 * time.Millisecond):
			if tun.Stats().Status == StatusPaused {
				t.Fatal("tunnel auto-paused despite auto_pause_threshold being unset (0)")
			}
		}
	}
}
