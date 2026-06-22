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

	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

var (
	trustPort       int
	trustKnownHosts string
)

var trustCmd = &cobra.Command{
	Use:   "trust <host>",
	Short: "Fetch and add a host's SSH fingerprint to known_hosts",
	Args:  cobra.ExactArgs(1),
	RunE:  runTrust,
}

func init() {
	trustCmd.Flags().IntVar(&trustPort, "port", 22, "SSH port")
	trustCmd.Flags().StringVar(&trustKnownHosts, "known-hosts", "", "path to known_hosts file (default: ~/.ssh/known_hosts)")
	rootCmd.AddCommand(trustCmd)
}

func runTrust(cmd *cobra.Command, args []string) error {
	host := args[0]
	addr := fmt.Sprintf("%s:%d", host, trustPort)

	knownHostsPath, err := resolveKnownHosts(trustKnownHosts)
	if err != nil {
		return err
	}

	// Check if already trusted.
	if alreadyTrusted(host, trustPort, knownHostsPath) {
		fmt.Printf("%s is already in %s\n", host, knownHostsPath)
		return nil
	}

	// Dial and capture the host key without verifying.
	var capturedKey ssh.PublicKey
	cfg := &ssh.ClientConfig{
		User: "hopscotch-trust",
		Auth: []ssh.AuthMethod{ssh.Password("")},
		HostKeyCallback: func(hostname string, remote net.Addr, key ssh.PublicKey) error {
			capturedKey = key
			return nil // accept any key during discovery
		},
		Timeout: 10 * time.Second,
	}

	conn, err := ssh.Dial("tcp", addr, cfg)
	if err != nil && capturedKey == nil {
		return fmt.Errorf("connecting to %s: %w", addr, err)
	}
	if conn != nil {
		conn.Close()
	}

	if capturedKey == nil {
		return fmt.Errorf("failed to retrieve host key from %s", addr)
	}

	fingerprint := fingerprintSHA256(capturedKey)

	fmt.Printf("Host:        %s:%d\n", host, trustPort)
	fmt.Printf("Fingerprint: %s\n", fingerprint)
	fmt.Printf("Type:        %s\n", capturedKey.Type())
	fmt.Println()
	fmt.Print("Add to known_hosts? [y/N]: ")

	reader := bufio.NewReader(os.Stdin)
	answer, _ := reader.ReadString('\n')
	answer = strings.TrimSpace(strings.ToLower(answer))

	if answer != "y" && answer != "yes" {
		fmt.Println("Aborted.")
		return nil
	}

	if err := appendKnownHost(host, trustPort, capturedKey, knownHostsPath); err != nil {
		return fmt.Errorf("writing known_hosts: %w", err)
	}

	fmt.Printf("✓ Added %s to %s\n", host, knownHostsPath)
	return nil
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
	_, err := knownhosts.New(knownHostsPath)
	if err != nil {
		return false
	}
	// A quick check: try parsing — if the file has an entry for this host,
	// the callback would not error on a matching key. We just check file existence.
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
