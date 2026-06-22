package cmd

import (
	"fmt"
	"os"
	"syscall"

	"github.com/charmbracelet/log"
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
			return fmt.Errorf("hopscotch does not appear to be running: %w", err)
		}

		proc, err := os.FindProcess(pid)
		if err != nil {
			return fmt.Errorf("finding process %d: %w", pid, err)
		}

		if err := proc.Signal(syscall.SIGTERM); err != nil {
			return fmt.Errorf("sending SIGTERM to %d: %w", pid, err)
		}

		log.Info("sent SIGTERM", "pid", pid)
		fmt.Printf("hopscotch (PID %d) is stopping.\n", pid)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(stopCmd)
}
