package cmd

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/pottom/hopscotch/internal/config"
	"github.com/pottom/hopscotch/internal/security"
)

var (
	tunnelAddHost               string
	tunnelAddPort               int
	tunnelAddUser               string
	tunnelAddIdentityFile       string
	tunnelAddLocalPort          int
	tunnelAddRequiresVPN        string
	tunnelAddAutoPauseThreshold int
	tunnelAddAutoResumeAfter    int
	tunnelAddYes                bool
)

var tunnelCmd = &cobra.Command{
	Use:   "tunnel",
	Short: "Manage tunnels in the config file",
}

var tunnelAddCmd = &cobra.Command{
	Use:   "add [name]",
	Short: "Add a new SSH tunnel to the config file",
	Long: `Adds a new tunnel to config.yaml. Prompts for any field not already given
as a flag; pass -y to skip prompting and use flag values/defaults as-is
(fails if a required field is still missing — useful for scripting).

The daemon must be reloaded to pick up the new tunnel: send SIGHUP,
or restart with 'hopscotch start --restart'.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runTunnelAdd,
}

func init() {
	tunnelAddCmd.Flags().StringVar(&tunnelAddHost, "host", "", "SSH server hostname or IP")
	tunnelAddCmd.Flags().IntVar(&tunnelAddPort, "port", 0, "SSH server port (default 22)")
	tunnelAddCmd.Flags().StringVar(&tunnelAddUser, "user", "", "SSH username")
	tunnelAddCmd.Flags().StringVar(&tunnelAddIdentityFile, "identity-file", "", "path to private key (omit to use SSH agent)")
	tunnelAddCmd.Flags().IntVar(&tunnelAddLocalPort, "local-port", 0, "local SOCKS5 port for this tunnel")
	tunnelAddCmd.Flags().StringVar(&tunnelAddRequiresVPN, "requires-vpn", "", "name of a vpn entry this tunnel waits for")
	tunnelAddCmd.Flags().IntVar(&tunnelAddAutoPauseThreshold, "auto-pause-threshold", 0, "consecutive failures before auto-pausing (0 disables)")
	tunnelAddCmd.Flags().IntVar(&tunnelAddAutoResumeAfter, "auto-resume-after", 0, "seconds before retrying an auto-paused tunnel (0 disables)")
	tunnelAddCmd.Flags().BoolVarP(&tunnelAddYes, "yes", "y", false, "don't prompt; use flag values/defaults as-is")
	tunnelCmd.AddCommand(tunnelAddCmd)
	rootCmd.AddCommand(tunnelCmd)
}

func runTunnelAdd(_ *cobra.Command, args []string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	name := ""
	if len(args) > 0 {
		name = args[0]
	}

	reader := bufio.NewReader(os.Stdin)
	good := lipgloss.NewStyle().Foreground(lipgloss.Color("#34d399"))
	muted := lipgloss.NewStyle().Foreground(lipgloss.Color("#475569"))

	existingNames := make(map[string]bool, len(cfg.Tunnels))
	existingPorts := make(map[int]string, len(cfg.Tunnels))
	maxLocalPort := 0
	for _, t := range cfg.Tunnels {
		existingNames[t.Name] = true
		existingPorts[t.LocalPort] = t.Name
		if t.LocalPort > maxLocalPort {
			maxLocalPort = t.LocalPort
		}
	}
	suggestedLocalPort := max(maxLocalPort+1, 1080)

	for {
		name, err = fieldString(reader, "Tunnel name", name, tunnelAddYes, true)
		if err != nil {
			return err
		}
		if existingNames[name] {
			if tunnelAddYes {
				return fmt.Errorf("tunnel %q already exists", name)
			}
			fmt.Printf("  tunnel %q already exists, choose another name\n", name)
			name = ""
			continue
		}
		break
	}

	host, err := fieldString(reader, "SSH host (hostname or IP)", tunnelAddHost, tunnelAddYes, true)
	if err != nil {
		return err
	}

	if tunnelAddPort == 0 {
		tunnelAddPort = 22
	}
	port, err := fieldInt(reader, "SSH port", tunnelAddPort, tunnelAddYes)
	if err != nil {
		return err
	}

	user, err := fieldString(reader, "SSH user", tunnelAddUser, tunnelAddYes, true)
	if err != nil {
		return err
	}

	identityFile, err := fieldString(reader, "Identity file (blank = use SSH agent)", tunnelAddIdentityFile, tunnelAddYes, false)
	if err != nil {
		return err
	}

	var localPort int
	for {
		if tunnelAddLocalPort != 0 {
			suggestedLocalPort = tunnelAddLocalPort
		}
		localPort, err = fieldInt(reader, "Local SOCKS5 port", suggestedLocalPort, tunnelAddYes)
		if err != nil {
			return err
		}
		if prev, ok := existingPorts[localPort]; ok {
			if tunnelAddYes {
				return fmt.Errorf("local_port %d already used by tunnel %q", localPort, prev)
			}
			fmt.Printf("  port %d already used by tunnel %q, choose another\n", localPort, prev)
			tunnelAddLocalPort = 0
			suggestedLocalPort++
			continue
		}
		break
	}

	requiresVPN := tunnelAddRequiresVPN
	if len(cfg.VPNs) > 0 {
		vpnNames := make([]string, len(cfg.VPNs))
		for i, v := range cfg.VPNs {
			vpnNames[i] = v.Name
		}
		requiresVPN, err = fieldString(reader,
			fmt.Sprintf("Requires VPN (blank = none; available: %s)", strings.Join(vpnNames, ", ")),
			requiresVPN, tunnelAddYes, false)
		if err != nil {
			return err
		}
	}

	autoPauseThreshold, err := fieldInt(reader, "Auto-pause after N consecutive failures (0 disables)", tunnelAddAutoPauseThreshold, tunnelAddYes)
	if err != nil {
		return err
	}

	autoResumeAfter := tunnelAddAutoResumeAfter
	if autoPauseThreshold > 0 {
		autoResumeAfter, err = fieldInt(reader, "Auto-resume after N seconds (0 disables, stays paused until manual resume)", tunnelAddAutoResumeAfter, tunnelAddYes)
		if err != nil {
			return err
		}
	}

	newTunnel := config.TunnelConfig{
		Name:               name,
		Host:               host,
		Port:               port,
		User:               user,
		IdentityFile:       identityFile,
		LocalPort:          localPort,
		RequiresVPN:        requiresVPN,
		AutoPauseThreshold: autoPauseThreshold,
		AutoResumeAfter:    autoResumeAfter,
	}

	candidate := *cfg
	candidate.Tunnels = append(append([]config.TunnelConfig{}, cfg.Tunnels...), newTunnel)

	// Validate against a defaulted copy, not candidate itself: ApplyDefaults
	// expands a leading "~/" to an absolute home path and fills in numeric
	// defaults (port 22, timeouts, ...) in place, and Load() already reapplies
	// the same defaults on every read. Running it on candidate would bake the
	// expanded/defaulted values into the file we're about to write, replacing
	// the user's portable "~/..." with this machine's literal home directory.
	// Deep-copy VPNs (not just Tunnels) into this scratch copy too, since a
	// bare struct-copy of Config only copies slice headers — ApplyDefaults
	// would otherwise mutate cfg.VPNs's shared backing array in place.
	validated := candidate
	validated.Tunnels = append([]config.TunnelConfig{}, candidate.Tunnels...)
	validated.VPNs = append([]config.VPNConfig{}, candidate.VPNs...)
	config.ApplyDefaults(&validated)
	if err := config.Validate(&validated); err != nil {
		return fmt.Errorf("new tunnel is invalid, not saved: %w", err)
	}

	if identityFile != "" {
		expanded := identityFile
		if home, herr := os.UserHomeDir(); herr == nil && home != "" && strings.HasPrefix(expanded, "~/") {
			expanded = filepath.Join(home, expanded[2:])
		}
		if err := security.CheckKeyFiles([]string{expanded}); err != nil {
			fmt.Println(muted.Render("  warning: " + err.Error()))
		}
	}

	if err := config.WriteConfig(&candidate, cfg.Path); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}

	fmt.Println(good.Render(fmt.Sprintf("✓ tunnel %q added to %s", name, cfg.Path)))
	fmt.Println(muted.Render("  next: hopscotch trust " + name + "   (then reload: SIGHUP, or 'hopscotch start --restart')"))
	return nil
}

// fieldString prompts for a string field, pre-filled with flagVal as the
// default (shown and accepted on Enter). In yes mode, no prompt is shown and
// flagVal is used as-is, failing if required and empty.
func fieldString(reader *bufio.Reader, label, flagVal string, yes, required bool) (string, error) {
	if yes {
		if required && flagVal == "" {
			return "", fmt.Errorf("%s is required (pass it as a flag when using -y)", label)
		}
		return flagVal, nil
	}
	for {
		if flagVal != "" {
			fmt.Printf("%s [%s]: ", label, flagVal)
		} else {
			fmt.Printf("%s: ", label)
		}
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return "", fmt.Errorf("reading input: %w", err)
		}
		// ReadString returns whatever it read before hitting EOF (e.g. the
		// last line of piped input with no trailing newline) alongside the
		// error — only bail on a real read error, not on EOF with data.
		line = strings.TrimSpace(line)
		if line == "" {
			line = flagVal
		}
		if line == "" && required {
			if err == io.EOF {
				return "", fmt.Errorf("%s is required, but reached end of input", label)
			}
			fmt.Println("  required, try again")
			continue
		}
		return line, nil
	}
}

// fieldInt prompts for an int field, pre-filled with defaultVal. In yes mode,
// no prompt is shown and defaultVal is used as-is.
func fieldInt(reader *bufio.Reader, label string, defaultVal int, yes bool) (int, error) {
	if yes {
		return defaultVal, nil
	}
	for {
		fmt.Printf("%s [%d]: ", label, defaultVal)
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return 0, fmt.Errorf("reading input: %w", err)
		}
		line = strings.TrimSpace(line)
		if line == "" {
			return defaultVal, nil
		}
		n, convErr := strconv.Atoi(line)
		if convErr != nil {
			if err == io.EOF {
				return 0, fmt.Errorf("%s: %q is not a number, and reached end of input", label, line)
			}
			fmt.Println("  not a number, try again")
			continue
		}
		return n, nil
	}
}
