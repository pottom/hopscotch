package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/pottom/hopscotch/internal/config"
	"github.com/pottom/hopscotch/internal/keychain"
	"github.com/pottom/hopscotch/internal/state"
	"github.com/pottom/hopscotch/internal/vpn"
)

var vpnCmd = &cobra.Command{
	Use:   "vpn",
	Short: "VPN credentials and connection statistics",
}

var vpnStatsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Show recorded VPN sessions and what the dark-session retry policy learned",
	Long: `Reads the session history the daemon records in the cache directory
(vpn-sessions.jsonl: the last 30 days, at most 500 sessions) and prints, per VPN,
how sessions ended and how retries after a "dark" session (tunnel up, gateway
returning no traffic) worked out for each wait. The last line is the wait the
daemon would choose after a dark session right now.

Deleting the file resets what was learned.`,
	Args: cobra.NoArgs,
	RunE: runVPNStats,
}

var vpnPasswordCmd = &cobra.Command{
	Use:   "password <vpn-name>",
	Short: "Store or update the password for a VPN in the OS keychain",
	Long: `Prompts for a password and stores it securely in the OS keychain
(macOS Keychain, Linux Secret Service, Windows Credential Manager).

hopscotch start will read the password from the keychain automatically.
Run this command again to update a stored password.`,
	Args: cobra.ExactArgs(1),
	RunE: runVPNPassword,
}

func init() {
	vpnCmd.AddCommand(vpnPasswordCmd)
	vpnCmd.AddCommand(vpnStatsCmd)
	rootCmd.AddCommand(vpnCmd)
}

func runVPNStats(cmd *cobra.Command, _ []string) error {
	stateMgr, err := state.NewManager()
	if err != nil {
		return fmt.Errorf("state manager: %w", err)
	}
	path := filepath.Join(filepath.Dir(stateMgr.PIDFile()), vpn.HistoryFileName)
	writeVPNStats(cmd.OutOrStdout(), path, vpn.ReadReports(path, time.Now()))
	return nil
}

func writeVPNStats(w io.Writer, path string, reports []vpn.Report) {
	if len(reports) == 0 {
		fmt.Fprintf(w, "No VPN sessions recorded yet (%s).\n", path)
		return
	}
	fmt.Fprintf(w, "Session history: %s\n", path)
	for _, r := range reports {
		fmt.Fprintf(w, "\n%s: %d sessions (ok %d, dark %d, failed %d, cut %d), last %s\n",
			r.VPN, r.Sessions, r.OK, r.Dark, r.Failed, r.Cut, r.Last.Local().Format("2006-01-02 15:04"))
		fmt.Fprintln(w, "  retries after a dark session:")
		for _, wait := range r.RetriesAfterDark {
			fmt.Fprintf(w, "    wait ~%-4s worked %d/%d  (estimate %.0f%%)\n",
				shortDuration(wait.Wait), wait.Worked, wait.Tried, wait.SuccessRate*100)
		}
		fmt.Fprintf(w, "  next wait after a dark session: %s (%s)\n", shortDuration(r.NextWait), r.NextReason)
	}
}

// shortDuration prints 15s, 30s, 1m, 2m rather than 1m0s.
func shortDuration(d time.Duration) string {
	s := d.Round(time.Second).String()
	if strings.HasSuffix(s, "m0s") {
		return strings.TrimSuffix(s, "0s")
	}
	return s
}

func runVPNPassword(_ *cobra.Command, args []string) error {
	name := args[0]
	muted := lipgloss.NewStyle().Foreground(lipgloss.Color("#475569"))
	good := lipgloss.NewStyle().Foreground(lipgloss.Color("#34d399"))

	fmt.Printf("VPN %q password: ", name)
	raw, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		return fmt.Errorf("reading password: %w", err)
	}
	if len(raw) == 0 {
		return errors.New("password cannot be empty")
	}

	if err := keychain.SetVPNPassword(name, string(raw)); err != nil {
		return err
	}
	fmt.Println(good.Render("✓ password stored") + muted.Render("  vpn: "+name+" · keychain: "+keychainLabel()))
	return nil
}

