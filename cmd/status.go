package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"hopscotch/internal/admin"
	"hopscotch/internal/config"
	"hopscotch/internal/tui"
)

var plainStatus bool

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show status of running tunnels and proxy",
	RunE:  runStatus,
}

func init() {
	rootCmd.AddCommand(statusCmd)
	statusCmd.Flags().BoolVar(&plainStatus, "plain", false, "print plain text instead of opening the TUI")
}

func runStatus(cmd *cobra.Command, args []string) error {
	adminPort, err := resolveAdminPort()
	if err != nil {
		return err
	}

	url := fmt.Sprintf("http://127.0.0.1:%d/status", adminPort)

	if !plainStatus && term.IsTerminal(os.Stdout.Fd()) {
		m := tui.New(url)
		p := tea.NewProgram(m, tea.WithAltScreen())
		_, err := p.Run()
		return err
	}

	// Non-TTY or --plain: fetch once and print plain text.
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

// printStatus renders a plain-text status table (used when stdout is not a TTY or --plain).
func printStatus(st admin.StatusResponse) {
	badge := "✓ " + st.Status
	fmt.Printf("hopscotch %s  %s  PID %d  up %s\n", st.Version, badge, st.PID, st.Uptime)

	const (
		wName   = 26
		wStatus = 16
		wUptime = 10
		wRC     = 4
	)

	// VPN section.
	if len(st.VPNs) > 0 {
		fmt.Println()
		sepLen := wName + wStatus + wUptime + wRC
		fmt.Printf("%-*s%-*s%-*s%s\n", wName, "VPN", wStatus, "STATUS", wUptime, "UPTIME", "RC")
		fmt.Println(strings.Repeat("─", sepLen))

		vpnNames := make([]string, 0, len(st.VPNs))
		for name := range st.VPNs {
			vpnNames = append(vpnNames, name)
		}
		sort.Strings(vpnNames)

		for _, name := range vpnNames {
			v := st.VPNs[name]
			uptime := "—"
			if v.UptimeSeconds > 0 {
				uptime = formatDuration(time.Duration(v.UptimeSeconds) * time.Second)
			}
			icon := "○"
			if v.State == "connected" {
				icon = "✓"
			}
			fmt.Printf("%-*s%-*s%-*s%d\n",
				wName, name,
				wStatus, icon+" "+v.State,
				wUptime, uptime,
				v.Reconnects,
			)
		}
	}

	// Tunnel section.
	const wPort = 7
	fmt.Println()
	sepLen := wName + wPort + wStatus + wUptime + len("RECONNECTS")
	fmt.Printf("%-*s%-*s%-*s%-*s%s\n", wName, "TUNNEL", wPort, "PORT", wStatus, "STATUS", wUptime, "UPTIME", "RECONNECTS")
	fmt.Println(strings.Repeat("─", sepLen))

	names := make([]string, 0, len(st.Tunnels))
	for name := range st.Tunnels {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		t := st.Tunnels[name]
		uptime := "—"
		if t.UptimeSeconds > 0 {
			uptime = formatDuration(time.Duration(t.UptimeSeconds) * time.Second)
		}
		icon := "○"
		if t.Status == "connected" {
			icon = "✓"
		}
		fmt.Printf("%-*s%-*d%-*s%-*s%d\n",
			wName, name,
			wPort, t.LocalPort,
			wStatus, icon+" "+t.Status,
			wUptime, uptime,
			t.ReconnectCount,
		)
	}

	fmt.Printf("\nPROXY  :%d    ADMIN  :%d\n", st.ProxyPort, st.AdminPort)
}

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
