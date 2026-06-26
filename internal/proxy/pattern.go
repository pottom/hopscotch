// Package proxy implements the SOCKS5 routing layer.
package proxy

import (
	"net"
	"strings"
)

// matchPattern reports whether host matches a glob-style pattern.
// Supported forms:
//   - "*"              – matches everything
//   - "*.example.com"  – matches any subdomain
//   - "10.0.1.*"       – matches any host with that IP prefix
//   - "10.0.1.0/24"    – CIDR block; host must be a parseable IP
//   - "exact.host"     – exact match
//
// MatchPattern is the exported form used by the TUI.
func MatchPattern(pattern, host string) bool { return matchPattern(pattern, host) }

// IsCIDR reports whether pattern is in CIDR notation (contains a slash).
func IsCIDR(pattern string) bool { return strings.Contains(pattern, "/") }

func matchPattern(pattern, host string) bool {
	if pattern == "*" {
		return true
	}

	// CIDR block: "10.0.1.0/24"
	if strings.Contains(pattern, "/") {
		_, cidr, err := net.ParseCIDR(pattern)
		if err == nil {
			ip := net.ParseIP(host)
			return ip != nil && cidr.Contains(ip)
		}
	}

	// Prefix wildcard: "10.0.1.*"
	if strings.HasSuffix(pattern, ".*") {
		prefix := strings.TrimSuffix(pattern, "*")
		return strings.HasPrefix(host, prefix)
	}

	// Subdomain wildcard: "*.example.com"
	if strings.HasPrefix(pattern, "*.") {
		suffix := pattern[1:] // ".example.com"
		return strings.HasSuffix(host, suffix) || host == pattern[2:]
	}

	// Generic suffix wildcard: "*b.example.com", "*-prod.internal"
	// The * anchors to the start; anything before the literal suffix matches.
	if strings.HasPrefix(pattern, "*") {
		return strings.HasSuffix(host, pattern[1:])
	}

	return pattern == host
}
