//go:build !linux

package vpn

// ifaceCounters is not implemented outside Linux (macOS has no sysfs; parsing
// netstat per probe tick isn't worth it). ok=false makes pollPingHost fall back
// to the plain connect timeout.
func ifaceCounters(name string) (rx, tx uint64, ok bool) {
	return 0, 0, false
}
