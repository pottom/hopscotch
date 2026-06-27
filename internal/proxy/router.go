package proxy

import (
	"context"
	"errors"
	"fmt"
	"net"

	"github.com/charmbracelet/log"

	"hopscotch/internal/config"
	"hopscotch/internal/tunnel"
)

// errBlocked is returned when a via:block rule matches.
var errBlocked = errors.New("blocked")

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

	label, dialer, tunnelStatus, pattern, err := r.resolve(ctx, host)
	if err != nil {
		if errors.Is(err, errBlocked) {
			log.Warn("proxy blocked", "host", host, "proto", inferProto(port), "err", err)
		} else {
			log.Warn("proxy refused", "host", host, "proto", inferProto(port), "err", err)
		}
		return nil, err
	}

	if tunnelStatus != "" {
		log.Info("proxy",
			"proto", inferProto(port),
			"host", host,
			"pattern", pattern,
			"via", label,
			"tunnel", tunnelStatus,
		)
	} else {
		log.Info("proxy",
			"proto", inferProto(port),
			"host", host,
			"pattern", pattern,
			"via", label,
		)
	}
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

type dialContexter interface {
	DialContext(ctx context.Context, network, addr string) (net.Conn, error)
}

// resolve finds the matching rule and returns the label, dialer, matched pattern, and (for tunnels)
// the tunnel status captured before any wait — so the caller can log what state
// the tunnel was in when the request arrived.
func (r *Router) resolve(ctx context.Context, host string) (label string, dialer dialContexter, tunnelStatus string, pattern string, err error) {
	for _, rule := range r.rules {
		if !matchPattern(rule.Pattern, host) {
			continue
		}

		if rule.Via == config.ViaDirect {
			return config.ViaDirect, &r.direct, "", rule.Pattern, nil
		}

		if rule.Via == config.ViaBlock {
			return "", nil, "", "", fmt.Errorf("%w: connection to %s blocked by rule (pattern: %s)", errBlocked, host, rule.Pattern)
		}

		t := r.tunnels.Get(rule.Tunnel)
		if t == nil {
			return "", nil, "", "", fmt.Errorf("rule refers to unknown tunnel %q", rule.Tunnel)
		}

		// Snapshot status before waiting so the log reflects what the caller saw.
		initialStatus := t.Stats().Status.String()

		if err := r.waitForTunnel(ctx, t); err != nil {
			return "", nil, "", "", err
		}

		return rule.Tunnel, t, initialStatus, rule.Pattern, nil
	}

	// No matching rule — use direct as fallback.
	log.Warn("no routing rule matched, using direct", "host", host)
	return config.ViaDirect, &r.direct, "", "", nil
}

// waitForTunnel returns immediately with an error if the tunnel is not connected.
// We fail fast instead of waiting so callers get instant feedback rather than
// sitting through a 30-second timeout.
func (r *Router) waitForTunnel(_ context.Context, t *tunnel.Tunnel) error {
	if t.Stats().Status == tunnel.StatusConnected {
		return nil
	}
	return fmt.Errorf("tunnel %q is offline (status: %s) — connection refused", t.Name(), t.Stats().Status)
}

