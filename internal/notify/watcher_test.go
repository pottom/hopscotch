package notify

import (
	"testing"

	"github.com/pottom/hopscotch/internal/tunnel"
)

func TestDiff_FirstObservation_NoNotification(t *testing.T) {
	event, next := diff(trackedState{}, false, snapshot{connected: true})
	if event != "" {
		t.Errorf("event = %q, want empty on first observation", event)
	}
	if next.notifiedDown || next.notifiedAutoPause {
		t.Errorf("next = %+v, want zero value seeded from a connected snapshot", next)
	}
}

func TestDiff_FirstObservation_SeedsAutoPause(t *testing.T) {
	// A tunnel restored already auto-paused at startup must not immediately
	// re-fire an auto-pause notification.
	_, next := diff(trackedState{}, false, snapshot{autoPaused: true})
	if !next.notifiedAutoPause {
		t.Error("notifiedAutoPause = false, want true after seeding from an already-auto-paused snapshot")
	}
}

func TestDiff_UnexpectedDisconnect(t *testing.T) {
	prev := trackedState{}
	event, next := diff(prev, true, snapshot{down: true})
	if event != "disconnected" {
		t.Errorf("event = %q, want %q", event, "disconnected")
	}
	if !next.notifiedDown {
		t.Error("notifiedDown = false, want true after a disconnect notification")
	}
}

func TestDiff_Flapping_OnlyNotifiesOnce(t *testing.T) {
	state := trackedState{}
	seen := true

	// Down, then repeated Connecting ticks while it keeps retrying — only the
	// first tick should produce a notification.
	for i, wantEvent := range []string{"disconnected", "", "", ""} {
		event, next := diff(state, seen, snapshot{down: true})
		if event != wantEvent {
			t.Errorf("tick %d: event = %q, want %q", i, event, wantEvent)
		}
		state = next
	}
}

func TestDiff_Reconnect_OnlyAfterADisconnectWasNotified(t *testing.T) {
	// Recovering from the very first connect (startup) must not notify.
	_, seeded := diff(trackedState{}, false, snapshot{connected: false, down: true})
	// Startup seeds from a non-connected snapshot too (e.g. still connecting);
	// notifiedDown stays false since seeding never sets it.
	if seeded.notifiedDown {
		t.Fatal("seeding must not set notifiedDown")
	}
	event, _ := diff(seeded, true, snapshot{connected: true})
	if event != "" {
		t.Errorf("event = %q, want empty — no disconnect was ever notified", event)
	}
}

func TestDiff_SlowInitialConnect_NoPhantomDisconnect(t *testing.T) {
	// Startup while still connecting (down): a slow handshake spanning
	// multiple ticks must not be mistaken for a fresh disconnection.
	_, seeded := diff(trackedState{}, false, snapshot{down: true})

	for i := range 3 {
		event, next := diff(seeded, true, snapshot{down: true})
		if event != "" {
			t.Fatalf("tick %d: event = %q, want empty — still the initial connect, not a new disconnect", i, event)
		}
		seeded = next
	}
}

func TestDiff_Reconnect_AfterRealDisconnect(t *testing.T) {
	state := trackedState{}
	event, state := diff(state, true, snapshot{down: true})
	if event != "disconnected" {
		t.Fatalf("setup: event = %q, want %q", event, "disconnected")
	}

	event, state = diff(state, true, snapshot{connected: true})
	if event != "reconnected" {
		t.Errorf("event = %q, want %q", event, "reconnected")
	}
	if state.notifiedDown {
		t.Error("notifiedDown = true, want false after a reconnect notification")
	}
}

func TestDiff_AutoPause_OnlyNotifiesOnceUntilResumed(t *testing.T) {
	state := trackedState{}

	event, state := diff(state, true, snapshot{autoPaused: true, failures: 3})
	if event != "auto_paused" {
		t.Fatalf("event = %q, want %q", event, "auto_paused")
	}

	// Stays auto-paused on subsequent ticks — no repeat notification.
	event, state = diff(state, true, snapshot{autoPaused: true, failures: 3})
	if event != "" {
		t.Errorf("event = %q, want empty while still auto-paused", event)
	}

	// Resumed (no longer auto-paused), then auto-paused again — new edge, new notification.
	event, state = diff(state, true, snapshot{connected: true})
	if event != "" {
		t.Errorf("event = %q, want empty on resume", event)
	}
	event, _ = diff(state, true, snapshot{autoPaused: true, failures: 5})
	if event != "auto_paused" {
		t.Errorf("event = %q, want %q on a new auto-pause cycle", event, "auto_paused")
	}
}

func TestDiff_ManualPause_NoNotification(t *testing.T) {
	// A manual pause is neither "down" nor "autoPaused" — the watcher must
	// treat it as a distinct, non-notifying bucket.
	state := trackedState{}
	event, next := diff(state, true, snapshot{connected: false, down: false, autoPaused: false})
	if event != "" {
		t.Errorf("event = %q, want empty for a manual pause", event)
	}
	if next.notifiedDown || next.notifiedAutoPause {
		t.Errorf("next = %+v, want no flags set for a manual pause", next)
	}
}

func TestTunnelSnapshot_ManualVsAutoPause(t *testing.T) {
	// Both states report Status == StatusPaused; only AutoPaused differs.
	manual := tunnelSnapshot(tunnel.Stats{Status: tunnel.StatusPaused, AutoPaused: false})
	auto := tunnelSnapshot(tunnel.Stats{Status: tunnel.StatusPaused, AutoPaused: true})

	if manual.down || manual.autoPaused {
		t.Errorf("manual pause snapshot = %+v, want down=false autoPaused=false", manual)
	}
	if auto.down || !auto.autoPaused {
		t.Errorf("auto pause snapshot = %+v, want down=false autoPaused=true", auto)
	}
}
