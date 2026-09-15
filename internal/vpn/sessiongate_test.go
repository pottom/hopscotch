package vpn

import (
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (f *fakeClock) now() time.Time          { return f.t }
func (f *fakeClock) advance(d time.Duration) { f.t = f.t.Add(d) }

func newTestGate(clock *fakeClock) *sessionGate {
	g := newSessionGate()
	g.now = clock.now
	return g
}

// The burst that left the gateway dark: four session starts in 18 s from
// switching back and forth. The fourth must wait for the window to roll.
func TestSessionGateLimitsBursts(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1_000_000, 0)}
	g := newTestGate(clock)

	for i, vpn := range []string{"m2c", "4ig", "m2c"} {
		if wait := g.reserve(vpn); wait != 0 {
			t.Fatalf("start %d (%s): wait %v, want 0 (within the burst allowance)", i+1, vpn, wait)
		}
		clock.advance(6 * time.Second)
	}
	// Now at t=18s; the first start was at t=0, so a slot frees at t=120s.
	if wait := g.reserve("4ig"); wait != 102*time.Second {
		t.Fatalf("4th start: wait %v, want 1m42s", wait)
	}
	// A refused reserve must not consume a slot.
	clock.advance(102 * time.Second)
	if wait := g.reserve("4ig"); wait != 0 {
		t.Fatalf("after the oldest start aged out: wait %v, want 0", wait)
	}
	if wait := g.reserve("4ig"); wait == 0 {
		t.Fatal("window full again, but reserve allowed another start")
	}
}

// A normal single switch or reconnect is never delayed.
func TestSessionGateLeavesNormalUseAlone(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1_000_000, 0)}
	g := newTestGate(clock)

	if wait := g.reserve("4ig"); wait != 0 {
		t.Fatalf("first start: wait %v", wait)
	}
	clock.advance(10 * time.Minute)
	if d := g.settleFor("m2c"); d != 0 {
		t.Errorf("switch long after the last start: settle %v, want 0", d)
	}
	if wait := g.reserve("m2c"); wait != 0 {
		t.Errorf("switch long after the last start: wait %v, want 0", wait)
	}
	// The same VPN restarting (reconnect loop) never settles.
	clock.advance(time.Second)
	if d := g.settleFor("m2c"); d != 0 {
		t.Errorf("same VPN restarting: settle %v, want 0", d)
	}
}

func TestSessionGateSettlesBackAndForth(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1_000_000, 0)}
	g := newTestGate(clock)

	g.reserve("m2c")
	clock.advance(5 * time.Second)
	if d := g.settleFor("4ig"); d != 3*time.Second {
		t.Errorf("switching back 5 s after another VPN started: settle %v, want 3s", d)
	}
	clock.advance(40 * time.Second)
	if d := g.settleFor("4ig"); d != 0 {
		t.Errorf("45 s after another VPN started: settle %v, want 0", d)
	}
}
