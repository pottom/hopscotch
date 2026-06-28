package netcheck

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// HasInternet reports whether actual internet connectivity exists by attempting
// a TCP connection to 1.1.1.1:53. Faster than an HTTP round-trip and needs no
// external service dependency for the basic check.
func HasInternet() bool {
	conn, err := net.DialTimeout("tcp4", "1.1.1.1:53", 500*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

var publicIP atomic.Value // stores string

// PublicIP returns the last successfully fetched public IP, or empty string if
// show_public_ip is disabled or no successful fetch has happened yet.
func PublicIP() string {
	v, _ := publicIP.Load().(string)
	return v
}

// StartPublicIPWatcher starts a background goroutine that fetches the public IP
// every interval and stores it via publicIP. Stops when ctx is cancelled.
// If the uplink was down and comes back up, a fetch is triggered immediately
// without waiting for the next tick.
func StartPublicIPWatcher(ctx context.Context, interval time.Duration) {
	go func() {
		fetch(ctx)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		wasUp := HasUplink()
		linkCheck := time.NewTicker(2 * time.Second)
		defer linkCheck.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				fetch(ctx)
			case <-linkCheck.C:
				up := HasUplink()
				if up && !wasUp {
					fetch(ctx)
					ticker.Reset(interval)
				}
				wasUp = up
			}
		}
	}()
}

func fetch(ctx context.Context) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.ipify.org", nil)
	if err != nil {
		return
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		publicIP.Store("")
		return
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	if err != nil || resp.StatusCode != http.StatusOK {
		publicIP.Store("")
		return
	}
	ip := strings.TrimSpace(string(body))
	if net.ParseIP(ip) == nil {
		publicIP.Store("")
		return
	}
	publicIP.Store(ip)
}
