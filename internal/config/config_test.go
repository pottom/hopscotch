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
      tunnel: prod
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
