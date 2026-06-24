//go:build windows

package vpn

import "os/exec"

func setProcGroup(cmd *exec.Cmd) {}

func killProcGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		cmd.Process.Kill() //nolint:errcheck
	}
}
