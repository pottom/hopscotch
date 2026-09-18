package vpn

import (
	"testing"
	"time"
)

func TestGatewayDark(t *testing.T) {
	for _, tc := range []struct {
		name   string
		rx, tx uint64
		up     time.Duration
		want   bool
	}{
		{"healthy: replies arrived", 5, 12, 10 * time.Second, false},
		{"one reply is enough to not be dark", 1, 40, time.Minute, false},
		{"dark: sending, nothing back", 0, 8, 20 * time.Second, true},
		{"too early to judge", 0, 8, 10 * time.Second, false},
		{"not enough traffic sent to judge", 0, 2, 30 * time.Second, false},
	} {
		if got := gatewayDark(tc.rx, tc.tx, tc.up); got != tc.want {
			t.Errorf("%s: gatewayDark(rx=%d, tx=%d, up=%v) = %v, want %v", tc.name, tc.rx, tc.tx, tc.up, got, tc.want)
		}
	}
}
