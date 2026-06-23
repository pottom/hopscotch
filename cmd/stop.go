package cmd

import (
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"hopscotch/internal/state"
)

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the running hopscotch daemon",
	RunE: func(cmd *cobra.Command, args []string) error {
		stateMgr, err := state.NewManager()
		if err != nil {
			return err
		}

		pid, err := stateMgr.ReadPID()
		if err != nil {
			return fmt.Errorf("hopscotch is not running")
		}

		if !isRunning(pid) {
			stateMgr.Remove()
			return fmt.Errorf("hopscotch is not running (stale PID %d removed)", pid)
		}

		proc, err := os.FindProcess(pid)
		if err != nil {
			return fmt.Errorf("finding process %d: %w", pid, err)
		}

		if err := proc.Signal(syscall.SIGTERM); err != nil {
			return fmt.Errorf("sending SIGTERM to %d: %w", pid, err)
		}

		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if !isRunning(pid) {
				fmt.Printf("hopscotch stopped (PID %d)\n", pid)
				return nil
			}
			time.Sleep(200 * time.Millisecond)
		}

		return fmt.Errorf("process %d did not stop within 5 seconds; try kill -9 %d", pid, pid)
	},
}

func init() {
	rootCmd.AddCommand(stopCmd)
}
