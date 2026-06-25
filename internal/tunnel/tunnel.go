package tunnel

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/log"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"

	"hopscotch/internal/config"
	"hopscotch/internal/netcheck"
)

// Clock abstracts time to allow synctest-based testing of reconnect logic.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

type realClock struct{}

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// Tunnel manages a single SSH connection that exposes a local SOCKS5 port.
type Tunnel struct {
	cfg            config.TunnelConfig
	clock          Clock
	vpnGate        func(ctx context.Context) error // non-nil when requires_vpn is set
	vpnIsConnected func() bool                     // non-nil when requires_vpn is set; instant state check
	stats          atomic.Value // holds Stats (without traffic counters)
	client         *ssh.Client  // guarded by the reconnect loop (single goroutine writer)
	ptySession     *ssh.Session // held open when force_pty is set; closed after keepalive exits
	// Traffic counters — always-incrementing, read by Stats().
	bytesIn     atomic.Uint64
	bytesOut    atomic.Uint64
	activeConns atomic.Int64
}

// New creates a Tunnel with a real system clock.
func New(cfg config.TunnelConfig) *Tunnel {
	return NewWithGate(cfg, nil, nil)
}

// NewWithGate creates a Tunnel whose reconnect loop waits for gate before each dial.
// gate is called at the start of every connect attempt; a non-nil return aborts the tunnel.
// isConnected is polled while the tunnel is connected to detect gate loss immediately.
func NewWithGate(cfg config.TunnelConfig, gate func(ctx context.Context) error, isConnected func() bool) *Tunnel {
	t := &Tunnel{cfg: cfg, clock: realClock{}, vpnGate: gate, vpnIsConnected: isConnected}
	t.stats.Store(Stats{Status: StatusConnecting, LocalPort: cfg.LocalPort, Host: fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)})
	return t
}

// Stats returns a snapshot of the tunnel's current metrics including traffic.
func (t *Tunnel) Stats() Stats {
	s := t.stats.Load().(Stats)
	s.RequiresVPN = t.cfg.RequiresVPN
	s.BytesIn = t.bytesIn.Load()
	s.BytesOut = t.bytesOut.Load()
	s.ActiveConns = t.activeConns.Load()
	return s
}

// Name returns the tunnel's configured name.
func (t *Tunnel) Name() string { return t.cfg.Name }

// DialContext dials a TCP address through the SSH tunnel.
// Implements socks5.Dialer and proxy.Dialer.
func (t *Tunnel) DialContext(_ context.Context, network, addr string) (net.Conn, error) {
	c := t.client
	if c == nil {
		return nil, fmt.Errorf("tunnel %q is not connected", t.cfg.Name)
	}
	conn, err := c.Dial(network, addr)
	if err != nil {
		if isTCPForwardingDenied(err) {
			log.Error("TCP forwarding denied by SSH server",
				"tunnel", t.cfg.Name,
				"addr", addr,
				"hint", "ask your admin to set AllowTcpForwarding yes in sshd_config",
			)
		}
		return nil, err
	}
	t.activeConns.Add(1)
	return &countingConn{Conn: conn, tunnel: t}, nil
}


// countingConn wraps net.Conn to track bytes transferred and active connection count.
type countingConn struct {
	net.Conn
	tunnel *Tunnel
	once   sync.Once
}

func (c *countingConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	if n > 0 {
		c.tunnel.bytesIn.Add(uint64(n))
	}
	return n, err
}

func (c *countingConn) Write(b []byte) (int, error) {
	n, err := c.Conn.Write(b)
	if n > 0 {
		c.tunnel.bytesOut.Add(uint64(n))
	}
	return n, err
}

func (c *countingConn) Close() error {
	c.once.Do(func() { c.tunnel.activeConns.Add(-1) })
	return c.Conn.Close()
}

// isTCPForwardingDenied reports whether err indicates the SSH server refused
// to open a direct-tcpip channel (AllowTcpForwarding no).
func isTCPForwardingDenied(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "unexpected packet in response to channel open") ||
		strings.Contains(s, "administratively prohibited") ||
		strings.Contains(s, "open failed")
}

