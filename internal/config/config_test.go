package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "hopscotch-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return f.Name()
}

func TestLoad_Valid(t *testing.T) {
	path := writeConfig(t, `
tunnels:
  - name: prod
    host: jump.example.com
    user: myuser
    local_port: 1080
    identity_file: /tmp/id_test

proxy:
  port: 8080
  rules:
    - pattern: "*.example.com"
      target: prod
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if len(cfg.Tunnels) != 1 {
		t.Errorf("Tunnels count = %d, want 1", len(cfg.Tunnels))
	}
	if cfg.Tunnels[0].Port != DefaultSSHPort {
		t.Errorf("Default port not applied: got %d", cfg.Tunnels[0].Port)
	}
	if cfg.Admin.Port != DefaultAdminPort {
		t.Errorf("Default admin port not applied: got %d", cfg.Admin.Port)
	}
}

func TestLoad_MissingHost(t *testing.T) {
	path := writeConfig(t, `
tunnels:
  - name: prod
    user: myuser
    local_port: 1080
proxy:
  port: 8080
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for missing host, got nil")
	}
}

func TestLoad_DuplicateName(t *testing.T) {
	path := writeConfig(t, `
tunnels:
  - name: prod
    host: jump1.example.com
    user: myuser
    local_port: 1080
  - name: prod
    host: jump2.example.com
    user: myuser
    local_port: 1081
proxy:
  port: 8080
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for duplicate name, got nil")
	}
}

func TestLoad_PortConflict(t *testing.T) {
	path := writeConfig(t, `
tunnels:
  - name: prod
    host: jump1.example.com
    user: myuser
    local_port: 1080
  - name: dev
    host: jump2.example.com
    user: myuser
    local_port: 1080
proxy:
  port: 8080
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for port conflict, got nil")
	}
}

func TestLoad_NoConfigFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nonexistent.yaml"))
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestLoad_MissingUser(t *testing.T) {
	path := writeConfig(t, `
tunnels:
  - name: prod
    host: jump.example.com
    local_port: 1080
proxy:
  port: 8080
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for missing user, got nil")
	}
}

func TestLoad_MissingName(t *testing.T) {
	path := writeConfig(t, `
tunnels:
  - host: jump.example.com
    user: myuser
    local_port: 1080
proxy:
  port: 8080
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for missing name, got nil")
	}
}

func TestLoad_MissingLocalPort(t *testing.T) {
	path := writeConfig(t, `
tunnels:
  - name: prod
    host: jump.example.com
    user: myuser
proxy:
  port: 8080
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for missing local_port, got nil")
	}
}

func TestLoad_ProxyAdminPortConflict(t *testing.T) {
	path := writeConfig(t, `
tunnels:
  - name: prod
    host: jump.example.com
    user: myuser
    local_port: 1080
proxy:
  port: 9090
admin:
  port: 9090
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for proxy/admin port conflict, got nil")
	}
}

func TestLoad_RuleEmptyPattern(t *testing.T) {
	path := writeConfig(t, `
tunnels:
  - name: prod
    host: jump.example.com
    user: myuser
    local_port: 1080
proxy:
  port: 8080
  rules:
    - pattern: ""
      target: prod
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for empty pattern, got nil")
	}
}

func TestLoad_RuleNoTunnelOrVia(t *testing.T) {
	path := writeConfig(t, `
tunnels:
  - name: prod
    host: jump.example.com
    user: myuser
    local_port: 1080
proxy:
  port: 8080
  rules:
    - pattern: "*.example.com"
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for rule with no target, got nil")
	}
}

func TestLoad_UnknownVPNReference(t *testing.T) {
	path := writeConfig(t, `
tunnels:
  - name: prod
    host: jump.example.com
    user: myuser
    local_port: 1080
    requires_vpn: nonexistent-vpn
proxy:
  port: 8080
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for unknown requires_vpn reference, got nil")
	}
}

func TestLoad_VPNUnsupportedType(t *testing.T) {
	path := writeConfig(t, `
tunnels:
  - name: prod
    host: jump.example.com
    user: myuser
    local_port: 1080
vpn:
  - name: myvpn
    type: wireguard
    server: vpn.example.com
proxy:
  port: 8080
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for unsupported VPN type, got nil")
	}
}

func TestLoad_VPNExtraArgsManagedFlag(t *testing.T) {
	path := writeConfig(t, `
tunnels:
  - name: prod
    host: jump.example.com
    user: myuser
    local_port: 1080
vpn:
  - name: myvpn
    type: openconnect
    server: vpn.example.com
    user: vpnuser
    extra_args:
      - "--user=other"
proxy:
  port: 8080
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for managed flag in extra_args, got nil")
	}
}

func TestLoad_VPNDuplicateName(t *testing.T) {
	path := writeConfig(t, `
tunnels:
  - name: prod
    host: jump.example.com
    user: myuser
    local_port: 1080
vpn:
  - name: myvpn
    type: openconnect
    server: vpn1.example.com
  - name: myvpn
    type: openconnect
    server: vpn2.example.com
proxy:
  port: 8080
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for duplicate VPN name, got nil")
	}
}
