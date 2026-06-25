//go:build !windows

package vpn

import (
	"os/exec"
	"syscall"
)

func setProcGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killProcGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) //nolint:errcheck
	}
}

// killOrphanedProcs kills any processes named name that have been reparented to
// PID 1 (init/launchd), i.e. orphaned when their parent (sudo) was SIGKILLed.
// useSudo should be true when the subprocess was originally launched via sudo —
// the orphan runs as root and requires elevated privileges to kill.
func killOrphanedProcs(name string, useSudo bool) {
	var cmd *exec.Cmd
	if useSudo {
		cmd = exec.Command("sudo", "pkill", "-9", "-P", "1", "-x", name)
	} else {
		cmd = exec.Command("pkill", "-9", "-P", "1", "-x", name)
	}
	_ = cmd.Run() // exit code 1 = no match — not an error
}
