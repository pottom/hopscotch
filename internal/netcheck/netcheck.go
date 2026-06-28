// Package netcheck provides a cross-platform uplink detector.
// It checks whether the kernel can route to the internet by resolving the
// default-route interface via a UDP connect (no packet sent), then verifying
// the interface is up and has a routable IPv4 address.
package netcheck

import (
	"context"
	"net"
	"time"
)

// defaultRouteIP returns the local IP the kernel would use to reach 1.1.1.1,
// or nil if routing fails (no default route, no network).
func defaultRouteIP() net.IP {
	conn, err := net.Dial("udp4", "1.1.1.1:53")
	if err != nil {
		return nil
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP
}

// HasUplink reports whether the machine has a routable internet path.
// It finds the default-route interface via a UDP connect (no packet sent),
// then checks that the interface is up and has a non-link-local IPv4 address.
func HasUplink() bool {
	localIP := defaultRouteIP()
	if localIP == nil {
		return false
	}
	if localIP.IsLoopback() || localIP.IsLinkLocalUnicast() {
		return false
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		return false
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip.Equal(localIP) {
				return true
			}
		}
	}
	return false
}

// UplinkInterface returns the name of the network interface that would be used
// to reach the internet (e.g. "en0", "eth0"). Returns empty string on failure.
func UplinkInterface() string {
	localIP := defaultRouteIP()
	if localIP == nil {
		return ""
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range ifaces {
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip.Equal(localIP) {
				return iface.Name
			}
		}
	}
	return ""
}

// WaitForUplink blocks until HasUplink returns true or ctx is cancelled.
// Returns nil when uplink is detected, ctx.Err() if cancelled.
func WaitForUplink(ctx context.Context) error {
	if HasUplink() {
		return nil
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if HasUplink() {
				return nil
			}
		}
	}
}
