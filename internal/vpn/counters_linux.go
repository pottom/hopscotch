//go:build linux

package vpn

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// sysClassNet is where Linux exposes per-interface statistics; a variable so
// tests can point it at a fake tree.
var sysClassNet = "/sys/class/net"

// ifaceCounters returns the received and transmitted packet counts of a
// network interface. ok is false when they can't be read (interface gone,
// unexpected name), in which case callers must not draw conclusions.
func ifaceCounters(name string) (rx, tx uint64, ok bool) {
	if name == "" || name == "." || name == ".." || strings.ContainsRune(name, '/') {
		return 0, 0, false
	}
	stats := filepath.Join(sysClassNet, name, "statistics")
	rx, rxErr := readCounter(filepath.Join(stats, "rx_packets"))
	tx, txErr := readCounter(filepath.Join(stats, "tx_packets"))
	return rx, tx, rxErr == nil && txErr == nil
}

func readCounter(path string) (uint64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
}
