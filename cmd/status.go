package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"
	xterm "golang.org/x/term"

	"hopscotch/internal/admin"
	"hopscotch/internal/config"
	"hopscotch/internal/tui"
)

var (
	plainStatus    bool
	statusUsername string
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show status of running tunnels and proxy",
	RunE:  runStatus,
}

func init() {
	rootCmd.AddCommand(statusCmd)
	statusCmd.Flags().BoolVar(&plainStatus, "plain", false, "print plain text instead of opening the TUI")
	statusCmd.Flags().StringVar(&statusUsername, "username", "", "admin username (prompted if omitted and auth is required)")
}

func runStatus(cmd *cobra.Command, args []string) error {
	adminPort, err := resolveAdminPort()
	if err != nil {
		return err
	}

	base := fmt.Sprintf("http://127.0.0.1:%d", adminPort)
	statusURL := base + "/status"

	client, err := resolveAdminClient(base, statusUsername)
	if err != nil {
		return err
	}

	if !plainStatus && term.IsTerminal(os.Stdout.Fd()) {
		m := tui.New(statusURL, client)
		p := tea.NewProgram(m, tea.WithAltScreen())
		_, err := p.Run()
		return err
	}

	// Non-TTY or --plain: fetch once and print plain text.
	resp, err := client.Get(statusURL) //nolint:noctx
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

// resolveAdminClient returns an authenticated *http.Client ready to use.
// If the admin requires auth it prompts interactively; on failure it returns
// an error so the TUI is never started.
func resolveAdminClient(baseURL, username string) (*http.Client, error) {
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}

	// Probe: do we need auth at all?
	resp, err := client.Get(baseURL + "/status") //nolint:noctx
	if err != nil {
		// Server not running — pass client through, TUI will show the error.
		return client, nil
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		return client, nil
	}

	// Auth required.
	fmt.Fprintln(os.Stderr, "Admin authentication required.")

	if username == "" {
		fmt.Fprint(os.Stderr, "Username: ")
		fmt.Fscan(os.Stdin, &username)
	}

	fmt.Fprint(os.Stderr, "Password: ")
	passwordBytes, err := xterm.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr) // newline after hidden input
	if err != nil {
		return nil, fmt.Errorf("reading password: %w", err)
	}

	// Attempt login.
	loginResp, err := client.PostForm(baseURL+"/api/login", url.Values{
		"username": {username},
		"password": {string(passwordBytes)},
	})
	if err != nil {
		return nil, fmt.Errorf("login request failed: %w", err)
	}
	loginResp.Body.Close()

	// Verify the session cookie works.
	checkResp, err := client.Get(baseURL + "/status") //nolint:noctx
	if err != nil {
		return nil, err
	}
	checkResp.Body.Close()

	if checkResp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("authentication failed — invalid credentials")
	}

	return client, nil
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
