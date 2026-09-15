package cmd

import (
	"bufio"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/log"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/pottom/hopscotch/internal/config"
)

var (
	trustPort       int
	trustKnownHosts string
	trustYes        bool
)

// trustDialTimeout bounds each per-algorithm dial, handshake included.
const trustDialTimeout = 10 * time.Second

var trustCmd = &cobra.Command{
	Use:   "trust <tunnel-name|host|all>",
	Short: "Fetch and add SSH host fingerprints to known_hosts",
	Long: `Connects to the host, fetches every host key type it offers (ED25519,
ECDSA, RSA), and adds the ones not yet pinned to known_hosts.

Storing all key types matters because OpenSSH clients may negotiate a different
type than hopscotch did; with only one type pinned they report "REMOTE HOST
IDENTIFICATION HAS CHANGED". If known_hosts already pins a different key of the
same type, nothing is added for that host and the command fails.

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

// stdinReader is shared across hosts so `trust all` with piped answers does
// not lose input buffered by a previous per-host reader.
var stdinReader = bufio.NewReader(os.Stdin)

func trustOne(host string, port int, label, knownHostsPath string) error {
	confirm := func(prompt string) bool {
		if trustYes {
			return true
		}
		fmt.Print(prompt)
		answer, _ := stdinReader.ReadString('\n')
		answer = strings.TrimSpace(strings.ToLower(answer))
		return answer == "y" || answer == "yes"
	}
	return trustHost(os.Stdout, host, port, label, knownHostsPath, trustDialTimeout, confirm)
}

// trustHost fetches every host key the server offers, compares each against
// known_hosts, and appends the missing types after confirmation. A pinned key
// of the same type that differs from the presented one aborts the whole host:
// nothing is written and an error is returned.
func trustHost(out io.Writer, host string, port int, label, knownHostsPath string, timeout time.Duration, confirm func(prompt string) bool) error {
	addr := net.JoinHostPort(host, strconv.Itoa(port))

	keys, err := fetchHostKeys(addr, timeout)
	if err != nil {
		return err
	}

	plan, err := classifyHostKeys(knownHostsPath, host, port, keys)
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "Tunnel:      %s\n", label)
	fmt.Fprintf(out, "Host:        %s\n", addr)

	if len(plan.mismatches) > 0 {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "WARNING: HOST KEY MISMATCH - the server presented a different key than the one pinned in known_hosts.")
		fmt.Fprintln(out, "This may be a machine-in-the-middle attack, or the host may have been rebuilt.")
		for _, m := range plan.mismatches {
			if m.revoked {
				fmt.Fprintf(out, "  %s key %s is marked @revoked at %s:%d\n",
					m.presented.Type(), fingerprintSHA256(m.presented), m.pinned.Filename, m.pinned.Line)
				continue
			}
			fmt.Fprintf(out, "  %s: server presented %s, pinned at %s:%d is %s\n",
				m.presented.Type(), fingerprintSHA256(m.presented),
				m.pinned.Filename, m.pinned.Line, fingerprintSHA256(m.pinned.Key))
		}
		fmt.Fprintln(out, "Nothing was added for this host. Verify the new fingerprint out of band before removing the pinned line.")
		return fmt.Errorf("host key mismatch for %s: known_hosts pins a different key; nothing added", addr)
	}

	for _, k := range plan.pinned {
		fmt.Fprintf(out, "Pinned:      %-20s %s\n", k.Type(), fingerprintSHA256(k))
	}

	if len(plan.missing) == 0 {
		fmt.Fprintf(out, "%s (%s) is already fully trusted\n", label, addr)
		return nil
	}

	for _, k := range plan.missing {
		fmt.Fprintf(out, "New:         %-20s %s\n", k.Type(), fingerprintSHA256(k))
	}
	fmt.Fprintln(out)

	if !confirm(fmt.Sprintf("Add %d key(s) to known_hosts? [y/N]: ", len(plan.missing))) {
		fmt.Fprintln(out, "Skipped.")
		return nil
	}

	if err := appendKnownHost(host, port, plan.missing, knownHostsPath); err != nil {
		return fmt.Errorf("writing known_hosts: %w", err)
	}

	fmt.Fprintf(out, "✓ Added %d key(s) for %s (%s) to %s\n", len(plan.missing), label, addr, knownHostsPath)
	return nil
}

// hostKeyFamilies are dialed one at a time, each restricting negotiation to a
// single key type, so the server has to reveal the key it holds for that type
// (the same approach as ssh-keyscan). RSA keys are stored as ssh-rsa but
// negotiated via the SHA-2 signature algorithms modern servers still accept.
var hostKeyFamilies = []struct {
	name  string
	algos []string
}{
	{"ED25519", []string{ssh.KeyAlgoED25519}},
	{"ECDSA", []string{ssh.KeyAlgoECDSA256, ssh.KeyAlgoECDSA384, ssh.KeyAlgoECDSA521}},
	{"RSA", []string{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256}},
}

// errHostKeyCaptured aborts the handshake as soon as the host key is known,
// so no authentication is ever attempted.
var errHostKeyCaptured = errors.New("host key captured")

// fetchHostKeys returns one key per host key family the server at addr
// supports. Unsupported families are skipped; it fails only when no key at all
// could be retrieved.
func fetchHostKeys(addr string, timeout time.Duration) ([]ssh.PublicKey, error) {
	var keys []ssh.PublicKey
	var errs []string

	for _, fam := range hostKeyFamilies {
		key, err := fetchHostKey(addr, fam.algos, timeout)
		if key != nil {
			keys = append(keys, key)
			continue
		}

		var opErr *net.OpError
		if errors.As(err, &opErr) && opErr.Op == "dial" {
			// The host is unreachable; the remaining families would only
			// repeat the same timeout.
			return nil, fmt.Errorf("connecting to %s: %w", addr, err)
		}
		if err != nil && strings.Contains(err.Error(), "no common algorithm for host key") {
			continue
		}
		errs = append(errs, fmt.Sprintf("%s: %v", fam.name, err))
	}

	if len(keys) == 0 {
		if len(errs) > 0 {
			return nil, fmt.Errorf("failed to retrieve host key from %s: %s", addr, strings.Join(errs, "; "))
		}
		return nil, fmt.Errorf("failed to retrieve host key from %s: server offers no supported host key type", addr)
	}
	return keys, nil
}

func fetchHostKey(addr string, algos []string, timeout time.Duration) (ssh.PublicKey, error) {
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	// ClientConfig.Timeout only covers the TCP connect; bound the handshake too.
	_ = conn.SetDeadline(time.Now().Add(timeout))

	var captured ssh.PublicKey
	cfg := &ssh.ClientConfig{
		User:              "hopscotch-trust",
		HostKeyAlgorithms: algos,
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			captured = key
			return errHostKeyCaptured
		},
		Timeout: timeout,
	}

	c, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if c != nil {
		ssh.NewClient(c, chans, reqs).Close()
	}
	if captured != nil {
		return captured, nil
	}
	if err == nil {
		err = errors.New("handshake completed without presenting a host key")
	}
	return nil, err
}

type keyMismatch struct {
	presented ssh.PublicKey
	pinned    knownhosts.KnownKey
	revoked   bool
}

type hostKeyPlan struct {
	pinned     []ssh.PublicKey // presented and already in known_hosts
	missing    []ssh.PublicKey // type not pinned for this host yet
	mismatches []keyMismatch   // same type pinned with a different key, or revoked
}

// classifyHostKeys checks keys against known_hosts with the knownhosts matcher
// itself, so hashed entries, patterns and [host]:port forms are honoured.
// Never compare known_hosts contents by string search.
func classifyHostKeys(knownHostsPath, host string, port int, keys []ssh.PublicKey) (hostKeyPlan, error) {
	var plan hostKeyPlan

	cb, err := loadKnownHosts(knownHostsPath)
	if err != nil {
		return plan, err
	}

	addr := net.JoinHostPort(host, strconv.Itoa(port))
	// knownhosts prefers the address argument; remote only has to parse.
	remote := &net.TCPAddr{IP: net.IPv4zero, Port: port}

	for _, key := range keys {
		err := cb(addr, remote, key)
		if err == nil {
			plan.pinned = append(plan.pinned, key)
			continue
		}

		var revErr *knownhosts.RevokedError
		if errors.As(err, &revErr) {
			plan.mismatches = append(plan.mismatches, keyMismatch{presented: key, pinned: revErr.Revoked, revoked: true})
			continue
		}

		var keyErr *knownhosts.KeyError
		if !errors.As(err, &keyErr) {
			return plan, fmt.Errorf("checking %s against %s: %w", addr, knownHostsPath, err)
		}

		mismatched := false
		for _, want := range keyErr.Want {
			if want.Key.Type() == key.Type() {
				plan.mismatches = append(plan.mismatches, keyMismatch{presented: key, pinned: want})
				mismatched = true
			}
		}
		if !mismatched {
			plan.missing = append(plan.missing, key)
		}
	}
	return plan, nil
}

// loadKnownHosts treats a missing file as empty, since trust creates it.
func loadKnownHosts(path string) (ssh.HostKeyCallback, error) {
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return knownhosts.New()
	}
	cb, err := knownhosts.New(path)
	if err != nil {
		return nil, fmt.Errorf("loading known_hosts %s: %w", path, err)
	}
	return cb, nil
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

func appendKnownHost(host string, port int, keys []ssh.PublicKey, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()

	hostEntry := host
	if port != 22 {
		hostEntry = fmt.Sprintf("[%s]:%d", host, port)
	}

	var b strings.Builder
	// Don't glue the first new entry onto a last line lacking its newline.
	if info, err := f.Stat(); err == nil && info.Size() > 0 {
		last := make([]byte, 1)
		if _, err := f.ReadAt(last, info.Size()-1); err == nil && last[0] != '\n' {
			b.WriteByte('\n')
		}
	}
	for _, key := range keys {
		b.WriteString(knownhosts.Line([]string{hostEntry}, key))
		b.WriteByte('\n')
	}
	if _, err := f.WriteString(b.String()); err != nil {
		return err
	}
	return f.Close()
}

func fingerprintSHA256(key ssh.PublicKey) string {
	hash := sha256.Sum256(key.Marshal())
	return "SHA256:" + base64.StdEncoding.EncodeToString(hash[:])
}
