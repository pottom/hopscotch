//go:build !darwin

package netcheck

// systemDNSServers reads /etc/resolv.conf. When that only names a local stub
// (systemd-resolved's 127.0.0.53), the upstream servers of every link come
// from the file resolved keeps for exactly this purpose.
func systemDNSServers() []string {
	servers := resolvConfServers("/etc/resolv.conf")
	if len(servers) == 0 || allLoopback(servers) {
		if upstream := resolvConfServers("/run/systemd/resolve/resolv.conf"); len(upstream) > 0 {
			return upstream
		}
	}
	return servers
}
