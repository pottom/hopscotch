package netcheck

import (
	"bufio"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"
)

// DNSServers returns the DNS servers the system currently resolves with,
// refreshed at most every dnsCacheTTL. It reports the real upstream servers,
// never a local stub such as systemd-resolved's 127.0.0.53, so the header
// shows the VPN-pushed resolvers while a VPN is up. Empty when unknown.
func DNSServers() []string {
	dnsCache.mu.Lock()
	defer dnsCache.mu.Unlock()
	if time.Since(dnsCache.at) < dnsCacheTTL {
		return dnsCache.servers
	}
	dnsCache.servers = systemDNSServers()
	dnsCache.at = time.Now()
	return dnsCache.servers
}

const dnsCacheTTL = 5 * time.Second

var dnsCache struct {
	mu      sync.Mutex
	at      time.Time
	servers []string
}

// resolvConfServers reads the nameserver entries of a resolv.conf-style file;
// nil when it can't be read.
func resolvConfServers(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	return parseResolvConf(f)
}

// parseResolvConf returns the nameserver addresses listed in r, in order.
func parseResolvConf(r io.Reader) []string {
	var servers []string
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 || fields[0] != "nameserver" {
			continue
		}
		if ip := net.ParseIP(fields[1]); ip != nil {
			servers = append(servers, fields[1])
		}
	}
	return servers
}

// allLoopback reports whether every address is a local stub resolver.
func allLoopback(servers []string) bool {
	for _, s := range servers {
		if ip := net.ParseIP(s); ip == nil || !ip.IsLoopback() {
			return false
		}
	}
	return len(servers) > 0
}
