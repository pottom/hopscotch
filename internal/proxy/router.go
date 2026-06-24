package proxy

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/charmbracelet/log"

	"hopscotch/internal/config"
	"hopscotch/internal/tunnel"
)

// TunnelLookup finds a Tunnel by name.
type TunnelLookup interface {
	Get(name string) *tunnel.Tunnel
}

// Router selects the right dial target for each connection based on proxy rules.
// It implements socks5.Dialer.
type Router struct {
	rules   []config.Rule
	tunnels TunnelLookup
	direct  DirectCounter

	// connCounts tracks connections per route label for /metrics.
	connCounts map[string]*int64
}

// NewRouter creates a Router from the proxy config.
func NewRouter(rules []config.Rule, tunnels TunnelLookup) *Router {
	return &Router{
		rules:      rules,
		tunnels:    tunnels,
		connCounts: make(map[string]*int64),
	}
}

// DirectSnapshot returns a point-in-time view of direct connection traffic.
func (r *Router) DirectSnapshot() tunnel.TrafficSnapshot {
	return r.direct.Snapshot()
}

// UpdateRules replaces the routing rules (called on SIGHUP).
func (r *Router) UpdateRules(rules []config.Rule) {
	r.rules = rules
}

// Rules returns a snapshot of the current routing rules.
func (r *Router) Rules() []config.Rule {
	return r.rules
}

// DialContext selects the tunnel matching addr and dials through it.
func (r *Router) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
		port = ""
	}

	label, dialer, err := r.resolve(ctx, host)
	if err != nil {
		return nil, err
	}

	log.Info("proxy",
		"proto", inferProto(port),
		"host", host,
		"via", label,
	)
	return dialer.DialContext(ctx, network, addr)
}

// inferProto guesses the application protocol from the port number.
func inferProto(port string) string {
	switch port {
	case "443", "8443":
		return "https"
	case "80", "8080":
		return "http"
	case "":
		return "tcp"
	default:
		return "tcp/" + port
	}
}

// resolve finds the matching rule and returns the label and dialer to use.
func (r *Router) resolve(ctx context.Context, host string) (string, interface {
	DialContext(ctx context.Context, network, addr string) (net.Conn, error)
}, error) {
	for _, rule := range r.rules {
		if !matchPattern(rule.Pattern, host) {
			continue
		}

		if rule.Via == "direct" {
			return "direct", &r.direct, nil
		}

		t := r.tunnels.Get(rule.Tunnel)
		if t == nil {
			return "", nil, fmt.Errorf("rule refers to unknown tunnel %q", rule.Tunnel)
		}

		if err := r.waitForTunnel(ctx, t); err != nil {
			return "", nil, err
		}

		return rule.Tunnel, t, nil
	}

	// No matching rule — use direct as fallback.
	log.Warn("no routing rule matched, using direct", "host", host)
	return "direct", &r.direct, nil
}

// waitForTunnel blocks until the tunnel is connected or the wait window expires.
func (r *Router) waitForTunnel(ctx context.Context, t *tunnel.Tunnel) error {
	if t.Stats().Status == tunnel.StatusConnected {
		return nil
	}

	// Wait up to 30 seconds for the tunnel to come up.
	deadline := time.Now().Add(time.Duration(config.DefaultTunnelWaitTimeout) * time.Second)
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	for {
		if t.Stats().Status == tunnel.StatusConnected {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("tunnel %q is not connected (waited %ds)", t.Name(), config.DefaultTunnelWaitTimeout)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

