package config

import (
	"reflect"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const vpnNamesConfig = `
tunnels:
  - name: none
    host: n.example.com
    user: u
    local_port: 1079
  - name: single
    host: a.example.com
    user: u
    local_port: 1080
    requires_vpn: vpn-a
  - name: any
    host: b.example.com
    user: u
    local_port: 1081
    requires_vpn: [vpn-a, vpn-b]
vpn:
  - name: vpn-a
    server: https://a.example.com
  - name: vpn-b
    server: https://b.example.com
proxy:
  port: 8080
`

func TestLoad_RequiresVPNScalarOrList(t *testing.T) {
	cfg, err := Load(writeConfig(t, vpnNamesConfig))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	want := map[string]VPNNames{"none": nil, "single": {"vpn-a"}, "any": {"vpn-a", "vpn-b"}}
	for _, tun := range cfg.Tunnels {
		if !reflect.DeepEqual(tun.RequiresVPN, want[tun.Name]) {
			t.Errorf("tunnel %s: requires_vpn = %#v, want %#v", tun.Name, tun.RequiresVPN, want[tun.Name])
		}
	}
}

// WriteConfig rewrites the whole file; a config that only ever used the scalar
// form must not come back as one-element lists.
func TestVPNNamesMarshalKeepsScalarForm(t *testing.T) {
	cfg, err := Load(writeConfig(t, vpnNamesConfig))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	out, err := yaml.Marshal(cfg.Tunnels)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	for _, want := range []*regexp.Regexp{
		regexp.MustCompile(`requires_vpn: ""\n`),
		regexp.MustCompile(`requires_vpn: vpn-a\n`),
		regexp.MustCompile(`requires_vpn:\n\s+- vpn-a\n\s+- vpn-b\n`),
	} {
		if !want.Match(out) {
			t.Errorf("marshalled config does not match %s:\n%s", want, out)
		}
	}
}

func TestLoad_UnknownVPNInRequiresVPNList(t *testing.T) {
	path := writeConfig(t, strings.Replace(vpnNamesConfig, "[vpn-a, vpn-b]", "[vpn-a, nonexistent-vpn]", 1))
	if _, err := Load(path); err == nil {
		t.Fatal("expected error for unknown vpn inside a requires_vpn list, got nil")
	}
}
