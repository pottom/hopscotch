package vpn

import "time"

// A "dark" session is one where openconnect brings the tunnel up (interface,
// address, routes) but the gateway never sends a single packet back. Measured
// live on two VPNs into the same network (2026-09-15):
//   - it follows bursts of short sessions (fast back-and-forth switching,
//     restarts in quick succession), and more sessions keep it stuck;
//   - an immediate restart never fixed it (9 of 9 stayed dark), while a retry
//     after the normal reconnect delay sometimes did;
//   - once several sessions in a row were dark, only a quiet period of about
//     two minutes without new sessions brought the gateway back.
const (
	// darkDetectAfter and darkMinTx: a tunnel that has been up this long and
	// has sent at least this many packets (ping_host probes alone send one per
	// second) without receiving any is dark. Healthy sessions receive their
	// first reply within about two seconds.
	darkDetectAfter = 6 * time.Second
	darkMinTx       = 3

	// darkCooldownBase/Max bound the quiet period enforced after dark sessions.
	darkCooldownBase = 2 * time.Minute
	darkCooldownMax  = 8 * time.Minute
)

// gatewayDark reports whether a tunnel that has been up for `up` with the given
// packet counters is receiving nothing back from the gateway.
func gatewayDark(rx, tx uint64, up time.Duration) bool {
	return up >= darkDetectAfter && tx >= darkMinTx && rx == 0
}

// darkCooldown returns the quiet period to keep before the next session after
// `streak` dark sessions in a row: none after the first (the normal reconnect
// delay applies, and a retry after it sometimes works), then 2, 4 and at most
// 8 minutes.
func darkCooldown(streak int) time.Duration {
	if streak < 2 {
		return 0
	}
	shift := streak - 2
	if shift >= 3 { // 2m << 2 == 8m already reaches the cap
		return darkCooldownMax
	}
	if d := darkCooldownBase << shift; d < darkCooldownMax {
		return d
	}
	return darkCooldownMax
}
