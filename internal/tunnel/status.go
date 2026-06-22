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
	Status         Status
	ConnectedAt    time.Time
	ReconnectCount int
	LocalPort      int
}
