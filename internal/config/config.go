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
	Hint    string // optional fix suggestion shown to the user
}

func (e *ConfigError) Error() string {
	s := fmt.Sprintf("config: field %q: %s", e.Field, e.Message)
	if e.Hint != "" {
		s += "\n  → " + e.Hint
	}
	return s
}

// TunnelConfig describes a single SSH jump-host tunnel.
type TunnelConfig struct {
	Name               string `yaml:"name"`
	Host               string `yaml:"host"`
	Port               int    `yaml:"port"`
	User               string `yaml:"user"`
	IdentityFile       string `yaml:"identity_file"`
	KnownHostsFile     string `yaml:"known_hosts_file"`
	LocalPort          int    `yaml:"local_port"`
	DialTimeout        int    `yaml:"dial_timeout"`        // seconds; SSH TCP + handshake
	KeepaliveInterval  int    `yaml:"keepalive_interval"`  // seconds between keepalive probes
	KeepaliveMaxFails  int    `yaml:"keepalive_max_fails"` // consecutive failures before reconnect
	ReconnectDelay    int  `yaml:"reconnect_delay"`     // initial backoff seconds
	ReconnectMaxDelay int  `yaml:"reconnect_max_delay"` // backoff cap seconds
	ForcePTY          bool     `yaml:"force_pty"`           // open a PTY shell session to satisfy SPS/SCB channel policy
	RequiresVPN       string   `yaml:"requires_vpn"`        // wait for this VPN before connecting
	PreConnect        []string `yaml:"pre_connect"`         // commands to run before each dial attempt
}

// VPNConfig describes a VPN connection managed as a subprocess.
type VPNConfig struct {
	Name              string   `yaml:"name"`
	Type              string   `yaml:"type"`         // currently only "openconnect"
	Server            string   `yaml:"server"`
	User              string   `yaml:"user"`
	Binary            string   `yaml:"binary"`       // path to openconnect binary; default: "openconnect" (PATH)
	AuthGroup         string   `yaml:"authgroup"`    // --authgroup value (Cisco AnyConnect groups)
	PasswordEnv       string   `yaml:"password_env"` // env var containing the password
	PasswordCmd       string   `yaml:"password_cmd"` // shell command whose stdout is the password
	Certificate       string   `yaml:"certificate"`  // path to client cert (cert auth)
	Key               string   `yaml:"key"`          // path to private key (cert auth)
	PingHost          string   `yaml:"ping_host"`      // host[:port] TCP-probed to detect connectivity
	ExtraArgs         []string `yaml:"extra_args"`     // passed through to openconnect verbatim
	PreConnect        []string `yaml:"pre_connect"`    // commands to run before each connection attempt
	PostDisconnect    []string `yaml:"post_disconnect"` // commands to run after each VPN disconnect
	Sudo              bool     `yaml:"sudo"`           // prepend sudo (needed on most platforms)
	DNSResolver       string   `yaml:"dns_resolver"`   // DNS server for pre-connect hostname resolution; default: 1.1.1.1:53
	ReconnectDelay    int      `yaml:"reconnect_delay"`
	ReconnectMaxDelay int      `yaml:"reconnect_max_delay"`
}

// Rule maps a host pattern to a tunnel name or "direct".
type Rule struct {
	Pattern string `yaml:"pattern"`
	Tunnel  string `yaml:"tunnel"`
	Via     string `yaml:"via"` // "direct"
}

// ProxyConfig holds the SOCKS5 router configuration.
type ProxyConfig struct {
	Port      int    `yaml:"port"`
	Bind      string `yaml:"bind"`       // listen address; default 0.0.0.0
	Rules     []Rule `yaml:"rules"`
	NoProxy   string `yaml:"no_proxy"`   // passed to NO_PROXY / no_proxy on shell enable
	ShellIcon string `yaml:"shell_icon"` // icon shown in HOPSCOTCH_ACTIVE; default ⇢
}

// AdminConfig controls the HTTP admin server.
type AdminConfig struct {
	Port int    `yaml:"port"`
	Bind string `yaml:"bind"`
}

// Config is the root configuration object.
type Config struct {
	Tunnels []TunnelConfig `yaml:"tunnels"`
	VPNs    []VPNConfig    `yaml:"vpn"`
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
	if explicit != "" {
		if _, err := os.Stat(explicit); err != nil {
			return "", fmt.Errorf("config file not found: %s", explicit)
		}
		return explicit, nil
	}

	candidates := []string{os.Getenv("HOPSCOTCH_CONFIG")}

	// binary directory
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "hopscotch.yaml"))
	}

	// user config dir
	if home, _ := os.UserHomeDir(); home != "" {
		candidates = append(candidates, filepath.Join(home, ".config", "hopscotch", "config.yaml"))
	}

	for _, p := range candidates {
		if p == "" {
			continue
		}
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}

	return "", errors.New("no config file found; place hopscotch.yaml next to the binary, use --config, or create ~/.config/hopscotch/config.yaml")
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
		if home != "" {
			if strings.HasPrefix(t.IdentityFile, "~/") {
				t.IdentityFile = filepath.Join(home, t.IdentityFile[2:])
			}
			if strings.HasPrefix(t.KnownHostsFile, "~/") {
				t.KnownHostsFile = filepath.Join(home, t.KnownHostsFile[2:])
			}
		}
	}

	for i := range cfg.VPNs {
		v := &cfg.VPNs[i]
		if v.Type == "" {
			v.Type = DefaultVPNType
		}
		if v.ReconnectDelay == 0 {
			v.ReconnectDelay = DefaultVPNReconnectDelay
		}
		if v.ReconnectMaxDelay == 0 {
			v.ReconnectMaxDelay = DefaultVPNReconnectMaxDelay
		}
		if home != "" {
			if strings.HasPrefix(v.Certificate, "~/") {
				v.Certificate = filepath.Join(home, v.Certificate[2:])
			}
			if strings.HasPrefix(v.Key, "~/") {
				v.Key = filepath.Join(home, v.Key[2:])
			}
		}
	}

	if cfg.Proxy.Port == 0 {
		cfg.Proxy.Port = DefaultProxyPort
	}
	if cfg.Proxy.Bind == "" {
		cfg.Proxy.Bind = "0.0.0.0"
	}
	if cfg.Proxy.ShellIcon == "" {
		cfg.Proxy.ShellIcon = "⇢"
	}
	if cfg.Admin.Port == 0 {
		cfg.Admin.Port = DefaultAdminPort
	}
	if cfg.Admin.Bind == "" {
		cfg.Admin.Bind = DefaultAdminBind
	}
}

