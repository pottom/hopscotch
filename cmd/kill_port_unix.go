//go:build !windows

package cmd

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// pidListeningOnPort returns the PID of the process listening on the given TCP
// port, or an error if none is found. Uses lsof which is available on macOS
// and most Linux distributions.
func pidListeningOnPort(port int) (int, error) {
	out, err := exec.Command("lsof", "-ti", fmt.Sprintf("tcp:%d", port), "-sTCP:LISTEN").Output()
	if err != nil {
		return 0, fmt.Errorf("no process found on port %d", port)
	}
	s := strings.TrimSpace(string(out))
	if s == "" {
		return 0, fmt.Errorf("no process found on port %d", port)
	}
	// lsof may return multiple PIDs (one per line) — take the first
	pid, err := strconv.Atoi(strings.SplitN(s, "\n", 2)[0])
	if err != nil {
		return 0, fmt.Errorf("unexpected lsof output: %q", s)
	}
	return pid, nil
}
