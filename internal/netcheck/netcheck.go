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
