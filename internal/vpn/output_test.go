package vpn

import (
	"strings"
	"testing"
)

// openconnect's progress output, as it appears on stdout during a connect.
const connectOutput = `POST https://vpn.example.com/
Got CONNECT response: HTTP/1.1 200 OK
CSTP connected. DPD 30, Keepalive 20
Set up tun device tun77
Connected as 10.4.0.26, using SSL, with DTLS in progress
`

// With ping_host configured, a "Connected as" line must not mark the VPN
// connected: after a switch the session is up long before internal hosts
// answer, and tunnels would dial into an unrouted network. Without ping_host
// the line is the best signal there is and must promote the state.
func TestWatchOutputPromotesOnlyWithoutPingHost(t *testing.T) {
	for _, tc := range []struct {
		name     string
		pingHost string
		want     State
	}{
		{"ping_host is authoritative", "10.0.0.1:53", StateConnecting},
		{"no ping_host", "", StateConnected},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newConnection(connConfig{Name: "test", Server: "https://vpn.example.com", PingHost: tc.pingHost})
			c.setState(StateConnecting)

			c.watchOutput(strings.NewReader(connectOutput), make(chan struct{}))

			if got := c.State(); got != tc.want {
				t.Errorf("state = %v, want %v", got, tc.want)
			}
			if got := c.Stats().TunIface; got != "tun77" {
				t.Errorf("TunIface = %q, want tun77 (from the stdout \"Set up tun device\" line)", got)
			}
		})
	}
}

// Once runOnce has returned, late output from an orphaned process must only be
// logged, never change the state of the next attempt.
func TestWatchOutputIgnoresStateAfterDone(t *testing.T) {
	c := newConnection(connConfig{Name: "test", Server: "https://vpn.example.com"})
	c.setState(StateConnecting)

	done := make(chan struct{})
	close(done)
	c.watchOutput(strings.NewReader(connectOutput), done)

	if got := c.State(); got != StateConnecting {
		t.Errorf("state = %v, want %v (output after done must not promote)", got, StateConnecting)
	}
}