// Run establishes the SSH tunnel, keeps it alive, and reconnects on failure.
// Blocks until ctx is cancelled.
func (t *Tunnel) Run(ctx context.Context) error {
	backoff := newBackoff(
		time.Duration(t.cfg.ReconnectDelay)*time.Second,
		time.Duration(t.cfg.ReconnectMaxDelay)*time.Second,
	)

	for {
		// Wait for VPN if this tunnel has a dependency.
		if t.vpnGate != nil {
			s := t.Stats()
			s.LastError = "waiting for VPN: " + t.cfg.RequiresVPN
			s.NextReconnectAt = time.Time{} // clear stale countdown from previous delay
			t.stats.Store(s)

			log.Info("tunnel waiting for vpn", "tunnel", t.cfg.Name, "vpn", t.cfg.RequiresVPN)
			if err := t.vpnGate(ctx); err != nil {
				// ctx cancelled — clean shutdown.
				t.setStatus(StatusDisconnected)
				return nil
			}

			s = t.Stats()
			s.LastError = ""
			t.stats.Store(s)

			log.Info("vpn ready, connecting tunnel", "tunnel", t.cfg.Name)
		}

		// Run pre_connect commands before each dial attempt.
		if err := t.runPreConnect(ctx); err != nil {
			if ctx.Err() != nil {
				t.setStatus(StatusDisconnected)
				return nil
			}
			s := t.Stats()
			s.LastError = "pre_connect: " + err.Error()
			t.stats.Store(s)
		}

		// Clear reconnect timer so the UI shows "connecting" during the dial,
		// not a stale countdown frozen at 0s.
		s0 := t.Stats()
		s0.NextReconnectAt = time.Time{}
		t.stats.Store(s0)

		if err := t.dial(ctx); err != nil {
			log.Warn("tunnel dial failed",
				"tunnel", t.cfg.Name,
				"err", err,
			)
			s := t.Stats()
			s.LastError = err.Error()
			t.stats.Store(s)
		} else {
			backoff.reset(time.Duration(t.cfg.ReconnectDelay) * time.Second)
			t.keepalive(ctx)
			if t.ptySession != nil {
				t.ptySession.Close()
				t.ptySession = nil
			}
		}

		s := t.Stats()
		s.ReconnectCount++
		s.Status = StatusConnecting
		s.ConnectedAt = time.Time{}
		t.stats.Store(s)
		t.client = nil

		// If there's no network at all, wait for it and reset backoff.
		// Skip the countdown after restore — waiting for the network already
		// served as the delay.
		if !netcheck.HasUplink() {
			s.LastError = "waiting for network"
			t.stats.Store(s)
			log.Info("tunnel waiting for network", "tunnel", t.cfg.Name)
			if err := netcheck.WaitForUplink(ctx); err != nil {
				t.setStatus(StatusDisconnected)
				return nil
			}
			s = t.Stats()
			s.LastError = ""
			t.stats.Store(s)
			backoff.reset(time.Duration(t.cfg.ReconnectDelay) * time.Second)
			log.Info("network up, reconnecting tunnel immediately", "tunnel", t.cfg.Name)
			continue
		}

		delay := backoff.next()
		s.NextReconnectAt = t.clock.Now().Add(delay)
		t.stats.Store(s)

		log.Warn("tunnel disconnected, reconnecting",
			"tunnel", t.cfg.Name,
			"delay", delay,
			"reconnects", s.ReconnectCount,
		)

		select {
		case <-ctx.Done():
			t.setStatus(StatusDisconnected)
			return nil
		case <-t.clock.After(delay):
		}
	}
}

func (t *Tunnel) dial(ctx context.Context) error {
	sshCfg, err := t.buildSSHConfig()
	if err != nil {
		return fmt.Errorf("building SSH config: %w", err)
	}

	addr := fmt.Sprintf("%s:%d", t.cfg.Host, t.cfg.Port)
	log.Info("connecting tunnel", "tunnel", t.cfg.Name, "addr", addr)

	// Respect ctx during the dial itself.
	// KeepAlive sends TCP-level probes so NAT/firewall entries don't expire.
	timeout := time.Duration(t.cfg.DialTimeout) * time.Second
	dialer := &net.Dialer{
		Timeout:   timeout,
		KeepAlive: 15 * time.Second,
	}
	tcpConn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("TCP dial %s: %w", addr, err)
	}

	sshConn, chans, reqs, err := ssh.NewClientConn(tcpConn, addr, sshCfg)
	if err != nil {
		tcpConn.Close()
		return fmt.Errorf("SSH handshake: %w", err)
	}

	t.client = ssh.NewClient(sshConn, chans, reqs)

	if t.cfg.ForcePTY {
		if err := t.openPTYSession(); err != nil {
			t.client.Close()
			t.client = nil
			return fmt.Errorf("PTY session: %w", err)
		}
	}

	now := t.clock.Now()
	t.stats.Store(Stats{
		Status:         StatusConnected,
		ConnectedAt:    now,
		LocalPort:      t.cfg.LocalPort,
		Host:           addr,
		ReconnectCount: t.Stats().ReconnectCount,
	})

	log.Info("tunnel connected", "tunnel", t.cfg.Name, "addr", addr)
	return nil
}

