package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"hopscotch/internal/admin"
	"hopscotch/internal/config"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show status of running tunnels and proxy",
	RunE:  runStatus,
}

func init() {
	rootCmd.AddCommand(statusCmd)
}

func runStatus(cmd *cobra.Command, args []string) error {
	adminPort, err := resolveAdminPort()
	if err != nil {
		return err
	}

	url := fmt.Sprintf("http://127.0.0.1:%d/status", adminPort)
	resp, err := http.Get(url) //nolint:noctx
	if err != nil {
		return fmt.Errorf("hopscotch is not running")
	}
	defer resp.Body.Close()

	var st admin.StatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return fmt.Errorf("decoding status response: %w", err)
	}

	printStatus(st)
	return nil
}

func printStatus(st admin.StatusResponse) {
	icon := "✓"
	if st.Status == "degraded" {
		icon = "!"
	}

	fmt.Printf("hopscotch %s  %s  PID %d  up %s\n\n",
		st.Version, icon+" "+st.Status, st.PID, st.Uptime)

	fmt.Printf("%-24s %-6s %-14s %-8s %s\n",
		"TUNNEL", "PORT", "STATUS", "UPTIME", "RECONNECTS")
	fmt.Println("─────────────────────────────────────────────────────────")

	names := make([]string, 0, len(st.Tunnels))
	for name := range st.Tunnels {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		t := st.Tunnels[name]
		statusIcon := "⟳"
		if t.Status == "connected" {
			statusIcon = "✓"
		}

		uptime := "-"
		if t.UptimeSeconds > 0 {
			uptime = formatDuration(time.Duration(t.UptimeSeconds) * time.Second)
		}

		fmt.Printf("%-24s %-6d %-14s %-8s %d\n",
			name, t.LocalPort, statusIcon+" "+t.Status, uptime, t.ReconnectCount)
	}

	fmt.Println()
	fmt.Printf("PROXY   localhost:%d\n", st.ProxyPort)
	fmt.Printf("ADMIN   localhost:%d\n", st.AdminPort)
}

// resolveAdminPort reads the admin port from the config, falling back to the default.
func resolveAdminPort() (int, error) {
	cfg, err := config.Load(configPath)
	if err == nil {
		return cfg.Admin.Port, nil
	}
	return config.DefaultAdminPort, nil
}

func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh%dm", h, m)
	}
	if m > 0 {
		return fmt.Sprintf("%dm%ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}
