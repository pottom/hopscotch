// Package netcheck provides a cross-platform uplink detector.
// It uses a TCP dial to a well-known host rather than interface enumeration,
// which avoids false positives from tunnel interfaces (utun, VPN tun, VM bridges).
package netcheck

import (
	"context"
	"net"
	"time"
)

// HasUplink reports whether the machine has an active internet path by
// attempting a short TCP connection to 1.1.1.1:53. Returns true on error
// from the dial setup itself (not a connection refusal) to avoid blocking.
func HasUplink() bool {
	conn, err := net.DialTimeout("tcp4", "1.1.1.1:53", 500*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// UplinkInterface returns the name of the network interface that would be used
// to reach the internet (e.g. "en0", "eth0"). Returns empty string on failure.
// Uses a UDP "connection" — no packet is sent, the kernel just resolves routing.
func UplinkInterface() string {
	conn, err := net.Dial("udp4", "1.1.1.1:53")
	if err != nil {
		return ""
	}
	defer conn.Close()
	localIP := conn.LocalAddr().(*net.UDPAddr).IP
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
