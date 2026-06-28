package proxy

import (
	"context"
	"errors"
	"fmt"
	"net"

	"github.com/charmbracelet/log"

	"github.com/pottom/hopscotch/internal/config"
	"github.com/pottom/hopscotch/internal/tunnel"
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

	proto := inferProto(network, port)

	label, dialer, tunnelStatus, pattern, err := r.resolve(ctx, host)
	if err != nil {
		if errors.Is(err, errBlocked) {
			log.Warn("proxy blocked", "host", host, "proto", proto, "err", err)
		} else {
			log.Warn("proxy refused", "host", host, "proto", proto, "err", err)
		}
		return nil, err
	}

	if tunnelStatus != "" {
		log.Info("proxy",
			"proto", proto,
			"host", host,
			"pattern", pattern,
			"via", label,
			"tunnel", tunnelStatus,
		)
	} else {
		log.Info("proxy",
			"proto", proto,
			"host", host,
			"pattern", pattern,
			"via", label,
		)
	}
	return dialer.DialContext(ctx, network, addr)
}

// inferProto returns a human-readable protocol string in the form
// "name/network/port" (e.g. "ssh/tcp/22") when the port is well-known,
// or "network/port" (e.g. "tcp/9999") otherwise.
func inferProto(network, port string) string {
	if network == "" {
		network = "tcp"
	}
	name := knownPort(port)
	if port == "" {
		return network
	}
	if name != "" {
		return name + "/" + network + "/" + port
	}
	return network + "/" + port
}

func knownPort(port string) string {
	switch port {
	case "21":
		return "ftp"
	case "22":
		return "ssh"
	case "25", "587":
		return "smtp"
	case "53":
		return "dns"
	case "80", "8080":
		return "http"
	case "443", "8443":
		return "https"
	case "1433":
		return "mssql"
	case "3306":
		return "mysql"
	case "3389":
		return "rdp"
	case "5432":
		return "postgres"
	case "5672":
		return "amqp"
	case "6379":
		return "redis"
	case "6443":
		return "k8s-api"
	case "8883":
		return "mqtt"
	case "9200":
		return "elasticsearch"
	case "27017":
		return "mongodb"
	default:
		return ""
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

