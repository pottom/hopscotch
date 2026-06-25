// Package netcheck provides a simple cross-platform uplink detector.
// It uses only stdlib net.Interfaces — no CGO, no platform-specific syscalls.
package netcheck

import (
	"context"
	"net"
	"time"
)

// HasUplink reports whether at least one non-loopback, non-link-local
// interface is up and has a routable IP address.
func HasUplink() bool {
	ifaces, err := net.Interfaces()
	if err != nil {
		return true // don't block on transient error
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip != nil && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() {
				return true
			}
		}
	}
	return false
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
