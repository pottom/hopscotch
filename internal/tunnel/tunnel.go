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

	"github.com/pottom/hopscotch/internal/config"
	"github.com/pottom/hopscotch/internal/msgs"
	"github.com/pottom/hopscotch/internal/netcheck"
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
	stats          atomic.Value                    // holds Stats (without traffic counters)
	client         *ssh.Client                     // guarded by the reconnect loop (single goroutine writer)
	ptySession     *ssh.Session                    // held open when force_pty is set; closed after keepalive exits
	ptyStdin       io.WriteCloser                  // stdin of ptySession; used to keep the PTY channel's data flow alive
	// Traffic counters — always-incrementing, read by Stats().
	bytesIn        atomic.Uint64
	bytesOut       atomic.Uint64
	activeConns    atomic.Int64
	forceReconnect chan struct{} // buffered(1); signals immediate reconnect

	paused        atomic.Bool
	pauseRequest  chan struct{} // buffered(1); signals a pause request
	resume        chan struct{} // buffered(1); signals resume from pause
	cancelMu      sync.Mutex
	cancelAttempt context.CancelFunc // cancels the in-flight gate-wait/pre-connect/dial; nil when none active
}

// New creates a Tunnel with a real system clock.
func New(cfg config.TunnelConfig) *Tunnel {
	return NewWithGate(cfg, nil, nil)
}

// NewWithGate creates a Tunnel whose reconnect loop waits for gate before each dial.
// gate is called at the start of every connect attempt; a non-nil return aborts the tunnel.
// isConnected is polled while the tunnel is connected to detect gate loss immediately.
func NewWithGate(cfg config.TunnelConfig, gate func(ctx context.Context) error, isConnected func() bool) *Tunnel {
	t := &Tunnel{
		cfg:            cfg,
		clock:          realClock{},
		vpnGate:        gate,
		vpnIsConnected: isConnected,
		forceReconnect: make(chan struct{}, 1),
		pauseRequest:   make(chan struct{}, 1),
		resume:         make(chan struct{}, 1),
	}
	t.stats.Store(Stats{Status: StatusConnecting, LocalPort: cfg.LocalPort, Host: fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)})
	return t
}

// ForceReconnect interrupts the current backoff wait or active keepalive loop,
// triggering an immediate reconnect attempt.
func (t *Tunnel) ForceReconnect() {
	select {
	case t.forceReconnect <- struct{}{}:
	default:
	}
}

// Pause stops the tunnel from retrying. It aborts an in-flight connect
// attempt (gate-wait/pre-connect/dial, including a mid-handshake dial)
// immediately rather than waiting for it to finish, or interrupts an active
// keepalive/backoff wait. The tunnel stays paused until Resume is called.
func (t *Tunnel) Pause() {
	t.paused.Store(true)
	t.cancelMu.Lock()
	cancel := t.cancelAttempt
	t.cancelMu.Unlock()
	if cancel != nil {
		cancel()
	}
	select {
	case t.pauseRequest <- struct{}{}:
	default:
	}
}

// Resume clears a paused tunnel and triggers an immediate reconnect attempt.
func (t *Tunnel) Resume() {
	t.paused.Store(false)
	select {
	case t.resume <- struct{}{}:
	default:
	}
}

