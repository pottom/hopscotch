package vpn

import (
	"context"
	"fmt"
	"time"

	"github.com/charmbracelet/log"
	"golang.org/x/sync/errgroup"

	"github.com/pottom/hopscotch/internal/config"
)

// Manager owns all VPN connections and provides WaitConnected for tunnel dependency.
type Manager struct {
	connections map[string]*Connection
}

// NewManager creates a Manager from the given VPN configs.
func NewManager(vpnCfgs []config.VPNConfig) *Manager {
	m := &Manager{connections: make(map[string]*Connection, len(vpnCfgs))}
	for _, cfg := range vpnCfgs {
		m.connections[cfg.Name] = newConnection(connConfig{
			Name:              cfg.Name,
			Binary:            cfg.Binary,
			Server:            cfg.Server,
			User:              cfg.User,
			AuthGroup:         cfg.AuthGroup,
			PasswordEnv:       cfg.PasswordEnv,
			PasswordCmd:       cfg.PasswordCmd,
			Certificate:       cfg.Certificate,
			Key:               cfg.Key,
			PingHost:          cfg.PingHost,
			ExtraArgs:         cfg.ExtraArgs,
			PreConnect:        cfg.PreConnect,
			PostDisconnect:    cfg.PostDisconnect,
			Sudo:              cfg.Sudo,
			DNSResolver:       cfg.DNSResolver,
			ReconnectDelay:    cfg.ReconnectDelay,
			ReconnectMaxDelay: cfg.ReconnectMaxDelay,
		})
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

// IsConnected reports whether the named VPN is currently in StateConnected.
func (m *Manager) IsConnected(name string) bool {
	conn, ok := m.connections[name]
	if !ok {
		return false
	}
	return conn.State() == StateConnected
}

// WaitConnected blocks until the named VPN reaches StateConnected or ctx is cancelled.
// Returns fmt.Errorf if the VPN name is not configured (config validation should catch this first).
func (m *Manager) WaitConnected(ctx context.Context, name string) error {
	conn, ok := m.connections[name]
	if !ok {
		return fmt.Errorf("vpn %q not configured", name)
	}
	for {
		if conn.State() == StateConnected {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(1 * time.Second):
		}
	}
}

// AllStats returns a Stats snapshot of every VPN connection, keyed by name.
func (m *Manager) AllStats() map[string]Stats {
	out := make(map[string]Stats, len(m.connections))
	for name, conn := range m.connections {
		out[name] = conn.Stats()
	}
	return out
}
