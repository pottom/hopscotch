package vpn

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

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
	// The vpnc wrapper and its records live next to the history.
	stateDir := ""
	if historyPath != "" {
		stateDir = filepath.Dir(historyPath)
	}
	for _, cfg := range vpnCfgs {
		wrapper := ""
		if stateDir != "" {
			systemScript := cfg.VPNCScript
			if systemScript == "" {
				systemScript = findSystemVPNCScript()
			}
			path, err := writeVPNCWrapper(stateDir, cfg.Name, systemScript)
			if err != nil {
				log.Warn("vpn: cannot write the vpnc-script wrapper, openconnect keeps its default script", "vpn", cfg.Name, "err", err)
			} else {
				wrapper = path
				log.Debug("vpn: vpnc-script wrapper written", "vpn", cfg.Name, "path", path, "wraps", systemScript)
			}
		}
		conn := newConnection(connConfig{
			ScriptWrapper:      wrapper,
			StateDir:           stateDir,
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

// Has reports whether a VPN with this name is configured.
func (m *Manager) Has(name string) bool {
	_, ok := m.connections[name]
	return ok
}

// ErrSwitchTimeout means the target VPN did not take over the traffic in time.
var ErrSwitchTimeout = errors.New("did not take over the traffic in time")

// Switch hands the traffic to the VPN named to: it resumes to, waits until to
// is connected, and then pauses every other connected VPN whose pushed routes
// overlap to's — those are the ones whose tunnel would otherwise keep the
// shared networks. VPNs that push only other networks are left alone, so two
// VPNs into different networks keep running side by side. Returns the VPNs it
// paused. If to doesn't connect within its connect_timeout plus a margin it is
// left running (it keeps retrying like any resumed VPN) and nothing else is
// touched.
//
// The decision is by route overlap, not by which interface currently carries
// the traffic: on macOS both openconnects re-point the shared routes on every
// reconnect and race for them (measured 2026-09-19), so "who owns the route
// right now" flaps. Overlap is stable, and once the losing VPN is paused its
// disconnect hook (see vpncscript.go) hands the shared routes back to to.
func (m *Manager) Switch(ctx context.Context, to string) ([]string, error) {
	conn, ok := m.connections[to]
	if !ok {
		return nil, fmt.Errorf("vpn %q not configured", to)
	}
	log.Info("vpn: switching", "vpn", to)
	conn.Resume()

	timeout := time.Duration(conn.cfg.ConnectTimeout) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	deadline := time.Now().Add(timeout + 15*time.Second)
	for {
		stats := m.AllStats()
		if stats[to].State == StateConnected {
			target := routeSet(stats[to].PushedRoutes)
			var paused []string
			for name, other := range stats {
				if name == to || other.State != StateConnected {
					continue
				}
				if !routesOverlap(target, other.PushedRoutes) {
					log.Info("vpn: switch keeps a VPN into another network up", "vpn", to, "kept", name)
					continue
				}
				log.Info("vpn: switch complete, pausing the overlapping VPN", "vpn", to, "paused", name)
				m.connections[name].Pause()
				paused = append(paused, name)
			}
			return paused, nil
		}
		if time.Now().After(deadline) {
			log.Warn("vpn: switch gave up waiting for the VPN to connect, leaving everything as it is", "vpn", to, "waited", timeout+15*time.Second)
			return nil, ErrSwitchTimeout
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// routeSet indexes pushed route prefixes for overlap tests.
func routeSet(routes []string) map[string]bool {
	set := make(map[string]bool, len(routes))
	for _, r := range routes {
		set[r] = true
	}
	return set
}

// routesOverlap reports whether any of routes is in target. When neither side
// has any pushed routes recorded (no wrapper, e.g. openconnect built without
// one), it returns true so the old pause-the-other behaviour is kept — a
// switch between two route-less VPNs still switches.
func routesOverlap(target map[string]bool, routes []string) bool {
	if len(target) == 0 && len(routes) == 0 {
		return true
	}
	for _, r := range routes {
		if target[r] {
			return true
		}
	}
	return false
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