// promptVPNPassword prompts for the password of a single VPN interactively,
// stores it in the keychain, and returns it. Called by start before daemonizing.
func promptVPNPassword(name string) (string, error) {
	fmt.Printf("VPN %q password: ", name)
	raw, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		return "", fmt.Errorf("reading password for vpn %q: %w", name, err)
	}
	if len(raw) == 0 {
		return "", fmt.Errorf("password for vpn %q cannot be empty", name)
	}
	if err := keychain.SetVPNPassword(name, string(raw)); err != nil {
		return "", fmt.Errorf("storing password for vpn %q: %w", name, err)
	}
	return string(raw), nil
}

// ensureVPNPasswords checks each VPN config that needs a password.
// If not in the keychain yet, prompts the user and stores it.
// Called by runStart before daemonizing so the prompt always has a terminal.
func ensureVPNPasswords(vpns []config.VPNConfig) error {
	for _, v := range vpns {
		// Skip VPNs that use env var or certificate auth.
		if v.PasswordEnv != "" || v.Certificate != "" {
			continue
		}
		if _, err := keychain.GetVPNPassword(v.Name); err == nil {
			continue // already stored
		}
		fmt.Printf("hopscotch: VPN %q password not found in keychain.\n", v.Name)
		if _, err := promptVPNPassword(v.Name); err != nil {
			return err
		}
		fmt.Printf("stored — run 'hopscotch vpn password %s' to update it later\n", v.Name)
	}
	return nil
}

// checkVPNSudo verifies that sudo is available without a password for each
// command the VPN subsystem needs to run (openconnect binary + pre_connect
// commands that use sudo). Called before daemonizing so the terminal is available.
//
// On Windows sudo semantics differ; the check is skipped.
func checkVPNSudo(vpns []config.VPNConfig) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	for _, v := range vpns {
		if !v.Sudo {
			continue
		}

		binary := v.Binary
		if binary == "" {
			binary = "openconnect"
		}
		if err := sudoCheck(binary); err != nil {
			hint := sudoHint(binary)
			return fmt.Errorf(
				"vpn %q: sudo access for %q requires a password (daemon cannot prompt)\n  Fix: run 'sudo visudo' and add:\n    %s",
				v.Name, binary, hint,
			)
		}

		for _, cmdStr := range v.PreConnect {
			exe := sudoCmdExe(cmdStr)
			if exe == "" {
				continue
			}
			if err := sudoCheck(exe); err != nil {
				hint := sudoHint(exe)
				return fmt.Errorf(
					"vpn %q: pre_connect %q requires a sudo password (daemon cannot prompt)\n  Fix: run 'sudo visudo' and add:\n    %s",
					v.Name, cmdStr, hint,
				)
			}
		}
	}
	return nil
}

// sudoCheck runs `sudo -n -l <exe>` non-interactively to verify that
// the executable can be invoked via sudo without a password prompt.
func sudoCheck(exe string) error {
	cmd := exec.Command("sudo", "-n", "-l", exe)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run()
}

// sudoHint returns a sudoers line hint for the given executable, using the
// full resolved path when available.
func sudoHint(exe string) string {
	user := os.Getenv("USER")
	if user == "" {
		user = "YOUR_USER"
	}
	if full, err := exec.LookPath(exe); err == nil {
		exe = full
	}
	return fmt.Sprintf("%s ALL=(ALL) NOPASSWD: %s", user, exe)
}

// sudoCmdExe extracts the executable name from a command string that starts
// with "sudo". Returns "" if the command does not invoke sudo.
func sudoCmdExe(cmdStr string) string {
	fields := strings.Fields(cmdStr)
	i := 0
	if len(fields) == 0 || fields[0] != "sudo" {
		return ""
	}
	i++ // skip "sudo"
	// skip sudo flags and their arguments
	for i < len(fields) && strings.HasPrefix(fields[i], "-") {
		flag := fields[i]
		i++
		// flags that take a value argument
		if flag == "-u" || flag == "-g" || flag == "-C" || flag == "-p" || flag == "-r" || flag == "-t" {
			i++
		}
	}
	if i >= len(fields) {
		return ""
	}
	return fields[i]
}

func keychainLabel() string {
	switch runtime.GOOS {
	case "linux":
		return "Secret Service"
	case "windows":
		return "Credential Manager"
	default:
		return "Keychain"
	}
}
