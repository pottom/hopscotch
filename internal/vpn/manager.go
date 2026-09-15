package vpn

import (
	"context"

	"github.com/charmbracelet/log"
	"golang.org/x/sync/errgroup"

	"github.com/pottom/hopscotch/internal/config"
)

// Manager owns all VPN connections and reports their state for tunnel gating.
type Manager struct {
	connections map[string]*Connection
}

// NewManager creates a Manager from the given VPN configs.
func NewManager(vpnCfgs []config.VPNConfig) *Manager {
	m := &Manager{connections: make(map[string]*Connection, len(vpnCfgs))}
	// One gate for all VPNs: switching back and forth between them is exactly
	// the burst of sessions it guards against.
	gate := newSessionGate()
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
		conn.gate = gate
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
func (m *Manager) AllStats() map[string]Stats {
	out := make(map[string]Stats, len(m.connections))
	for name, conn := range m.connections {
		out[name] = conn.Stats()
	}
	return out
}
