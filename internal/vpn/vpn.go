// Package vpn manages SSL VPN connections as subprocess lifecycle.
package vpn

import (
	"context"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/log"
)

// Stats is a point-in-time snapshot of one VPN connection.
type Stats struct {
	State       State
	Reconnects  int
	ConnectedAt time.Time // zero if never connected
	Server      string    // hostname extracted from server URL
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
	Certificate       string
	Key               string
	PingHost          string   // host[:port] TCP-probed to confirm VPN connectivity
	ExtraArgs         []string
	PreConnect        []string // commands to run before each connection attempt
	Sudo              bool
	ReconnectDelay    int
	ReconnectMaxDelay int
}

// Connection manages one VPN subprocess.
type Connection struct {
	cfg         connConfig
	state       atomic.Int32
	reconnects  atomic.Int32
	connectedAt atomic.Value // stores time.Time; zero until first connect
}

func newConnection(cfg connConfig) *Connection {
	c := &Connection{cfg: cfg}
	c.connectedAt.Store(time.Time{})
	return c
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
		State:       State(c.state.Load()),
		Reconnects:  int(c.reconnects.Load()),
		ConnectedAt: c.connectedAt.Load().(time.Time),
		Server:      server,
	}
}

func (c *Connection) setState(s State) {
	if s == StateConnected && State(c.state.Load()) != StateConnected {
		c.connectedAt.Store(time.Now())
	}
	c.state.Store(int32(s))
}

// Run manages the VPN subprocess lifecycle with exponential backoff reconnects.
// Blocks until ctx is cancelled.
func (c *Connection) Run(ctx context.Context) error {
	b := &backoff{
		current: time.Duration(c.cfg.ReconnectDelay) * time.Second,
		max:     time.Duration(c.cfg.ReconnectMaxDelay) * time.Second,
	}

	for {
		c.setState(StateConnecting)
		if err := c.runOnce(ctx); ctx.Err() != nil {
			c.setState(StateDisconnected)
			return nil
		} else if err != nil {
			_ = err // already logged in runOnce
		}
		c.setState(StateDisconnected)
		c.reconnects.Add(1)

		delay := b.next()
		log.Warn("vpn disconnected, reconnecting", "vpn", c.cfg.Name, "delay", delay)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(delay):
		}
	}
}

type backoff struct {
	current time.Duration
	max     time.Duration
}

func (b *backoff) next() time.Duration {
	d := b.current
	b.current = min(b.current*2, b.max)
	return d
}
