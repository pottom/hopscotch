package tunnel

import "time"

// Status represents the lifecycle state of a tunnel.
type Status int

const (
	StatusConnecting   Status = iota // dialing or waiting to reconnect
	StatusConnected                  // tunnel is up and forwarding
	StatusDisconnected               // gracefully stopped
)

func (s Status) String() string {
	switch s {
	case StatusConnected:
		return "connected"
	case StatusConnecting:
		return "connecting"
	case StatusDisconnected:
		return "disconnected"
	default:
		return "unknown"
	}
}

// Stats holds live metrics for a single tunnel.
type Stats struct {
	Status            Status
	ConnectedAt       time.Time
	NextReconnectAt   time.Time // non-zero only while waiting to reconnect
	ReconnectCount    int
	LocalPort         int
	Host              string // SSH server address (host:port)
	KeepaliveFailures int    // consecutive failures; resets to 0 on success or reconnect
	LastError         string // last connection failure reason; empty when connected
	// Traffic counters — cumulative since process start.
	BytesIn     uint64
	BytesOut    uint64
	ActiveConns int64
}

// TrafficSnapshot is a lightweight traffic-only view used by the direct counter
// and the SSE endpoint. Defined here so proxy and admin can share the type
// without introducing a circular import.
type TrafficSnapshot struct {
	BytesIn     uint64
	BytesOut    uint64
	ActiveConns int64
}
