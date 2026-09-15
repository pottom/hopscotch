//go:build linux

package vpn

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIfaceCountersReadsSysfs(t *testing.T) {
	root := t.TempDir()
	stats := filepath.Join(root, "tun0", "statistics")
	if err := os.MkdirAll(stats, 0o755); err != nil {
		t.Fatal(err)
	}
	for file, value := range map[string]string{"rx_packets": "0\n", "tx_packets": "42\n"} {
		if err := os.WriteFile(filepath.Join(stats, file), []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old := sysClassNet
	sysClassNet = root
	defer func() { sysClassNet = old }()

	rx, tx, ok := ifaceCounters("tun0")
	if !ok || rx != 0 || tx != 42 {
		t.Fatalf("ifaceCounters(tun0) = rx %d tx %d ok %v, want rx 0 tx 42 ok true", rx, tx, ok)
	}

	// A missing interface or a name that would escape the stats tree must
	// report ok=false, never zero counters that look like a dark session.
	for _, name := range []string{"tun9", "", "..", "../tun0", "tun0/../../etc"} {
		if _, _, ok := ifaceCounters(name); ok {
			t.Errorf("ifaceCounters(%q) ok = true, want false", name)
		}
	}
}
