// Package proxy implements the SOCKS5 routing layer.
package proxy

import "strings"

// matchPattern reports whether host matches a glob-style pattern.
// Supported forms:
//   - "*"             – matches everything
//   - "*.example.com" – matches any subdomain
//   - "10.0.1.*"      – matches any host with that IP prefix
//   - "exact.host"    – exact match
// MatchPattern is the exported form used by the TUI.
func MatchPattern(pattern, host string) bool { return matchPattern(pattern, host) }

func matchPattern(pattern, host string) bool {
	if pattern == "*" {
		return true
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

	return pattern == host
}
