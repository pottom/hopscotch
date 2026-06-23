// Package config handles loading, validation and watching of the YAML config file.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// ConfigError is returned when the config file is invalid.
type ConfigError struct {
	Field   string
	Message string
}

func (e *ConfigError) Error() string {
	return fmt.Sprintf("config: field %q: %s", e.Field, e.Message)
}

// TunnelConfig describes a single SSH jump-host tunnel.
type TunnelConfig struct {
	Name               string `yaml:"name"`
	Host               string `yaml:"host"`
	Port               int    `yaml:"port"`
	User               string `yaml:"user"`
	IdentityFile       string `yaml:"identity_file"`
	LocalPort          int    `yaml:"local_port"`
	DialTimeout        int    `yaml:"dial_timeout"`        // seconds; SSH TCP + handshake
	KeepaliveInterval  int    `yaml:"keepalive_interval"`  // seconds between keepalive probes
	KeepaliveMaxFails  int    `yaml:"keepalive_max_fails"` // consecutive failures before reconnect
	ReconnectDelay    int `yaml:"reconnect_delay"`     // initial backoff seconds
	ReconnectMaxDelay int `yaml:"reconnect_max_delay"` // backoff cap seconds
}

// Rule maps a host pattern to a tunnel name or "direct".
type Rule struct {
	Pattern string `yaml:"pattern"`
	Tunnel  string `yaml:"tunnel"`
	Via     string `yaml:"via"` // "direct"
}

// ProxyConfig holds the SOCKS5 router configuration.
type ProxyConfig struct {
	Port  int    `yaml:"port"`
	Rules []Rule `yaml:"rules"`
}

// AdminConfig controls the HTTP admin server.
type AdminConfig struct {
	Port int    `yaml:"port"`
	Bind string `yaml:"bind"`
}

// Config is the root configuration object.
type Config struct {
	Tunnels []TunnelConfig `yaml:"tunnels"`
	Proxy   ProxyConfig    `yaml:"proxy"`
	Admin   AdminConfig    `yaml:"admin"`

	// resolved path, not from YAML
	Path string `yaml:"-"`
}

// Load finds and parses the config file, applying defaults.
func Load(explicit string) (*Config, error) {
	path, err := resolvePath(explicit)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}

	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", path, err)
	}

	cfg.Path = path
	applyDefaults(cfg)

	if err := validate(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

func resolvePath(explicit string) (string, error) {
	candidates := []string{
		explicit,
		os.Getenv("HOPSCOTCH_CONFIG"),
		"/etc/hopscotch/config.yaml",
	}

	home, _ := os.UserHomeDir()
	if home != "" {
		candidates = append(candidates, filepath.Join(home, ".config", "hopscotch", "config.yaml"))
	}
	candidates = append(candidates, "hopscotch.yaml")

	for _, p := range candidates {
		if p == "" {
			continue
		}
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}

	return "", errors.New("no config file found; use --config or set HOPSCOTCH_CONFIG")
}

func applyDefaults(cfg *Config) {
	home, _ := os.UserHomeDir()

	for i := range cfg.Tunnels {
		t := &cfg.Tunnels[i]
		if t.Port == 0 {
			t.Port = DefaultSSHPort
		}
		if t.DialTimeout == 0 {
			t.DialTimeout = DefaultDialTimeout
		}
		if t.KeepaliveInterval == 0 {
			t.KeepaliveInterval = DefaultKeepaliveInterval
		}
		if t.KeepaliveMaxFails == 0 {
			t.KeepaliveMaxFails = DefaultKeepaliveMaxFails
		}
		if t.ReconnectDelay == 0 {
			t.ReconnectDelay = DefaultReconnectDelay
		}
		if t.ReconnectMaxDelay == 0 {
			t.ReconnectMaxDelay = DefaultReconnectMaxDelay
		}
		if home != "" && strings.HasPrefix(t.IdentityFile, "~/") {
			t.IdentityFile = filepath.Join(home, t.IdentityFile[2:])
		}
	}

	if cfg.Proxy.Port == 0 {
		cfg.Proxy.Port = DefaultProxyPort
	}
	if cfg.Admin.Port == 0 {
		cfg.Admin.Port = DefaultAdminPort
	}
	if cfg.Admin.Bind == "" {
		cfg.Admin.Bind = DefaultAdminBind
	}
}

func validate(cfg *Config) error {
	if len(cfg.Tunnels) == 0 {
		return &ConfigError{Field: "tunnels", Message: "at least one tunnel is required"}
	}

	seen := map[string]bool{}
	seenPort := map[int]string{}

	for _, t := range cfg.Tunnels {
		if t.Name == "" {
			return &ConfigError{Field: "tunnels[].name", Message: "name is required"}
		}
		if t.Host == "" {
			return &ConfigError{Field: fmt.Sprintf("tunnels[%s].host", t.Name), Message: "host is required"}
		}
		if t.User == "" {
			return &ConfigError{Field: fmt.Sprintf("tunnels[%s].user", t.Name), Message: "user is required"}
		}
		if t.LocalPort == 0 {
			return &ConfigError{Field: fmt.Sprintf("tunnels[%s].local_port", t.Name), Message: "local_port is required"}
		}
		if seen[t.Name] {
			return &ConfigError{Field: "tunnels[].name", Message: fmt.Sprintf("duplicate tunnel name %q", t.Name)}
		}
		seen[t.Name] = true

		if prev, ok := seenPort[t.LocalPort]; ok {
			return &ConfigError{
				Field:   fmt.Sprintf("tunnels[%s].local_port", t.Name),
				Message: fmt.Sprintf("port %d already used by tunnel %q", t.LocalPort, prev),
			}
		}
		seenPort[t.LocalPort] = t.Name
	}

	if cfg.Proxy.Port == cfg.Admin.Port {
		return &ConfigError{Field: "proxy.port / admin.port", Message: "proxy and admin ports must differ"}
	}

	for _, rule := range cfg.Proxy.Rules {
		if rule.Pattern == "" {
			return &ConfigError{Field: "proxy.rules[].pattern", Message: "pattern is required"}
		}
		if rule.Tunnel == "" && rule.Via == "" {
			return &ConfigError{Field: "proxy.rules[].tunnel", Message: "either tunnel or via is required"}
		}
	}

	return nil
}
