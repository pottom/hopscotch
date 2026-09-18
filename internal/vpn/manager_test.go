package vpn

import (
	"context"
	"testing"
	"time"
)

// twoConnectedVPNs builds a Manager whose two VPNs are both connected, each
// with its own tunnel interface and the same ping_host.
func twoConnectedVPNs(t *testing.T) *Manager {
	t.Helper()
	m := &Manager{connections: map[string]*Connection{}}
	for name, iface := range map[string]string{"a": "tun0", "b": "tun1"} {
		cfg := testConnConfig(name)
		cfg.PingHost = "10.4.60.100:53"
		c := newConnection(cfg)
		c.setState(StateConnected)
		c.tunIface.Store(iface)
		m.connections[name] = c
	}
	return m
}

// With two VPNs connected, the one whose interface doesn't carry the traffic
// to ping_host is reported as routed via the other.
func TestAllStatsReportsWhichVPNCarriesTheTraffic(t *testing.T) {
	m := twoConnectedVPNs(t)
	orig := routeInterface
	defer func() { routeInterface = orig }()
	routeInterface = func(string) string { return "tun1" }

	stats := m.AllStats()
	if got := stats["a"].RoutedVia; got != "b" {
		t.Errorf("a.RoutedVia = %q, want %q (its traffic leaves through b's tun1)", got, "b")
	}
	if got := stats["b"].RoutedVia; got != "" {
		t.Errorf("b.RoutedVia = %q, want empty (its own interface carries the traffic)", got)
	}

	// An interface no VPN owns is named as is.
	routeInterface = func(string) string { return "en0" }
	stats = m.AllStats()
	for name, st := range stats {
		if st.RoutedVia != "en0" {
			t.Errorf("%s.RoutedVia = %q, want en0", name, st.RoutedVia)
		}
	}
}

// A single connected VPN is never second-guessed, whatever the route says.
func TestAllStatsSkipsRouteCheckWithOneVPNConnected(t *testing.T) {
	m := twoConnectedVPNs(t)
	m.connections["b"].setState(StatePaused)
	orig := routeInterface
	defer func() { routeInterface = orig }()
	called := false
	routeInterface = func(string) string { called = true; return "tun1" }

	stats := m.AllStats()
	if called {
		t.Error("route lookup ran with only one VPN connected")
	}
	if got := stats["a"].RoutedVia; got != "" {
		t.Errorf("a.RoutedVia = %q, want empty", got)
	}
}

// Switch brings the target up and pauses only the VPN whose routes it took
// over; a VPN into another network stays up.
func TestSwitchPausesOnlyTheVPNItTookOver(t *testing.T) {
	m := &Manager{connections: map[string]*Connection{}}
	stops := []func(){}
	for _, name := range []string{"a", "b", "other"} {
		cfg := testConnConfig(name)
		cfg.PingHost = "10.4.60.100:53"
		cfg.ConnectTimeout = 1
		c := newConnection(cfg)
		iface := "tun-" + name
		stops = append(stops, runWithAttempt(c, func(ctx context.Context) error {
			c.tunIface.Store(iface)
			c.setState(StateConnected)
			<-ctx.Done()
			return ctx.Err()
		}))
		m.connections[name] = c
	}
	defer func() {
		for _, stop := range stops {
			stop()
		}
	}()
	// a and other are up; b is paused and about to be switched to.
	m.connections["b"].Pause()
	waitForVPNState(t, m.connections["b"], StatePaused, 2*time.Second)
	waitForVPNState(t, m.connections["a"], StateConnected, 2*time.Second)

	orig := routeInterface
	defer func() { routeInterface = orig }()
	// Once b is up its interface carries the traffic that a used to carry;
	// other probes a host in its own network and is unaffected.
	m.connections["other"].cfg.PingHost = "10.99.0.1:53"
	routeInterface = func(host string) string {
		if host == "10.99.0.1:53" {
			return "tun-other"
		}
		if m.connections["b"].State() == StateConnected {
			return "tun-b"
		}
		return "tun-a"
	}

	paused, err := m.Switch(context.Background(), "b")
	if err != nil {
		t.Fatalf("Switch: %v", err)
	}
	if len(paused) != 1 || paused[0] != "a" {
		t.Errorf("paused = %v, want [a]", paused)
	}
	waitForVPNState(t, m.connections["a"], StatePaused, 2*time.Second)
	if st := m.connections["other"].State(); st != StateConnected {
		t.Errorf("other = %s, want connected (different network, must stay up)", st)
	}
	if st := m.connections["b"].State(); st != StateConnected {
		t.Errorf("b = %s, want connected", st)
	}
}

// When the target never takes over, nothing else is paused and the error
// says so.
func TestSwitchTimesOutWithoutTouchingOthers(t *testing.T) {
	m := &Manager{connections: map[string]*Connection{}}
	cfgA := testConnConfig("a")
	cfgA.PingHost = "10.4.60.100:53"
	a := newConnection(cfgA)
	stopA := runWithAttempt(a, func(ctx context.Context) error {
		a.tunIface.Store("tun-a")
		a.setState(StateConnected)
		<-ctx.Done()
		return ctx.Err()
	})
	defer stopA()
	cfgB := testConnConfig("b")
	cfgB.PingHost = "10.4.60.100:53"
	cfgB.ConnectTimeout = -1 // <= 0 falls back to the default; keep the test short below
	b := newConnection(cfgB)
	stopB := runWithAttempt(b, func(ctx context.Context) error {
		<-ctx.Done() // never connects
		return ctx.Err()
	})
	defer stopB()
	m.connections["a"], m.connections["b"] = a, b
	b.Pause()
	waitForVPNState(t, b, StatePaused, 2*time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	paused, err := m.Switch(ctx, "b")
	if err == nil || len(paused) != 0 {
		t.Fatalf("Switch = %v, %v; want no pauses and an error", paused, err)
	}
	if st := a.State(); st != StateConnected {
		t.Errorf("a = %s, want still connected", st)
	}
	if _, err := m.Switch(context.Background(), "nope"); err == nil {
		t.Error("switching to an unknown VPN must fail")
	}
}
