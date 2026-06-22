// Package tunnel manages the lifecycle of SSH tunnels.
package tunnel

import (
	"context"
	"fmt"
	"sync"

	"github.com/charmbracelet/log"
	"golang.org/x/sync/errgroup"

	"hopscotch/internal/config"
)

// Manager owns all tunnels and exposes status and dialing.
type Manager struct {
	mu      sync.RWMutex
	tunnels map[string]*Tunnel
}

// NewManager creates a Manager from the given config.
func NewManager(tunnelCfgs []config.TunnelConfig) *Manager {
	m := &Manager{tunnels: make(map[string]*Tunnel, len(tunnelCfgs))}
	for _, cfg := range tunnelCfgs {
		m.tunnels[cfg.Name] = New(cfg)
	}
	return m
}

// Run starts all tunnels and blocks until ctx is cancelled.
func (m *Manager) Run(ctx context.Context) error {
	g, ctx := errgroup.WithContext(ctx)

	m.mu.RLock()
	for name, t := range m.tunnels {
		g.Go(func() error {
			log.Info("starting tunnel", "tunnel", name)
			if err := t.Run(ctx); err != nil {
				return fmt.Errorf("tunnel %q: %w", name, err)
			}
			return nil
		})
	}
	m.mu.RUnlock()

	return g.Wait()
}

// Get returns the tunnel with the given name, or nil.
func (m *Manager) Get(name string) *Tunnel {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.tunnels[name]
}

// AllStats returns a snapshot of every tunnel's stats keyed by name.
func (m *Manager) AllStats() map[string]Stats {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make(map[string]Stats, len(m.tunnels))
	for name, t := range m.tunnels {
		out[name] = t.Stats()
	}
	return out
}

// ApplyConfig adds new tunnels and starts them; stops and removes deleted ones.
// This is called on SIGHUP config reload.
func (m *Manager) ApplyConfig(ctx context.Context, newCfgs []config.TunnelConfig) {
	newSet := make(map[string]config.TunnelConfig, len(newCfgs))
	for _, cfg := range newCfgs {
		newSet[cfg.Name] = cfg
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Remove tunnels that disappeared from config.
	for name, t := range m.tunnels {
		if _, ok := newSet[name]; !ok {
			log.Info("stopping removed tunnel", "tunnel", name)
			t.setStatus(StatusDisconnected)
			delete(m.tunnels, name)
		}
	}

	// Add new tunnels.
	for name, cfg := range newSet {
		if _, exists := m.tunnels[name]; !exists {
			log.Info("starting new tunnel", "tunnel", name)
			t := New(cfg)
			m.tunnels[name] = t
			go func(t *Tunnel) {
				if err := t.Run(ctx); err != nil {
					log.Error("tunnel exited", "tunnel", t.Name(), "err", err)
				}
			}(t)
		}
	}
}
