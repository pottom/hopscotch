package tunnel

import (
	"context"
	"sync"
	"testing"

	"github.com/pottom/hopscotch/internal/config"
)

type fakeVPNs struct {
	mu        sync.Mutex
	connected map[string]bool
	paused    map[string]bool
}

func (f *fakeVPNs) IsConnected(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.connected[name]
}

func (f *fakeVPNs) IsPaused(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.paused[name]
}

func (f *fakeVPNs) set(connected, paused map[string]bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.connected, f.paused = connected, paused
}

// A tunnel listing two VPNs must be satisfied by either one, and report the
// VPN it actually depends on right now so both UIs can show its state.
func TestRequiresVPNAnyOf(t *testing.T) {
	vpns := &fakeVPNs{}
	cfg := config.TunnelConfig{Name: "jump", Host: "10.0.0.1", Port: 22, LocalPort: 1080, RequiresVPN: config.VPNNames{"a", "b"}}
	tun := NewManager([]config.TunnelConfig{cfg}, vpns).Get("jump")

	steps := []struct {
		connected, paused map[string]bool
		wantConnected     bool
		wantShown         string
	}{
		{nil, nil, false, "a"},                        // nothing up: first listed
		{nil, map[string]bool{"a": true}, false, "b"}, // switched away from a: b is the one coming up
		{map[string]bool{"b": true}, map[string]bool{"a": true}, true, "b"},
		{map[string]bool{"a": true, "b": true}, nil, true, "a"},  // both up: first listed wins
		{nil, map[string]bool{"a": true, "b": true}, false, "a"}, // all paused: first listed
	}
	for i, s := range steps {
		vpns.set(s.connected, s.paused)
		if got := tun.vpnIsConnected(); got != s.wantConnected {
			t.Errorf("step %d: gate open = %v, want %v", i, got, s.wantConnected)
		}
		if got := tun.Stats().RequiresVPN; got != s.wantShown {
			t.Errorf("step %d: shown VPN = %q, want %q", i, got, s.wantShown)
		}
	}
	if got := tun.Stats().RequiresVPNAny; len(got) != 2 {
		t.Errorf("RequiresVPNAny = %v, want both names", got)
	}
}

func TestRequiresVPNGateOpensOnAnyAndHonoursCancel(t *testing.T) {
	vpns := &fakeVPNs{}
	cfg := config.TunnelConfig{Name: "jump", Host: "10.0.0.1", Port: 22, LocalPort: 1080, RequiresVPN: config.VPNNames{"a", "b"}}
	tun := NewManager([]config.TunnelConfig{cfg}, vpns).Get("jump")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := tun.vpnGate(ctx); err == nil {
		t.Fatal("gate returned nil on a cancelled context with no VPN connected")
	}

	vpns.set(map[string]bool{"b": true}, nil)
	if err := tun.vpnGate(context.Background()); err != nil {
		t.Fatalf("gate with b connected: %v", err)
	}
}
