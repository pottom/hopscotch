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

// killOrphanedProcs terminates any running instances of name left over from a
// previous abrupt shutdown. We use SIGTERM (not SIGKILL) so openconnect has a
// chance to send a clean disconnect to the VPN server — without this the
// server keeps the session open and blocks the next reconnect attempt.
//
// We intentionally do NOT restrict to -P 1 (children of init): when launched
// via sudo the process tree is launchd→sudo→openconnect, so openconnect's
// direct parent is sudo, not PID 1, and -P 1 would silently miss it.
func killOrphanedProcs(name string, useSudo bool) {
	var term, kill *exec.Cmd
	if useSudo {
		term = exec.Command("sudo", "pkill", "-TERM", "-x", name)
		kill = exec.Command("sudo", "pkill", "-9", "-x", name)
	} else {
		term = exec.Command("pkill", "-TERM", "-x", name)
		kill = exec.Command("pkill", "-9", "-x", name)
	}
	_ = term.Run() // exit code 1 = no match — not an error
	// Brief pause to let openconnect send its goodbye packet before force-kill.
	time.Sleep(300 * time.Millisecond)
	_ = kill.Run()
}

// terminateByName sends SIGTERM to all processes named name.
// Unlike killProcGroup (which targets sudo's PGID), this reaches openconnect
// even if it created its own process group — giving it a chance to send a
// proper disconnect packet to the VPN server before we force-kill it.
func terminateByName(name string, useSudo bool) {
	var cmd *exec.Cmd
	if useSudo {
		cmd = exec.Command("sudo", "pkill", "-TERM", "-x", name)
	} else {
		cmd = exec.Command("pkill", "-TERM", "-x", name)
	}
	_ = cmd.Run()
}
