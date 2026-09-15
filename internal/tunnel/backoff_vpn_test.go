package tunnel

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// A tunnel whose required VPN goes down must not sit out its backoff countdown
// (up to reconnect_max_delay after a VPN switch): it goes straight back to the
// VPN gate, which dials the moment the VPN is up again.
func TestTunnelSkipsBackoffWhileVPNDown(t *testing.T) {
	host, port := closedPort(t)

	cfg := testTunnelCfg("vpn-backoff", 1094)
	cfg.Host = host
	cfg.Port = port
	cfg.DialTimeout = 2
	cfg.ReconnectDelay = 30 // a countdown would blow the deadline below
	cfg.ReconnectMaxDelay = 30

	// The VPN is up for the first check (so the dial runs and fails), then down.
	var checks atomic.Int32
	isConnected := func() bool { return checks.Add(1) == 1 }

	gateEntered := make(chan struct{}, 1)
	gate := func(ctx context.Context) error {
		select {
		case gateEntered <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return ctx.Err()
	}

	tun := NewWithGate(cfg, gate, isConnected)
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

	select {
	case <-gateEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("tunnel did not return to the VPN gate after a failed dial with the VPN down; it is waiting out the backoff")
	}
	if next := tun.Stats().NextReconnectAt; !next.IsZero() {
		t.Errorf("NextReconnectAt = %v, want zero (no countdown while waiting for the VPN)", next)
	}
}
