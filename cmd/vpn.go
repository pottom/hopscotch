package cmd

import (
	"errors"
	"fmt"
	"os"
	"runtime"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"hopscotch/internal/config"
	"hopscotch/internal/keychain"
)

var vpnCmd = &cobra.Command{
	Use:   "vpn",
	Short: "Manage VPN credentials",
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
	rootCmd.AddCommand(vpnCmd)
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
