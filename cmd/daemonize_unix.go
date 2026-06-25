//go:build !windows

package cmd

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
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
	if verbose {
		childArgs = append(childArgs, "--verbose")
	}
	if logFile != "" {
		childArgs = append(childArgs, "--log-file", logFile)
	}

	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("opening /dev/null: %w", err)
	}
	defer devNull.Close()

	// Capture startup output so we can show it if the daemon exits immediately.
	// On success we unlink the file (daemon's fd stays open until it exits).
	startLog, err := os.CreateTemp("", "hopscotch-start-*.log")
	if err != nil {
		startLog = devNull // fall back gracefully
	}

	child := exec.Command(exe, childArgs...)
	child.Stdin = devNull
	child.Stdout = startLog
	child.Stderr = startLog
	child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := child.Start(); err != nil {
		cleanupStartLog(startLog, devNull)
		return fmt.Errorf("starting daemon: %w", err)
	}

	done := make(chan error, 1)
	go func() { done <- child.Wait() }()

	select {
	case exitErr := <-done:
		output := readStartLog(startLog, devNull)
		cleanupStartLog(startLog, devNull)
		if exitErr != nil {
			if output != "" {
				return fmt.Errorf("daemon exited immediately: %w\n\n%s", exitErr, output)
			}
			return fmt.Errorf("daemon exited immediately: %w", exitErr)
		}
		if output != "" {
			return fmt.Errorf("daemon exited immediately (no error)\n\n%s", output)
		}
		return fmt.Errorf("daemon exited immediately with no error")
	case <-time.After(600 * time.Millisecond):
		// Daemon is running — unlink the temp file (daemon's fd keeps the data
		// until it exits, which is fine; we no longer need it).
		cleanupStartLog(startLog, devNull)
		fmt.Printf("hopscotch started (PID %d)\n", child.Process.Pid)
		return nil
	}
}

func readStartLog(f, devNull *os.File) string {
	if f == devNull {
		return ""
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return ""
	}
	b, _ := io.ReadAll(io.LimitReader(f, 8192))
	return strings.TrimSpace(string(b))
}

func cleanupStartLog(f, devNull *os.File) {
	if f == devNull {
		return
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
}