func (t *Tunnel) openPTYSession() error {
	// Forward the local SSH agent to the server (-A equivalent).
	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		if agentConn, err := net.Dial("unix", sock); err == nil {
			if err := agent.ForwardToAgent(t.client, agent.NewClient(agentConn)); err != nil {
				agentConn.Close()
				log.Debug("agent forwarding setup failed", "tunnel", t.cfg.Name, "err", err)
			}
		}
	}

	sess, err := t.client.NewSession()
	if err != nil {
		return fmt.Errorf("new session: %w", err)
	}

	// Request agent forwarding on this session before PTY.
	_ = agent.RequestAgentForwarding(sess)

	modes := ssh.TerminalModes{ssh.ECHO: 0}
	if err := sess.RequestPty("xterm", 24, 80, modes); err != nil {
		sess.Close()
		return fmt.Errorf("request pty: %w", err)
	}
	// Drain stdout/stderr before Shell() so the SCB shell doesn't block on a full buffer.
	sess.Stdout = io.Discard
	sess.Stderr = io.Discard
	if err := sess.Shell(); err != nil {
		sess.Close()
		return fmt.Errorf("start shell: %w", err)
	}
	t.ptySession = sess
	log.Debug("PTY session opened with agent forwarding", "tunnel", t.cfg.Name)
	return nil
}

func (t *Tunnel) keepalive(ctx context.Context) {
	interval := time.Duration(t.cfg.KeepaliveInterval) * time.Second
	probeTimeout := time.Duration(t.cfg.DialTimeout) * time.Second
	fails := 0

	// depLost receives a reason string when network or VPN dependency is lost.
	// Buffered so watchDeps can send without blocking even if keepalive already exited.
	depLost := make(chan string, 1)
	go t.watchDeps(ctx, depLost)

	for {
		select {
		case <-ctx.Done():
			return
		case reason := <-depLost:
			log.Info("tunnel dependency lost, reconnecting",
				"tunnel", t.cfg.Name,
				"reason", reason,
			)
			s := t.stats.Load().(Stats)
			s.LastError = reason
			t.stats.Store(s)
			t.client.Close()
			return
		case <-t.clock.After(interval):
		}

		err := t.sendKeepalive(ctx, probeTimeout)
		if err != nil {
			fails++
			s := t.stats.Load().(Stats)
			s.KeepaliveFailures = fails
			t.stats.Store(s)
			log.Warn("keepalive failed",
				"tunnel", t.cfg.Name,
				"fails", fails,
				"max", t.cfg.KeepaliveMaxFails,
			)
			if fails >= t.cfg.KeepaliveMaxFails {
				log.Warn("keepalive max fails reached, reconnecting", "tunnel", t.cfg.Name)
				t.client.Close()
				return
			}
			continue
		}
		// Reset on success.
		if fails > 0 {
			s := t.stats.Load().(Stats)
			s.KeepaliveFailures = 0
			t.stats.Store(s)
		}
		fails = 0
	}
}

// watchDeps polls network and VPN prerequisites every 2 s while the tunnel is
// connected. When a dependency is lost it sends a human-readable reason to lost
// (non-blocking) so keepalive() can close the client and trigger an immediate
// reconnect rather than waiting for keepalive timeouts.
func (t *Tunnel) watchDeps(ctx context.Context, lost chan<- string) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !netcheck.HasUplink() {
				select {
				case lost <- "waiting for network":
				default:
				}
				return
			}
			if t.vpnIsConnected != nil && !t.vpnIsConnected() {
				select {
				case lost <- "waiting for VPN: " + t.cfg.RequiresVPN:
				default:
				}
				return
			}
		}
	}
}

