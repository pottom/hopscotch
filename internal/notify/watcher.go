package notify

import (
	"context"
	"time"

	"github.com/pottom/hopscotch/internal/tunnel"
	"github.com/pottom/hopscotch/internal/vpn"
)

// TunnelStatter is implemented by *tunnel.Manager.
type TunnelStatter interface {
	AllStats() map[string]tunnel.Stats
}

// VPNStatter is implemented by *vpn.Manager.
type VPNStatter interface {
	AllStats() map[string]vpn.Stats
}

// snapshot is a state view collapsed from either tunnel.Stats or vpn.Stats so
// the transition logic in diff can stay agnostic of which one it's watching.
type snapshot struct {
	connected  bool
	down       bool // connecting or disconnected — a real failure state, not a manual pause
	autoPaused bool
	failures   int
}

func tunnelSnapshot(s tunnel.Stats) snapshot {
	return snapshot{
		connected:  s.Status == tunnel.StatusConnected,
		down:       s.Status == tunnel.StatusConnecting || s.Status == tunnel.StatusDisconnected,
		autoPaused: s.AutoPaused,
		failures:   s.ConsecutiveFailures,
	}
}

func vpnSnapshot(s vpn.Stats) snapshot {
	return snapshot{
		connected:  s.State == vpn.StateConnected,
		down:       s.State == vpn.StateConnecting || s.State == vpn.StateDisconnected,
		autoPaused: s.AutoPaused,
		failures:   s.ConsecutiveFailures,
	}
}

// trackedState is what the watcher remembers per tunnel/VPN name between
// polls, so a flapping connection or a steady auto-paused state doesn't
// re-fire the same notification on every tick.
type trackedState struct {
	notifiedDown      bool // a "disconnected" notification was sent for the current down period
	notifiedAutoPause bool // an "auto-paused" notification was sent for the current auto-pause period
}

// diff decides which notification (if any) should fire for one named
// tunnel/VPN, given whether it has been observed before (seen) and its
// previously tracked state. It is a pure function so the transition rules can
// be unit tested without beeep or a ticker.
//
// event is one of "", "disconnected", "reconnected", "auto_paused".
func diff(prev trackedState, seen bool, curr snapshot) (event string, next trackedState) {
	next = prev

	if !seen {
		// First observation of this name (process startup): seed state
		// without notifying, so the initial connect/pause never fires.
		next.notifiedAutoPause = curr.autoPaused
		return "", next
	}

	switch {
	case curr.down && !prev.notifiedDown:
		next.notifiedDown = true
		event = "disconnected"
	case curr.connected && prev.notifiedDown:
		next.notifiedDown = false
		event = "reconnected"
	case curr.connected:
		next.notifiedDown = false
	}

	// down and autoPaused are mutually exclusive (autoPaused implies a Paused
	// status, down implies Connecting/Disconnected), so this never collides
	// with the event already selected above.
	if curr.autoPaused && !prev.notifiedAutoPause {
		next.notifiedAutoPause = true
		event = "auto_paused"
	} else if !curr.autoPaused {
		next.notifiedAutoPause = false
	}

	return event, next
}

func (n *Notifier) fire(event, kind, name string, failures int) {
	if event == "" || n.suppressed(kind, name) {
		return
	}
	switch event {
	case "disconnected":
		n.Disconnected(kind, name)
	case "reconnected":
		n.Reconnected(kind, name)
	case "auto_paused":
		n.AutoPaused(kind, name, failures)
	}
}

// Watch polls tunnels (and vpns, if non-nil) every interval and fires
// notifications on meaningful state transitions (see diff). Runs until ctx is
// cancelled; intended to be started as its own errgroup goroutine so
// notifications fire even when no TUI or web UI is attached.
func Watch(ctx context.Context, n *Notifier, tunnels TunnelStatter, vpns VPNStatter, interval time.Duration) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	tunnelState := map[string]trackedState{}
	vpnState := map[string]trackedState{}

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			for name, s := range tunnels.AllStats() {
				prev, seen := tunnelState[name]
				event, next := diff(prev, seen, tunnelSnapshot(s))
				tunnelState[name] = next
				n.fire(event, "tunnel", name, s.ConsecutiveFailures)
			}
			if vpns != nil {
				for name, s := range vpns.AllStats() {
					prev, seen := vpnState[name]
					event, next := diff(prev, seen, vpnSnapshot(s))
					vpnState[name] = next
					n.fire(event, "vpn", name, s.ConsecutiveFailures)
				}
			}
		}
	}
}
