package proxy

import (
	"context"
	"testing"

	"github.com/pottom/hopscotch/internal/config"
	"github.com/pottom/hopscotch/internal/tunnel"
)

// mockLookup implements TunnelLookup for testing.
type mockLookup struct {
	tunnels map[string]*tunnel.Tunnel
}

func (m *mockLookup) Get(name string) *tunnel.Tunnel {
	if m == nil {
		return nil
	}
	return m.tunnels[name]
}

// directRules builds a router where every rule uses via:direct, so no real
// tunnel is needed — we only verify which pattern was selected.
func directRules(patterns ...string) ([]config.Rule, *Router) {
	rules := make([]config.Rule, len(patterns))
	for i, p := range patterns {
		rules[i] = config.Rule{Pattern: p, Via: "direct"}
	}
	return rules, NewRouter(rules, nil)
}

func TestResolve_CIDRPrecedence(t *testing.T) {
	// /24 exception must be listed before the broader /20 catch-all.
	_, r := directRules("10.0.1.0/24", "10.0.0.0/20")

	tests := []struct {
		host    string
		pattern string // expected winning pattern
	}{
		{"10.0.1.5", "10.0.1.0/24"},   // inside /24 → specific wins
		{"10.0.1.254", "10.0.1.0/24"}, // last address in /24
		{"10.0.2.1", "10.0.0.0/20"},   // outside /24, inside /20 → broad wins
		{"10.0.15.1", "10.0.0.0/20"},  // far end of /20
	}

	for _, tc := range tests {
		_, _, _, got, err := r.resolve(context.Background(), tc.host)
		if err != nil {
			t.Errorf("resolve(%q): unexpected error: %v", tc.host, err)
			continue
		}
		if got != tc.pattern {
			t.Errorf("resolve(%q): pattern = %q, want %q", tc.host, got, tc.pattern)
		}
	}
}

func TestResolve_GlobBeforeCIDR(t *testing.T) {
	// Glob wildcard listed before CIDR — glob should win for matching hosts.
	_, r := directRules("*.internal", "10.0.0.0/8")

	_, _, _, got, err := r.resolve(context.Background(), "api.internal")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "*.internal" {
		t.Errorf("pattern = %q, want *.internal", got)
	}
}

func TestResolve_DirectFallback(t *testing.T) {
	// Host matches no rule → direct fallback, no error.
	_, r := directRules("10.0.1.0/24")

	label, _, _, _, err := r.resolve(context.Background(), "192.168.1.1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if label != "direct" {
		t.Errorf("label = %q, want direct", label)
	}
}

func TestResolve_UnknownTunnel(t *testing.T) {
	rules := []config.Rule{
		{Pattern: "10.0.0.0/8", Tunnel: "nonexistent"},
	}
	r := NewRouter(rules, &mockLookup{tunnels: map[string]*tunnel.Tunnel{}})

	_, _, _, _, err := r.resolve(context.Background(), "10.0.0.1")
	if err == nil {
		t.Fatal("expected error for unknown tunnel, got nil")
	}
}

func TestResolve_StarMatchesAll(t *testing.T) {
	_, r := directRules("*")

	_, _, _, pattern, err := r.resolve(context.Background(), "anything.example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pattern != "*" {
		t.Errorf("pattern = %q, want *", pattern)
	}
}
