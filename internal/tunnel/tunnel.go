package tunnel

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/log"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"hopscotch/internal/config"
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
	cfg    config.TunnelConfig
	clock  Clock
	stats  atomic.Value // holds Stats
	client *ssh.Client  // guarded by the reconnect loop (single goroutine writer)
}

// New creates a Tunnel with a real system clock.
func New(cfg config.TunnelConfig) *Tunnel {
	t := &Tunnel{cfg: cfg, clock: realClock{}}
	t.stats.Store(Stats{Status: StatusConnecting, LocalPort: cfg.LocalPort})
	return t
}

// newWithClock is used in tests to inject a fake clock.
func newWithClock(cfg config.TunnelConfig, clk Clock) *Tunnel {
	t := &Tunnel{cfg: cfg, clock: clk}
	t.stats.Store(Stats{Status: StatusConnecting, LocalPort: cfg.LocalPort})
	return t
}

// Stats returns a snapshot of the tunnel's current metrics.
func (t *Tunnel) Stats() Stats { return t.stats.Load().(Stats) }

// Name returns the tunnel's configured name.
func (t *Tunnel) Name() string { return t.cfg.Name }

// DialContext dials a TCP address through the SSH tunnel.
// Implements socks5.Dialer and proxy.Dialer.
func (t *Tunnel) DialContext(_ context.Context, network, addr string) (net.Conn, error) {
	c := t.client
	if c == nil {
		return nil, fmt.Errorf("tunnel %q is not connected", t.cfg.Name)
	}
	return c.Dial(network, addr)
}

// Run establishes the SSH tunnel, keeps it alive, and reconnects on failure.
// Blocks until ctx is cancelled.
func (t *Tunnel) Run(ctx context.Context) error {
	backoff := newBackoff(
		time.Duration(t.cfg.ReconnectDelay)*time.Second,
		2*time.Minute,
	)

	for {
		if err := t.dial(ctx); err != nil {
			log.Warn("tunnel dial failed",
				"tunnel", t.cfg.Name,
				"err", err,
			)
		} else {
			t.keepalive(ctx)
		}

		s := t.Stats()
		s.ReconnectCount++
		s.Status = StatusConnecting
		s.ConnectedAt = time.Time{}
		t.stats.Store(s)
		t.client = nil

		delay := backoff.next()
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
	dialer := &net.Dialer{Timeout: 30 * time.Second}
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

	now := t.clock.Now()
	t.stats.Store(Stats{
		Status:      StatusConnected,
		ConnectedAt: now,
		LocalPort:   t.cfg.LocalPort,
		ReconnectCount: t.Stats().ReconnectCount,
	})

	log.Info("tunnel connected", "tunnel", t.cfg.Name, "addr", addr)
	return nil
}

func (t *Tunnel) keepalive(ctx context.Context) {
	interval := time.Duration(t.cfg.KeepaliveInterval) * time.Second
	fails := 0

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.clock.After(interval):
		}

		_, _, err := t.client.SendRequest("keepalive@openssh.com", true, nil)
		if err != nil {
			fails++
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
		fails = 0
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
		Timeout:         30 * time.Second,
	}, nil
}

func (t *Tunnel) authMethods() ([]ssh.AuthMethod, error) {
	var methods []ssh.AuthMethod

	if t.cfg.IdentityFile != "" {
		signer, err := loadSigner(t.cfg.IdentityFile)
		if err != nil {
			return nil, fmt.Errorf("loading identity file %s: %w", t.cfg.IdentityFile, err)
		}
		methods = append(methods, ssh.PublicKeys(signer))
	}

	// Fall back to default key locations if no identity_file configured.
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
		return nil, fmt.Errorf("no SSH authentication method available for tunnel %q", t.cfg.Name)
	}

	return methods, nil
}

func (t *Tunnel) hostKeyCallback() (ssh.HostKeyCallback, error) {
	if os.Getenv("HOPSCOTCH_INSECURE_SKIP_KNOWN_HOSTS") == "true" {
		log.Warn("known_hosts verification disabled (HOPSCOTCH_INSECURE_SKIP_KNOWN_HOSTS=true)",
			"tunnel", t.cfg.Name)
		return ssh.InsecureIgnoreHostKey(), nil //nolint:gosec
	}

	home, _ := os.UserHomeDir()
	knownHostsFile := filepath.Join(home, ".ssh", "known_hosts")

	cb, err := knownhosts.New(knownHostsFile)
	if err != nil {
		return nil, fmt.Errorf(
			"loading known_hosts: %w\n  hint: run 'hopscotch trust %s' to add this host",
			err, t.cfg.Host,
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
