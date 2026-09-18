package vpn

import (
	"context"

	"github.com/charmbracelet/log"
	"golang.org/x/sync/errgroup"

	"github.com/pottom/hopscotch/internal/config"
	"github.com/pottom/hopscotch/internal/netcheck"
)

// routeInterface resolves which interface carries traffic to a host;
// replaced in tests.
var routeInterface = netcheck.RouteInterface

// Manager owns all VPN connections and reports their state for tunnel gating.
type Manager struct {
	connections map[string]*Connection
}

// NewManager creates a Manager from the given VPN configs. historyPath is the
// session history file the dark-retry policy learns from (see history.go);
// "" keeps the history in memory only.
func NewManager(vpnCfgs []config.VPNConfig, historyPath string) *Manager {
	m := &Manager{connections: make(map[string]*Connection, len(vpnCfgs))}
	// One history for all VPNs, so each session's context (time since the
	// other VPN's last session, recent starts) is complete.
	history := newSessionHistory(historyPath)
	for _, cfg := range vpnCfgs {
		conn := newConnection(connConfig{
			Name:               cfg.Name,
			Binary:             cfg.Binary,
			Server:             cfg.Server,
			User:               cfg.User,
			AuthGroup:          cfg.AuthGroup,
			PasswordEnv:        cfg.PasswordEnv,
			PasswordCmd:        cfg.PasswordCmd,
			Certificate:        cfg.Certificate,
			Key:                cfg.Key,
			PingHost:           cfg.PingHost,
			ConnectTimeout:     cfg.ConnectTimeout,
			HoldDarkSession:    cfg.HoldDarkSession,
			ExtraArgs:          cfg.ExtraArgs,
			PreConnect:         cfg.PreConnect,
			PostDisconnect:     cfg.PostDisconnect,
			Sudo:               cfg.Sudo,
			DNSResolver:        cfg.DNSResolver,
			ReconnectDelay:     cfg.ReconnectDelay,
			ReconnectMaxDelay:  cfg.ReconnectMaxDelay,
			AutoPauseThreshold: cfg.AutoPauseThreshold,
			AutoResumeAfter:    cfg.AutoResumeAfter,
		})
		conn.history = history
		m.connections[cfg.Name] = conn
	}
	return m
}

// Run starts all VPN connections and blocks until ctx is cancelled.
func (m *Manager) Run(ctx context.Context) error {
	g, ctx := errgroup.WithContext(ctx)
	for _, conn := range m.connections {
		c := conn
		g.Go(func() error {
			log.Info("starting vpn", "vpn", c.cfg.Name, "server", c.cfg.Server)
			return c.Run(ctx)
		})
	}
	return g.Wait()
}

// ForceReconnect triggers an immediate reconnect for the named VPN.
// Returns false if the VPN name is not found.
func (m *Manager) ForceReconnect(name string) bool {
	conn, ok := m.connections[name]
	if !ok {
		return false
	}
	conn.ForceReconnect()
	return true
}

// Pause stops the named VPN from retrying, tearing down any in-flight or
// active subprocess immediately. Returns false if the VPN name is not found.
func (m *Manager) Pause(name string) bool {
	conn, ok := m.connections[name]
	if !ok {
		return false
	}
	conn.Pause()
	return true
}

// Resume clears a paused VPN connection and triggers an immediate reconnect.
// Returns false if the VPN name is not found.
func (m *Manager) Resume(name string) bool {
	conn, ok := m.connections[name]
	if !ok {
		return false
	}
	conn.Resume()
	return true
}

// IsConnected reports whether the named VPN is currently in StateConnected.
func (m *Manager) IsConnected(name string) bool {
	conn, ok := m.connections[name]
	if !ok {
		return false
	}
	return conn.State() == StateConnected
}

// IsPaused reports whether the named VPN is paused (manually or automatically).
// Reads the pause flag rather than State so a just-requested pause counts
// before Run() has torn the subprocess down.
func (m *Manager) IsPaused(name string) bool {
	conn, ok := m.connections[name]
	if !ok {
		return false
	}
	return conn.paused.Load()
}

// AllStats returns a Stats snapshot of every VPN connection, keyed by name.
//
// With two VPNs connected at once their routes overlap (both push the same
// internal networks), and only one interface actually carries the traffic:
// on Linux the later session replaces the routes, on macOS the earlier one
// keeps them. ping_host answers through whichever interface owns the route,
// so both VPNs report connected even though one of them is idle. RoutedVia
// names the VPN (or bare interface) that really carries a connected VPN's
// traffic when it isn't its own tunnel, so the UIs can say so.
func (m *Manager) AllStats() map[string]Stats {
	out := make(map[string]Stats, len(m.connections))
	connected := 0
	for name, conn := range m.connections {
		st := conn.Stats()
		if st.State == StateConnected {
			connected++
		}
		out[name] = st
	}
	if connected < 2 {
		return out
	}
	owner := make(map[string]string, connected) // interface -> VPN name
	for name, st := range out {
		if st.State == StateConnected && st.TunIface != "" {
			owner[st.TunIface] = name
		}
	}
	for name, conn := range m.connections {
		st := out[name]
		if st.State != StateConnected || st.TunIface == "" || conn.cfg.PingHost == "" {
			continue
		}
		via := routeInterface(conn.cfg.PingHost)
		if via == "" || via == st.TunIface {
			continue
		}
		if vpnName, ok := owner[via]; ok {
			st.RoutedVia = vpnName
		} else {
			st.RoutedVia = via
		}
		out[name] = st
	}
	return out
}
