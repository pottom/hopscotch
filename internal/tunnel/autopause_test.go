package tunnel

import (
	"context"
	"net"
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
