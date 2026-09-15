//go:build !windows

package vpn

import (
	"os/exec"
	"syscall"
	"time"
)

func setProcGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killProcGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) //nolint:errcheck
	}
}

// killOrphanedProcs terminates any running openconnect matching pattern (see
// procPattern) left over from a previous abrupt shutdown. We use SIGTERM (not
// SIGKILL) first so openconnect has a chance to send a clean disconnect to the
// VPN server — without this the server keeps the session open and blocks the
// next reconnect attempt.
//
// Matching is by full command line, never by process name alone: with two VPNs
// configured both processes are named openconnect, and a name-only pkill from
// one VPN would tear down the other. We also do NOT restrict to -P 1: when
// launched via sudo the parent is sudo, not PID 1, and -P 1 would miss it.
func killOrphanedProcs(pattern string, useSudo bool) {
	_ = pkill(useSudo, "-TERM", pattern) // exit code 1 = no match — not an error
	// Brief pause to let openconnect send its goodbye packet before force-kill.
	time.Sleep(300 * time.Millisecond)
	_ = pkill(useSudo, "-9", pattern)
}

// terminateProcs sends SIGTERM to every process whose command line matches
// pattern. Unlike killProcGroup (which targets sudo's PGID), this reaches
// openconnect even if it created its own process group — giving it a chance to
// send a proper disconnect packet to the VPN server before we force-kill it.
func terminateProcs(pattern string, useSudo bool) {
	_ = pkill(useSudo, "-TERM", pattern)
}

func pkill(useSudo bool, signal, pattern string) error {
	args := []string{signal, "-f", pattern}
	if useSudo {
		return exec.Command("sudo", append([]string{"pkill"}, args...)...).Run()
	}
	return exec.Command("pkill", args...).Run()
}
