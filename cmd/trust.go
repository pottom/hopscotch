package cmd

import (
	"bufio"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/log"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"hopscotch/internal/config"
)

var (
	trustPort       int
	trustKnownHosts string
	trustYes        bool
)

var trustCmd = &cobra.Command{
	Use:   "trust <tunnel-name|host|all>",
	Short: "Fetch and add SSH host fingerprints to known_hosts",
	Long: `Connects to the host, captures its public key, and adds it to known_hosts.

The argument can be:
  - A tunnel name from the config (host and port are read automatically)
  - A raw hostname or IP address
  - "all" to trust every tunnel defined in the config at once`,
	Args: cobra.ExactArgs(1),
	RunE: runTrust,
}

func init() {
	trustCmd.Flags().IntVar(&trustPort, "port", 0, "SSH port (default: from config or 22)")
	trustCmd.Flags().StringVar(&trustKnownHosts, "known-hosts", "", "path to known_hosts file (default: ~/.ssh/known_hosts)")
	trustCmd.Flags().BoolVarP(&trustYes, "yes", "y", false, "auto-confirm all fingerprints without prompting")
	rootCmd.AddCommand(trustCmd)
}

func runTrust(cmd *cobra.Command, args []string) error {
	knownHostsPath, err := resolveKnownHosts(trustKnownHosts)
	if err != nil {
		return err
	}

	if args[0] == "all" {
		return trustAll(knownHostsPath)
	}

	host, port, label := resolveTrustTarget(args[0])
	return trustOne(host, port, label, knownHostsPath)
}

func trustAll(knownHostsPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	var errs []string
	for _, t := range cfg.Tunnels {
		fmt.Printf("── %s ──\n", t.Name)
		if err := trustOne(t.Host, t.Port, t.Name, knownHostsPath); err != nil {
			log.Error("failed to trust host", "tunnel", t.Name, "err", err)
			errs = append(errs, fmt.Sprintf("%s: %v", t.Name, err))
		}
		fmt.Println()
	}

	if len(errs) > 0 {
		return fmt.Errorf("%d tunnel(s) failed:\n  %s", len(errs), strings.Join(errs, "\n  "))
	}
	return nil
}

func trustOne(host string, port int, label, knownHostsPath string) error {
	if alreadyTrusted(host, port, knownHostsPath) {
		fmt.Printf("%s (%s:%d) is already trusted\n", label, host, port)
		return nil
	}

	addr := fmt.Sprintf("%s:%d", host, port)

	var capturedKey ssh.PublicKey
	sshCfg := &ssh.ClientConfig{
		User: "hopscotch-trust",
		Auth: []ssh.AuthMethod{ssh.Password("")},
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			capturedKey = key
			return nil
		},
		Timeout: 10 * time.Second,
	}

	conn, err := ssh.Dial("tcp", addr, sshCfg)
	if err != nil && capturedKey == nil {
		return fmt.Errorf("connecting to %s: %w", addr, err)
	}
	if conn != nil {
		conn.Close()
	}
	if capturedKey == nil {
		return fmt.Errorf("failed to retrieve host key from %s", addr)
	}

	fmt.Printf("Tunnel:      %s\n", label)
	fmt.Printf("Host:        %s:%d\n", host, port)
	fmt.Printf("Fingerprint: %s\n", fingerprintSHA256(capturedKey))
	fmt.Printf("Type:        %s\n", capturedKey.Type())
	fmt.Println()

	if !trustYes {
		fmt.Print("Add to known_hosts? [y/N]: ")
		reader := bufio.NewReader(os.Stdin)
		answer, _ := reader.ReadString('\n')
		answer = strings.TrimSpace(strings.ToLower(answer))
		if answer != "y" && answer != "yes" {
			fmt.Println("Skipped.")
			return nil
		}
	}

	if err := appendKnownHost(host, port, capturedKey, knownHostsPath); err != nil {
		return fmt.Errorf("writing known_hosts: %w", err)
	}

	fmt.Printf("✓ Added %s (%s) to %s\n", label, host, knownHostsPath)
	return nil
}

// resolveTrustTarget looks up arg as a tunnel name in the config.
// If found, returns the tunnel's host and port. Otherwise treats arg as a raw host.
func resolveTrustTarget(arg string) (host string, port int, label string) {
	flagPort := trustPort

	cfg, err := config.Load(configPath)
	if err == nil {
		for _, t := range cfg.Tunnels {
			if t.Name == arg {
				p := t.Port
				if flagPort != 0 {
					p = flagPort
				}
				return t.Host, p, t.Name
			}
		}
	}

	p := flagPort
	if p == 0 {
		p = 22
	}
	return arg, p, arg
}

func resolveKnownHosts(override string) (string, error) {
	if override != "" {
		return override, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("finding home dir: %w", err)
	}
	return filepath.Join(home, ".ssh", "known_hosts"), nil
}

func alreadyTrusted(host string, port int, knownHostsPath string) bool {
	data, err := os.ReadFile(knownHostsPath)
	if err != nil {
		return false
	}
	needle := host
	if port != 22 {
		needle = fmt.Sprintf("[%s]:%d", host, port)
	}
	return strings.Contains(string(data), needle)
}

func appendKnownHost(host string, port int, key ssh.PublicKey, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()

	hostEntry := host
	if port != 22 {
		hostEntry = fmt.Sprintf("[%s]:%d", host, port)
	}

	line := knownhosts.Line([]string{hostEntry}, key)
	_, err = fmt.Fprintln(f, line)
	return err
}

func fingerprintSHA256(key ssh.PublicKey) string {
	hash := sha256.Sum256(key.Marshal())
	return "SHA256:" + base64.StdEncoding.EncodeToString(hash[:])
}