// sendKeepalive sends a single keepalive probe with a timeout equal to the
// keepalive interval. Without this timeout, SendRequest blocks for the full OS
// TCP retransmit window (~75s on macOS) when the remote becomes unreachable
// (e.g. VPN drops without sending RST), masking the failure.
func (t *Tunnel) sendKeepalive(ctx context.Context, timeout time.Duration) error {
	type result struct{ err error }
	ch := make(chan result, 1)
	go func() {
		_, _, err := t.client.SendRequest("keepalive@openssh.com", true, nil)
		ch <- result{err}
	}()
	select {
	case res := <-ch:
		return res.err
	case <-t.clock.After(timeout):
		return fmt.Errorf("keepalive timeout after %s", timeout)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (t *Tunnel) buildSSHConfig() (*ssh.ClientConfig, error) {
	auths, err := t.authMethods()
	if err != nil {
		return nil, err
	}

	hostKey, err := t.hostKeyCallback()
	if err != nil {
		return nil, err
	}

	return &ssh.ClientConfig{
		User:            t.cfg.User,
		Auth:            auths,
		HostKeyCallback: hostKey,
		Timeout:         time.Duration(t.cfg.DialTimeout) * time.Second,
		ClientVersion:   "SSH-2.0-OpenSSH_9.6",
	}, nil
}

func (t *Tunnel) authMethods() ([]ssh.AuthMethod, error) {
	var methods []ssh.AuthMethod

	// Explicit identity file takes highest priority.
	if t.cfg.IdentityFile != "" {
		signer, err := loadSigner(t.cfg.IdentityFile)
		if err != nil {
			return nil, fmt.Errorf("loading identity file %s: %w", t.cfg.IdentityFile, err)
		}
		methods = append(methods, ssh.PublicKeys(signer))
	}

	// SSH agent (YubiKey, gpg-agent, ssh-agent) — preferred over file keys.
	if m := agentAuthMethod(); m != nil {
		methods = append(methods, m)
	}

	// Last resort: well-known default key file locations.
	if len(methods) == 0 {
		home, _ := os.UserHomeDir()
		for _, name := range []string{"id_ed25519", "id_ecdsa", "id_rsa"} {
			path := filepath.Join(home, ".ssh", name)
			if signer, err := loadSigner(path); err == nil {
				methods = append(methods, ssh.PublicKeys(signer))
				break
			}
		}
	}

	if len(methods) == 0 {
		return nil, fmt.Errorf("no SSH authentication method available for tunnel %q; is ssh-agent running?", t.cfg.Name)
	}

	return methods, nil
}

// agentAuthMethod returns an ssh.AuthMethod backed by the running SSH agent,
// or nil if SSH_AUTH_SOCK is not set or the socket cannot be opened.
func agentAuthMethod() ssh.AuthMethod {
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return nil
	}

	conn, err := net.Dial("unix", sock)
	if err != nil {
		log.Debug("ssh-agent not available", "socket", sock, "err", err)
		return nil
	}

	log.Debug("using ssh-agent", "socket", sock)
	return ssh.PublicKeysCallback(agent.NewClient(conn).Signers)
}

func (t *Tunnel) hostKeyCallback() (ssh.HostKeyCallback, error) {
	if os.Getenv("HOPSCOTCH_INSECURE_SKIP_KNOWN_HOSTS") == "true" {
		log.Warn("known_hosts verification disabled (HOPSCOTCH_INSECURE_SKIP_KNOWN_HOSTS=true)",
			"tunnel", t.cfg.Name)
		return ssh.InsecureIgnoreHostKey(), nil //nolint:gosec
	}

	knownHostsFile := t.cfg.KnownHostsFile
	if knownHostsFile == "" {
		home, _ := os.UserHomeDir()
		knownHostsFile = filepath.Join(home, ".ssh", "known_hosts")
	}

	cb, err := knownhosts.New(knownHostsFile)
	if err != nil {
		return nil, fmt.Errorf(
			"loading known_hosts %s: %w\n  hint: run 'hopscotch trust %s' to add this host",
			knownHostsFile, err, t.cfg.Host,
		)
	}

	return cb, nil
}

func (t *Tunnel) setStatus(s Status) {
	cur := t.Stats()
	cur.Status = s
	t.stats.Store(cur)
}

func loadSigner(path string) (ssh.Signer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ssh.ParsePrivateKey(data)
}


// runPreConnect executes each pre_connect command before a dial attempt.
func (t *Tunnel) runPreConnect(ctx context.Context) error {
	for _, cmdStr := range t.cfg.PreConnect {
		log.Info("tunnel pre_connect", "tunnel", t.cfg.Name, "cmd", cmdStr)
		s := t.Stats()
		s.LastError = "pre_connect: " + cmdStr
		t.stats.Store(s)

		var cmd *exec.Cmd
		if runtime.GOOS == "windows" {
			cmd = exec.CommandContext(ctx, "cmd", "/C", cmdStr)
		} else {
			cmd = exec.CommandContext(ctx, "sh", "-c", cmdStr)
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			log.Error("tunnel pre_connect failed", "tunnel", t.cfg.Name, "cmd", cmdStr, "err", err, "output", strings.TrimSpace(string(out)))
			return fmt.Errorf("%q: %w", cmdStr, err)
		}
	}
	// Clear pre_connect reason after all commands succeed.
	s := t.Stats()
	s.LastError = ""
	t.stats.Store(s)
	return nil
}

// backoff implements capped exponential backoff.
type backoff struct {
	current time.Duration
	max     time.Duration
}

func newBackoff(initial, max time.Duration) *backoff {
	return &backoff{current: initial, max: max}
}

func (b *backoff) next() time.Duration {
	d := b.current
	b.current = min(b.current*2, b.max)
	return d
}

// reset restarts the backoff from the initial delay.
func (b *backoff) reset(initial time.Duration) {
	b.current = initial
}

var _ io.Closer = (*ssh.Client)(nil) // compile-time interface check
