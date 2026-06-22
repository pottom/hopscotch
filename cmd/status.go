package cmd

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"hopscotch/internal/state"
	"hopscotch/internal/tunnel"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show status of running tunnels and proxy",
	RunE: func(cmd *cobra.Command, args []string) error {
		stateMgr, err := state.NewManager()
		if err != nil {
			return err
		}

		st, err := stateMgr.Read()
		if err != nil {
			return fmt.Errorf("hopscotch does not appear to be running: %w", err)
		}

		fmt.Printf("%-10s %-32s %-6s %-14s %-8s %-10s\n",
			"TUNNEL", "HOST", "PORT", "STATUS", "UPTIME", "RECONNECTS")
		fmt.Println("-----------------------------------------------------------------------------------------------")

		for _, t := range st.Tunnels {
			uptime := "-"
			icon := "⟳"
			if t.Status == tunnel.StatusConnected.String() {
				icon = "✓"
				if !t.ConnectedAt.IsZero() {
					uptime = formatDuration(time.Since(t.ConnectedAt))
				}
			}

			fmt.Printf("%-10s %-32s %-6d %-14s %-8s %-10d\n",
				t.Name,
				"-", // host not stored in state; would require config read
				t.LocalPort,
				icon+" "+t.Status,
				uptime,
				t.ReconnectCount,
			)
		}

		fmt.Println()
		fmt.Printf("PROXY      localhost:%d   running\n", st.ProxyPort)
		fmt.Printf("PID        %d\n", st.PID)
		fmt.Printf("UPTIME     %s\n", formatDuration(time.Since(st.StartedAt)))
		return nil
	},
}

func init() {
	rootCmd.AddCommand(statusCmd)
}

func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	if m > 0 {
		return fmt.Sprintf("%dm %ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}