// beginAttempt derives a cancelable context from parent and stores its cancel
// func so Pause can abort the in-flight attempt immediately. The returned end
// func must be called exactly once when the attempt-scoped work is done.
func (t *Tunnel) beginAttempt(parent context.Context) (ctx context.Context, end func()) {
	ctx, cancel := context.WithCancel(parent)
	t.cancelMu.Lock()
	t.cancelAttempt = cancel
	t.cancelMu.Unlock()
	return ctx, func() {
		cancel()
		t.cancelMu.Lock()
		t.cancelAttempt = nil
		t.cancelMu.Unlock()
	}
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
		if t.paused.Load() {
			s := t.Stats()
			s.Status = StatusPaused
			s.LastError = ""
			s.NextReconnectAt = time.Time{}
			t.stats.Store(s)
			select {
			case <-ctx.Done():
				t.setStatus(StatusDisconnected)
				return nil
			case <-t.resume:
				backoff.reset(time.Duration(t.cfg.ReconnectDelay) * time.Second)
				t.setStatus(StatusConnecting)
			}
			continue
		}

		// Discard a stale pause signal left over from a Pause() call whose effect
		// (cancelling the in-flight attempt, or the paused-wait above) was already
		// applied — otherwise a later select (keepalive/backoff-wait) could misread
		// it as a brand new pause request.
		select {
		case <-t.pauseRequest:
		default:
		}

		attemptCtx, endAttempt := t.beginAttempt(ctx)

		// Wait for VPN if this tunnel has a dependency, but only if the VPN
		// isn't already connected (e.g. after an SSH auth failure the VPN stays up).
		if t.vpnGate != nil && (t.vpnIsConnected == nil || !t.vpnIsConnected()) {
			s := t.Stats()
			s.LastError = msgs.WaitingForVPNPrefix + t.cfg.RequiresVPN
			s.NextReconnectAt = time.Time{} // clear stale countdown from previous delay
			t.stats.Store(s)

			log.Info("tunnel waiting for vpn", "tunnel", t.cfg.Name, "vpn", t.cfg.RequiresVPN)
			if err := t.vpnGate(attemptCtx); err != nil {
				endAttempt()
				if ctx.Err() != nil {
					// ctx cancelled — clean shutdown.
					t.setStatus(StatusDisconnected)
					return nil
				}
				// Otherwise the attempt was cancelled by Pause(); re-enter the
				// loop, where the paused branch above takes over.
				continue
			}

			s = t.Stats()
			s.LastError = ""
			t.stats.Store(s)

			log.Info("vpn ready, connecting tunnel", "tunnel", t.cfg.Name)
		}

		// Run pre_connect commands before each dial attempt.
		if err := t.runPreConnect(attemptCtx); err != nil {
			if attemptCtx.Err() != nil {
				endAttempt()
				if ctx.Err() != nil {
					t.setStatus(StatusDisconnected)
					return nil
				}
				continue
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

		dialErr := t.dial(attemptCtx)
		endAttempt()

		if dialErr != nil {
			if ctx.Err() == nil && t.paused.Load() {
				continue
			}
			log.Warn("tunnel dial failed",
				"tunnel", t.cfg.Name,
				"err", dialErr,
			)
			s := t.Stats()
			s.LastError = dialErr.Error()
			t.stats.Store(s)
		} else {
			backoff.reset(time.Duration(t.cfg.ReconnectDelay) * time.Second)
			t.keepalive(ctx)
			if t.ptySession != nil {
				t.ptySession.Close()
				t.ptySession = nil
				t.ptyStdin = nil
			}
		}

		s := t.Stats()
		s.ReconnectCount++
		s.Status = StatusConnecting
		s.ConnectedAt = time.Time{}
		t.stats.Store(s)
		t.client = nil

		if t.paused.Load() {
			// Status will be set to StatusPaused at the top of the loop.
			continue
		}

		// If there's no network at all, wait for it and reset backoff.
		// Skip the countdown after restore — waiting for the network already
		// served as the delay.
		if !netcheck.HasUplink() {
			s.LastError = msgs.WaitingForNetwork
			t.stats.Store(s)
			log.Info("tunnel waiting for network", "tunnel", t.cfg.Name)
			waitCtx, endWait := t.beginAttempt(ctx)
			err := netcheck.WaitForUplink(waitCtx)
			endWait()
			if err != nil {
				if ctx.Err() != nil {
					t.setStatus(StatusDisconnected)
					return nil
				}
				continue
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

		// If this was an SSH auth failure, watch for new agent keys (e.g. YubiKey
		// insertion) so we can retry immediately instead of waiting out the backoff.
		var agentChanged <-chan struct{}
		if isAuthError(s.LastError) {
			agentChanged = watchAgentKeys(ctx)
		}

		select {
		case <-ctx.Done():
			t.setStatus(StatusDisconnected)
			return nil
		case <-agentChanged:
			log.Info("SSH agent keys changed, retrying immediately", "tunnel", t.cfg.Name)
		case <-t.clock.After(delay):
		case <-t.forceReconnect:
			log.Info("force reconnect requested, skipping delay", "tunnel", t.cfg.Name)
		case <-t.pauseRequest:
			log.Info("pause requested, skipping delay", "tunnel", t.cfg.Name)
		}
	}
}

func (t *Tunnel) dial(ctx context.Context) error {
	sshCfg, agentConn, err := t.buildSSHConfig()
	if err != nil {
		return fmt.Errorf("building SSH config: %w", err)
	}
	// agentConn backs the agent-based AuthMethod's signer; it's only needed
	// during the handshake below (publickey auth happens there, not after),
	// so it can be closed once this attempt is done either way. Leaving it
	// open here previously leaked one connection to SSH_AUTH_SOCK (e.g.
	// gpg-agent) per connect attempt — fatal for flaky tunnels that retry
	// every few seconds.
	if agentConn != nil {
		defer agentConn.Close()
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

	sshConn, chans, reqs, err := t.handshake(ctx, tcpConn, addr, sshCfg)
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

	// Probe whether the SSH server allows TCP forwarding before declaring connected.
	if err := t.probeTCPForwarding(); err != nil {
		t.client.Close()
		t.client = nil
		return err
	}

	now := t.clock.Now()
	t.stats.Store(Stats{
		Status:         StatusConnected,
		ConnectedAt:    now.Round(0), // strip monotonic reading so uptime survives system sleep
		LocalPort:      t.cfg.LocalPort,
		Host:           addr,
		ReconnectCount: t.Stats().ReconnectCount,
	})

	log.Info("tunnel connected", "tunnel", t.cfg.Name, "addr", addr)
	return nil
}

// handshake performs the SSH handshake on an already-established TCP
// connection, aborting immediately if ctx is cancelled (e.g. by Pause)
// instead of blocking until the handshake times out or completes on its
// own — ssh.NewClientConn takes no context, so closing conn is the only way
// to interrupt it mid-handshake.
func (t *Tunnel) handshake(ctx context.Context, conn net.Conn, addr string, cfg *ssh.ClientConfig) (ssh.Conn, <-chan ssh.NewChannel, <-chan *ssh.Request, error) {
	type result struct {
		sshConn ssh.Conn
		chans   <-chan ssh.NewChannel
		reqs    <-chan *ssh.Request
		err     error
	}
	ch := make(chan result, 1)
	go func() {
		sshConn, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
		ch <- result{sshConn, chans, reqs, err}
	}()
	select {
	case r := <-ch:
		return r.sshConn, r.chans, r.reqs, r.err
	case <-ctx.Done():
		conn.Close()
		<-ch // drain the goroutine; NewClientConn unblocks once conn is closed
		return nil, nil, nil, ctx.Err()
	}
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
	stdin, err := sess.StdinPipe()
	if err != nil {
		sess.Close()
		return fmt.Errorf("stdin pipe: %w", err)
	}
	// Drain stdout/stderr before Shell() so the SCB shell doesn't block on a full buffer.
	sess.Stdout = io.Discard
	sess.Stderr = io.Discard
	if err := sess.Shell(); err != nil {
		sess.Close()
		return fmt.Errorf("start shell: %w", err)
	}
	t.ptySession = sess
	t.ptyStdin = stdin
	log.Debug("PTY session opened with agent forwarding", "tunnel", t.cfg.Name)
	return nil
}

// pokePTY writes a no-op keystroke (space immediately erased with backspace)
// into the PTY channel so devices that record/monitor the session (e.g. an
// SCB jump host) see data traffic and don't tear it down as idle. The SSH
// transport-level "keepalive@openssh.com" request travels outside this
// channel and doesn't count as activity on it.
func (t *Tunnel) pokePTY() {
	if t.ptyStdin == nil {
		return
	}
	if _, err := t.ptyStdin.Write([]byte{' ', '\b'}); err != nil {
		log.Debug("PTY keepalive write failed", "tunnel", t.cfg.Name, "err", err)
	}
}

func (t *Tunnel) keepalive(ctx context.Context) {
	interval := time.Duration(t.cfg.KeepaliveInterval) * time.Second
	probeTimeout := time.Duration(t.cfg.DialTimeout) * time.Second
	fails := 0
	lastPoke := t.clock.Now()

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
		case <-t.forceReconnect:
			log.Info("force reconnect requested", "tunnel", t.cfg.Name)
			t.client.Close()
			// Re-signal so Run()'s backoff select also skips the delay.
			select {
			case t.forceReconnect <- struct{}{}:
			default:
			}
			return
		case <-t.pauseRequest:
			log.Info("pause requested", "tunnel", t.cfg.Name)
			t.client.Close()
			return
		case <-t.clock.After(interval):
		}

		if t.cfg.ForcePTY {
			pokeInterval := time.Duration(t.cfg.PTYPokeInterval) * time.Second
			if t.clock.Now().Sub(lastPoke) >= pokeInterval {
				t.pokePTY()
				lastPoke = t.clock.Now()
			}
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
				case lost <- msgs.WaitingForNetwork:
				default:
				}
				return
			}
			if t.vpnIsConnected != nil && !t.vpnIsConnected() {
				select {
				case lost <- msgs.WaitingForVPNPrefix + t.cfg.RequiresVPN:
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

// buildSSHConfig returns the client config along with the agent connection
// (if any) backing an agent-based AuthMethod. The caller must close it once
// the handshake using this config is done.
func (t *Tunnel) buildSSHConfig() (*ssh.ClientConfig, io.Closer, error) {
	auths, agentConn, err := t.authMethods()
	if err != nil {
		return nil, nil, err
	}

	hostKey, err := t.hostKeyCallback()
	if err != nil {
		if agentConn != nil {
			agentConn.Close()
		}
		return nil, nil, err
	}

	return &ssh.ClientConfig{
		User:            t.cfg.User,
		Auth:            auths,
		HostKeyCallback: hostKey,
		Timeout:         time.Duration(t.cfg.DialTimeout) * time.Second,
		ClientVersion:   "SSH-2.0-OpenSSH_9.6",
	}, agentConn, nil
}

func (t *Tunnel) authMethods() ([]ssh.AuthMethod, io.Closer, error) {
	var methods []ssh.AuthMethod

	// Explicit identity file takes highest priority.
	if t.cfg.IdentityFile != "" {
		signer, err := loadSigner(t.cfg.IdentityFile)
		if err != nil {
			return nil, nil, fmt.Errorf("loading identity file %s: %w", t.cfg.IdentityFile, err)
		}
		methods = append(methods, ssh.PublicKeys(signer))
	}

	// SSH agent (YubiKey, gpg-agent, ssh-agent) — preferred over file keys.
	m, agentConn := agentAuthMethod()
	if m != nil {
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
		if agentConn != nil {
			agentConn.Close()
		}
		return nil, nil, fmt.Errorf("no SSH authentication method available for tunnel %q; is ssh-agent running?", t.cfg.Name)
	}

	return methods, agentConn, nil
}

// agentAuthMethod returns an ssh.AuthMethod backed by the running SSH agent
// plus the connection backing it (nil, nil if SSH_AUTH_SOCK is not set or the
// socket cannot be opened). The caller owns the connection and must close it
// once it's no longer needed — agent.NewClient does not take ownership.
func agentAuthMethod() (ssh.AuthMethod, io.Closer) {
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return nil, nil
	}

	conn, err := net.Dial("unix", sock)
	if err != nil {
		log.Debug("ssh-agent not available", "socket", sock, "err", err)
		return nil, nil
	}

	log.Debug("using ssh-agent", "socket", sock)
	return ssh.PublicKeysCallback(agent.NewClient(conn).Signers), conn
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

// probeTCPForwarding opens a test direct-tcpip channel immediately after
// connecting to detect AllowTcpForwarding=no (or PermitOpen=none) on the server.
// Any error that is not a forwarding-denied error is ignored (e.g. connection
// refused to the probe target means forwarding works).
func (t *Tunnel) probeTCPForwarding() error {
	type result struct {
		conn net.Conn
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		// Port 1 (tcpmux) is virtually never listening; we expect "connection refused"
		// on a healthy server. If AllowTcpForwarding is off we get "administratively
		// prohibited" before the remote even tries to connect.
		conn, err := t.client.Dial("tcp", "127.0.0.1:1")
		ch <- result{conn, err}
	}()
	select {
	case r := <-ch:
		if r.conn != nil {
			r.conn.Close()
		}
		if isTCPForwardingDenied(r.err) {
			log.Warn("TCP forwarding denied by SSH server",
				"tunnel", t.cfg.Name,
				"hint", "set AllowTcpForwarding yes (and check PermitOpen) in sshd_config",
			)
			return fmt.Errorf("SSH server denied TCP forwarding — tunnel cannot proxy connections (check AllowTcpForwarding / PermitOpen in sshd_config)")
		}
		return nil
	case <-time.After(5 * time.Second):
		// If the probe hangs (unexpected), assume forwarding works.
		return nil
	}
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
