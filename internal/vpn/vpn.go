// Package vpn manages SSL VPN connections as subprocess lifecycle.
package vpn

import (
	"context"
	"sync/atomic"
	"time"
)

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
	Server            string
	User              string
	AuthGroup         string
	PasswordEnv       string
	Certificate       string
	Key               string
	PingHost          string   // host[:port] TCP-probed to confirm VPN connectivity
	ExtraArgs         []string
	Sudo              bool
	ReconnectDelay    int
	ReconnectMaxDelay int
}

// Connection manages one VPN subprocess.
type Connection struct {
	cfg   connConfig
	state atomic.Int32
}

func newConnection(cfg connConfig) *Connection {
	return &Connection{cfg: cfg}
}

// State returns the current VPN connection state.
func (c *Connection) State() State { return State(c.state.Load()) }

// Name returns the configured VPN name.
func (c *Connection) Name() string { return c.cfg.Name }

func (c *Connection) setState(s State) {
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

		delay := b.next()
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
