package proxy

import (
	"context"
	"net"
	"sync"
	"sync/atomic"

	"hopscotch/internal/tunnel"
)

// DirectCounter wraps net.Dialer to track bytes and active connections for
// connections routed directly (not through an SSH tunnel).
type DirectCounter struct {
	bytesIn     atomic.Uint64
	bytesOut    atomic.Uint64
	activeConns atomic.Int64
}

// DialContext dials directly to addr and wraps the connection with byte counting.
func (d *DirectCounter) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	d.activeConns.Add(1)
	return &directConn{Conn: conn, ctr: d}, nil
}

// Snapshot returns a point-in-time copy of the traffic counters.
func (d *DirectCounter) Snapshot() tunnel.TrafficSnapshot {
	return tunnel.TrafficSnapshot{
		BytesIn:     d.bytesIn.Load(),
		BytesOut:    d.bytesOut.Load(),
		ActiveConns: d.activeConns.Load(),
	}
}

type directConn struct {
	net.Conn
	ctr  *DirectCounter
	once sync.Once
}

func (c *directConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	if n > 0 {
		c.ctr.bytesIn.Add(uint64(n))
	}
	return n, err
}

func (c *directConn) Write(b []byte) (int, error) {
	n, err := c.Conn.Write(b)
	if n > 0 {
		c.ctr.bytesOut.Add(uint64(n))
	}
	return n, err
}

func (c *directConn) Close() error {
	c.once.Do(func() { c.ctr.activeConns.Add(-1) })
	return c.Conn.Close()
}
