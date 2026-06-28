package cmd

import (
	"strings"
	"testing"

	"github.com/pottom/hopscotch/internal/config"
)

func baseTunnel(name string, port int) config.TunnelConfig {
	return config.TunnelConfig{
		Name:      name,
		Host:      "jump.example.com",
		User:      "myuser",
		LocalPort: port,
	}
}

func TestBuildSSHConfig_GlobOnly(t *testing.T) {
	cfg := &config.Config{
		Tunnels: []config.TunnelConfig{baseTunnel("prod", 1080)},
		Proxy: config.ProxyConfig{
			Rules: []config.Rule{
				{Pattern: "*.prod.internal", Tunnel: "prod"},
				{Pattern: "api.example.com", Tunnel: "prod"},
			},
		},
	}
	out := buildSSHConfig(cfg)

	if !strings.Contains(out, "Host *.prod.internal api.example.com") {
		t.Errorf("expected glob Host line, got:\n%s", out)
	}
	if !strings.Contains(out, "ProxyCommand hopscotch proxy-connect %h %p") {
		t.Errorf("expected ProxyCommand line, got:\n%s", out)
	}
	if strings.Contains(out, "CIDR") {
		t.Errorf("unexpected CIDR comment for glob-only config:\n%s", out)
	}
}

func TestBuildSSHConfig_CIDROnly(t *testing.T) {
	cfg := &config.Config{
		Tunnels: []config.TunnelConfig{baseTunnel("prod", 1080)},
		Proxy: config.ProxyConfig{
			Rules: []config.Rule{
				{Pattern: "10.0.1.0/24", Tunnel: "prod"},
				{Pattern: "10.0.2.0/24", Tunnel: "prod"},
			},
		},
	}
	out := buildSSHConfig(cfg)

	if strings.Contains(out, "Host 10.0") {
		t.Errorf("CIDR must not appear in Host line, got:\n%s", out)
	}
	if !strings.Contains(out, "CIDR") {
		t.Errorf("expected CIDR comment, got:\n%s", out)
	}
	if !strings.Contains(out, "10.0.1.0/24") {
		t.Errorf("expected CIDR 10.0.1.0/24 in comment, got:\n%s", out)
	}
	if !strings.Contains(out, "Match originalhost") {
		t.Errorf("expected Match originalhost hint, got:\n%s", out)
	}
}

func TestBuildSSHConfig_MixedGlobAndCIDR(t *testing.T) {
	cfg := &config.Config{
		Tunnels: []config.TunnelConfig{baseTunnel("prod", 1080)},
		Proxy: config.ProxyConfig{
			Rules: []config.Rule{
				{Pattern: "10.0.1.0/24", Tunnel: "prod"},
				{Pattern: "*.prod.internal", Tunnel: "prod"},
			},
		},
	}
	out := buildSSHConfig(cfg)

	// Glob must be in Host line.
	if !strings.Contains(out, "Host *.prod.internal") {
		t.Errorf("expected glob in Host line, got:\n%s", out)
	}
	// CIDR must be in comment only, not in Host line.
	if strings.Contains(out, "Host 10") {
		t.Errorf("CIDR must not appear in Host line, got:\n%s", out)
	}
	if !strings.Contains(out, "10.0.1.0/24") {
		t.Errorf("expected CIDR in comment, got:\n%s", out)
	}
}

func TestBuildSSHConfig_MultipleTunnels(t *testing.T) {
	cfg := &config.Config{
		Tunnels: []config.TunnelConfig{
			baseTunnel("prod", 1080),
			baseTunnel("staging", 1081),
		},
		Proxy: config.ProxyConfig{
			Rules: []config.Rule{
				{Pattern: "*.prod.internal", Tunnel: "prod"},
				{Pattern: "*.staging.internal", Tunnel: "staging"},
			},
		},
	}
	out := buildSSHConfig(cfg)

	if !strings.Contains(out, "*.prod.internal") {
		t.Errorf("expected prod pattern, got:\n%s", out)
	}
	if !strings.Contains(out, "*.staging.internal") {
		t.Errorf("expected staging pattern, got:\n%s", out)
	}
	// Both tunnels should appear as separate Host blocks.
	if strings.Count(out, "ProxyCommand") != 2 {
		t.Errorf("expected 2 ProxyCommand lines, got:\n%s", out)
	}
}

func TestBuildSSHConfig_DirectRuleSkipped(t *testing.T) {
	cfg := &config.Config{
		Tunnels: []config.TunnelConfig{baseTunnel("prod", 1080)},
		Proxy: config.ProxyConfig{
			Rules: []config.Rule{
				{Pattern: "*.prod.internal", Tunnel: "prod"},
				{Pattern: "*", Via: "direct"},
			},
		},
	}
	out := buildSSHConfig(cfg)

	// The direct catch-all rule should not produce a Host block.
	if strings.Contains(out, "Host *\n") {
		t.Errorf("direct rule must not produce a Host block, got:\n%s", out)
	}
}
