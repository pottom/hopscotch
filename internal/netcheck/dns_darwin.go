package netcheck

import (
	"bufio"
	"os/exec"
	"strings"
)

// systemDNSServers asks scutil for the default resolver (#1), which is what
// vpnc-script rewrites when a VPN pushes DNS servers. /etc/resolv.conf is the
// fallback when scutil is unavailable.
func systemDNSServers() []string {
	if out, err := exec.Command("scutil", "--dns").Output(); err == nil {
		if servers := parseScutilDNS(string(out)); len(servers) > 0 {
			return servers
		}
	}
	return resolvConfServers("/etc/resolv.conf")
}

// parseScutilDNS returns the nameservers of the first resolver block in
// `scutil --dns` output.
func parseScutilDNS(out string) []string {
	var servers []string
	inFirst := false
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(line, "resolver #"):
			if inFirst {
				return servers
			}
			inFirst = true
		case inFirst && strings.HasPrefix(line, "nameserver["):
			if _, addr, ok := strings.Cut(line, ":"); ok {
				servers = append(servers, strings.TrimSpace(addr))
			}
		}
	}
	return servers
}
