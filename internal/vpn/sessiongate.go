package vpn

import (
	"sync"
	"time"
)

// sessionGate spaces out openconnect session starts across every VPN of one
// Manager. Bursts of short sessions — fast back-and-forth switching, or
// restarts in quick succession — are what left the gateway dark for minutes
// (see dark.go), so this limits them no matter whether a user or the reconnect
// loop asked for the session.
//
// It is shared by all VPNs on purpose: the two VPNs this was measured on are
// concentrators of the same network for the same user, and switching between
// them is exactly the burst to avoid.
type sessionGate struct {
	mu  sync.Mutex
	now func() time.Time

	// At most maxStarts sessions may start within window.
	window    time.Duration
	maxStarts int
	// A start requested within recentOther of another VPN's start is part of
	// a back-and-forth switch; it waits settle first so a click that is
	// immediately undone never starts a session at all.
	recentOther time.Duration
	settle      time.Duration

	starts []gateStart // oldest first, pruned to window
}

type gateStart struct {
	at  time.Time
	vpn string
}

func newSessionGate() *sessionGate {
	return &sessionGate{
		now:         time.Now,
		window:      2 * time.Minute,
		maxStarts:   3,
		recentOther: 30 * time.Second,
		settle:      3 * time.Second,
	}
}

// reserve records a session start for vpn and returns 0 when one is allowed
// now. Otherwise it records nothing and returns how long to wait before
// asking again.
func (g *sessionGate) reserve(vpn string) time.Duration {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	g.pruneLocked(now)
	if len(g.starts) >= g.maxStarts {
		if wait := g.starts[0].at.Add(g.window).Sub(now); wait > 0 {
			return wait
		}
	}
	g.starts = append(g.starts, gateStart{at: now, vpn: vpn})
	return 0
}

// settleFor returns how long vpn should wait before reserving because another
// VPN started a session moments ago, or 0.
func (g *sessionGate) settleFor(vpn string) time.Duration {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	for i := len(g.starts) - 1; i >= 0; i-- {
		s := g.starts[i]
		if now.Sub(s.at) > g.recentOther {
			break
		}
		if s.vpn != vpn {
			return g.settle
		}
	}
	return 0
}

func (g *sessionGate) pruneLocked(now time.Time) {
	keep := 0
	for keep < len(g.starts) && now.Sub(g.starts[keep].at) >= g.window {
		keep++
	}
	g.starts = g.starts[keep:]
}
