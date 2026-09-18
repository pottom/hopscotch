package vpn

import (
	"testing"
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
