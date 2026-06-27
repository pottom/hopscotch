// Package vpn manages SSL VPN connections as subprocess lifecycle.
package vpn

import (
	"context"
	"net"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/log"

	"hopscotch/internal/msgs"
	"hopscotch/internal/netcheck"
)

// Stats is a point-in-time snapshot of one VPN connection.
type Stats struct {
	State           State
	Reconnects      int
	ConnectedAt     time.Time // zero if never connected
	Server          string    // hostname extracted from server URL
	NextReconnectAt time.Time // non-zero only while waiting to reconnect
	LastError       string    // last error from subprocess; empty when connected
	TunIface        string    // tunnel interface name (e.g. utun2, tun0); empty until detected
}

// State represents the lifecycle state of a VPN connection.
type State int32

const (
	StateDisconnected State = iota
	StateConnecting
	StateConnected
)

func (s State) String() string {
	switch s {
	case StateConnecting:
		return "connecting"
	case StateConnected:
		return "connected"
	default:
		return "disconnected"
	}
}

// connConfig holds all parameters for one VPN connection.
type connConfig struct {
	Name              string
	Binary            string // path to openconnect binary; default: "openconnect"
	Server            string
	User              string
	AuthGroup         string
	PasswordEnv       string
	PasswordCmd       string
	Certificate       string
	Key               string
	PingHost          string   // host[:port] TCP-probed to confirm VPN connectivity
	ExtraArgs         []string
	PreConnect        []string // commands to run before each connection attempt
	PostDisconnect    []string // commands to run after each VPN disconnect
	Sudo              bool
	DNSResolver       string // host:port; default "1.1.1.1:53"
	ReconnectDelay    int
	ReconnectMaxDelay int
}

// Connection manages one VPN subprocess.
type Connection struct {
	cfg              connConfig
	state            atomic.Int32
	reconnects       atomic.Int32
	connectedAt      atomic.Value // stores time.Time; zero until first connect
	nextReconnectAt  atomic.Value // stores time.Time; non-zero while waiting to reconnect
	lastError        atomic.Value // stores string; last subprocess error
	tunIface         atomic.Value // stores string; tunnel interface name
	tunIfacesBefore  atomic.Value // stores map[string]bool; tun interfaces before this runOnce
}

func newConnection(cfg connConfig) *Connection {
	c := &Connection{cfg: cfg}
	c.connectedAt.Store(time.Time{})
	c.nextReconnectAt.Store(time.Time{})
	c.lastError.Store("")
	c.tunIface.Store("")
	c.tunIfacesBefore.Store(map[string]bool{})
	return c
}

// detectTunIface finds the tunnel interface created by openconnect by comparing
// current network interfaces against the snapshot taken before the connection attempt.
// No-op if tunIface is already known (e.g. detected from stderr on Linux).
func (c *Connection) detectTunIface() {
	if c.tunIface.Load().(string) != "" {
		return
	}
	before := c.tunIfacesBefore.Load().(map[string]bool)
	ifaces, err := net.Interfaces()
	if err != nil {
		return
	}
	for _, iface := range ifaces {
		name := iface.Name
		if (strings.HasPrefix(name, "utun") || strings.HasPrefix(name, "tun")) && !before[name] {
			c.tunIface.Store(name)
			log.Info("vpn tunnel interface detected", "vpn", c.cfg.Name, "iface", name)
			return
		}
	}
}

// State returns the current VPN connection state.
func (c *Connection) State() State { return State(c.state.Load()) }

// Name returns the configured VPN name.
func (c *Connection) Name() string { return c.cfg.Name }

// Stats returns a point-in-time snapshot of the connection.
func (c *Connection) Stats() Stats {
	server := c.cfg.Server
	if u, err := url.Parse(c.cfg.Server); err == nil && u.Host != "" {
		server = u.Host
	}
	return Stats{
		State:           State(c.state.Load()),
		Reconnects:      int(c.reconnects.Load()),
		ConnectedAt:     c.connectedAt.Load().(time.Time),
		Server:          server,
		NextReconnectAt: c.nextReconnectAt.Load().(time.Time),
		LastError:       c.lastError.Load().(string),
		TunIface:        c.tunIface.Load().(string),
	}
}

func (c *Connection) setState(s State) {
	if s == StateConnected {
		if State(c.state.Load()) != StateConnected {
			c.connectedAt.Store(time.Now())
		}
		c.nextReconnectAt.Store(time.Time{})
		c.lastError.Store("")
	}
	c.state.Store(int32(s))
}

// Run manages the VPN subprocess lifecycle with exponential backoff reconnects.
// Blocks until ctx is cancelled.
func (c *Connection) Run(ctx context.Context) error {
	initial := time.Duration(c.cfg.ReconnectDelay) * time.Second
	b := &backoff{
		initial: initial,
		current: initial,
		max:     time.Duration(c.cfg.ReconnectMaxDelay) * time.Second,
	}

	for {
		c.setState(StateConnecting)
		beforeRun := time.Now()
		if err := c.runOnce(ctx); ctx.Err() != nil {
			c.setState(StateDisconnected)
			c.nextReconnectAt.Store(time.Time{})
			return nil
		} else if err != nil {
			// Don't overwrite a more specific error already captured from stderr.
			if c.lastError.Load().(string) == "" {
				c.lastError.Store(err.Error())
			}
		}
		c.setState(StateDisconnected)
		c.reconnects.Add(1)

		// If the VPN reached StateConnected during this run, reset the backoff —
		// only runs that never connected (e.g. auth failures, bad routes) should
		// accumulate reconnect delay.
		if c.connectedAt.Load().(time.Time).After(beforeRun) {
			b.reset()
		}

		// If there's no network at all, wait for it before the next attempt.
		// Skip the backoff countdown after restore — waiting for the network
		// already served as the delay.
		if !netcheck.HasUplink() {
			c.lastError.Store(msgs.WaitingForNetwork)
			log.Info("vpn waiting for network", "vpn", c.cfg.Name)
			if err := netcheck.WaitForUplink(ctx); err != nil {
				c.nextReconnectAt.Store(time.Time{})
				return nil
			}
			c.lastError.Store("")
			b.reset()
			log.Info("network up, reconnecting vpn immediately", "vpn", c.cfg.Name)
			continue
		}

		delay := b.next()
		log.Warn("vpn disconnected, reconnecting", "vpn", c.cfg.Name, "delay", delay)
		c.nextReconnectAt.Store(time.Now().Add(delay))
		select {
		case <-ctx.Done():
			c.nextReconnectAt.Store(time.Time{})
			return nil
		case <-time.After(delay):
			c.nextReconnectAt.Store(time.Time{})
		}
	}
}

type backoff struct {
	initial time.Duration
	current time.Duration
	max     time.Duration
}

func (b *backoff) next() time.Duration {
	d := b.current
	b.current = min(b.current*2, b.max)
	return d
}

func (b *backoff) reset() {
	b.current = b.initial
}