// managedVPNFlags maps each openconnect flag (and its short form) that hopscotch
// controls via an explicit config field to the field name shown in error messages.
// If a user puts one of these in extra_args it would be applied twice.
var managedVPNFlags = map[string]string{
	"--authgroup":      "authgroup",
	"--user":          "user",
	"-u":              "user",
	"--passwd-on-stdin": "(automatic — set when a password is available)",
	"--certificate":   "certificate",
	"-c":              "certificate",
	"--sslkey":        "key",
	"-k":              "key",
}

// validateVPNExtraArgs returns an error if extra_args contains a flag that is
// already managed by an explicit VPN config field.
func validateVPNExtraArgs(v VPNConfig) error {
	for _, arg := range v.ExtraArgs {
		// Strip optional value part for flags like "--user=foo".
		flag := strings.SplitN(arg, "=", 2)[0]
		if field, managed := managedVPNFlags[flag]; managed {
			return &ConfigError{
				Field: fmt.Sprintf("vpn[%s].extra_args", v.Name),
				Message: fmt.Sprintf("%q is already managed via the %q config field; remove it from extra_args", arg, field),
			}
		}
	}
	return nil
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

	// Validate VPN definitions.
	vpnNames := make(map[string]bool, len(cfg.VPNs))
	for _, v := range cfg.VPNs {
		if v.Name == "" {
			return &ConfigError{Field: "vpn[].name", Message: "name is required"}
		}
		if v.Server == "" {
			return &ConfigError{Field: fmt.Sprintf("vpn[%s].server", v.Name), Message: "server is required"}
		}
		if v.Type != "openconnect" {
			return &ConfigError{Field: fmt.Sprintf("vpn[%s].type", v.Name), Message: fmt.Sprintf("unsupported type %q; only \"openconnect\" is supported", v.Type)}
		}
		if vpnNames[v.Name] {
			return &ConfigError{Field: "vpn[].name", Message: fmt.Sprintf("duplicate vpn name %q", v.Name)}
		}
		vpnNames[v.Name] = true
		if err := validateVPNExtraArgs(v); err != nil {
			return err
		}
	}

	// Validate requires_vpn references.
	for _, t := range cfg.Tunnels {
		if t.RequiresVPN != "" && !vpnNames[t.RequiresVPN] {
			return &ConfigError{
				Field:   fmt.Sprintf("tunnels[%s].requires_vpn", t.Name),
				Message: fmt.Sprintf("vpn %q is not defined in the vpn section", t.RequiresVPN),
			}
		}
	}

	for i, rule := range cfg.Proxy.Rules {
		if rule.Pattern == "" {
			return &ConfigError{Field: fmt.Sprintf("proxy.rules[%d].pattern", i+1), Message: "pattern is required"}
		}
		if rule.Tunnel == "" && rule.Via == "" {
			return &ConfigError{Field: fmt.Sprintf("proxy.rules[%d].tunnel", i+1), Message: "either tunnel or via is required"}
		}
		if rule.Via != "" && rule.Via != "direct" && rule.Via != "block" && rule.Tunnel != "" {
			// via is only for special values; tunnel name goes in tunnel field
		}
		if rule.Via != "" && rule.Via != "direct" && rule.Via != "block" {
			return &ConfigError{Field: fmt.Sprintf("proxy.rules[%d].via", i+1), Message: fmt.Sprintf("via must be %q, %q, or empty (use tunnel field for tunnel names)", "direct", "block")}
		}
		if err := ValidatePattern(rule.Pattern); err != nil {
			hint := patternErrorHint(cfg.Path, rule.Pattern, i+1)
			return &ConfigError{
				Field:   fmt.Sprintf("proxy.rules[%d]", i+1),
				Message: fmt.Sprintf("invalid pattern %q: %s", rule.Pattern, err.Error()),
				Hint:    hint,
			}
		}
	}

	return nil
}

// patternErrorHint builds a user-friendly fix hint for a bad routing pattern.
// It tries to locate the exact line number in the config file.
func patternErrorHint(path, pattern string, ruleNum int) string {
	if path == "" {
		return fmt.Sprintf("fix rule %d in your config file and restart hopscotch", ruleNum)
	}
	if data, err := os.ReadFile(path); err == nil {
		for i, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, "pattern:") && strings.Contains(line, pattern) {
				return fmt.Sprintf("line %d in %s — fix the pattern and restart hopscotch", i+1, path)
			}
		}
	}
	return fmt.Sprintf("fix rule %d in %s and restart hopscotch", ruleNum, path)
}
