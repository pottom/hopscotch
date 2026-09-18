package vpn

import "time"

// A "dark" session is one where openconnect brings the tunnel up (interface,
// address, routes) but the gateway never sends a single packet back. Measured
// live on two VPNs into the same network (2026-09-15):
//   - dark sessions clustered after bursts of short sessions (fast switching,
//     quick restarts), and an immediate restart never fixed one;
//   - yet a retry some seconds later often did — including a manual
//     pause/resume 11 s after two dark sessions in a row, which connected in
//     1 s while a fixed two-minute cooldown was still counting down.
//
// So how long to wait before the next session is learned from the recorded
// history (retrypolicy.go) instead of fixed; only bursts are hard-limited
// (sessiongate.go and darkRetryFloor).
const (
	// darkDetectAfter and darkMinTx: a tunnel that has been up this long and
	// has sent at least this many packets (ping_host probes alone send one per
	// second) without receiving any is dark. Healthy sessions receive their
	// first reply within about two seconds, but a session can also sit for
	// 15 s with its routes not yet installed and then come up at once
	// (measured 2026-09-19), so the verdict waits half of the default
	// connect_timeout rather than jumping at the first quiet seconds.
	darkDetectAfter = 15 * time.Second
	darkMinTx       = 3
)

// gatewayDark reports whether a tunnel that has been up for `up` with the given
// packet counters is receiving nothing back from the gateway.
func gatewayDark(rx, tx uint64, up time.Duration) bool {
	return up >= darkDetectAfter && tx >= darkMinTx && rx == 0
}
