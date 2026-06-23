//go:build !windows

package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
)

func daemonize() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("finding executable: %w", err)
	}

	childArgs := []string{"start", "--foreground"}
	if configPath != "" {
		childArgs = append(childArgs, "--config", configPath)
	}

	devNull, err := os.Open(os.DevNull)
	if err != nil {
		return fmt.Errorf("opening /dev/null: %w", err)
	}
	defer devNull.Close()

	child := exec.Command(exe, childArgs...)
	child.Stdin = devNull
	child.Stdout = devNull
	child.Stderr = devNull
	child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := child.Start(); err != nil {
		return fmt.Errorf("starting daemon: %w", err)
	}

	done := make(chan error, 1)
	go func() { done <- child.Wait() }()

	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("daemon exited immediately: %w", err)
		}
		return fmt.Errorf("daemon exited immediately with no error")
	case <-time.After(600 * time.Millisecond):
		fmt.Printf("hopscotch started (PID %d)\n", child.Process.Pid)
		return nil
	}
}
