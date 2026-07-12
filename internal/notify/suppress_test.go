package notify

import (
	"testing"
	"time"

	"github.com/pottom/hopscotch/internal/config"
)

func TestSuppress_ActiveImmediatelyAfterCall(t *testing.T) {
	n := New(config.NotificationsConfig{Enabled: true})
	if n.suppressed("tunnel", "foo") {
		t.Fatal("suppressed = true before any Suppress call")
	}
	n.Suppress("tunnel", "foo")
	if !n.suppressed("tunnel", "foo") {
		t.Error("suppressed = false immediately after Suppress")
	}
}

func TestSuppress_ExpiresAndIsPruned(t *testing.T) {
	n := New(config.NotificationsConfig{Enabled: true})
	n.Suppress("tunnel", "foo")
	n.suppressedUntil["tunnel/foo"] = time.Now().Add(-time.Second) // force expiry

	if n.suppressed("tunnel", "foo") {
		t.Error("suppressed = true past the window")
	}
	if _, stillThere := n.suppressedUntil["tunnel/foo"]; stillThere {
		t.Error("expired entry was not pruned from the map")
	}
}

func TestSuppress_DisabledConfig_NoOp(t *testing.T) {
	// A manual reconnect handler calls Suppress unconditionally; it must not
	// start tracking state when notifications are off.
	n := New(config.NotificationsConfig{Enabled: false})
	n.Suppress("tunnel", "foo")
	if n.suppressed("tunnel", "foo") {
		t.Error("suppressed = true even though notifications are disabled")
	}
}

func TestSuppress_NilReceiver_NoPanic(t *testing.T) {
	var n *Notifier
	n.Suppress("tunnel", "foo") // must not panic
	if n.suppressed("tunnel", "foo") {
		t.Error("nil Notifier reported as suppressed")
	}
}

func TestSuppress_ScopedByKindAndName(t *testing.T) {
	n := New(config.NotificationsConfig{Enabled: true})
	n.Suppress("tunnel", "foo")

	if n.suppressed("vpn", "foo") {
		t.Error("suppression leaked across kind")
	}
	if n.suppressed("tunnel", "bar") {
		t.Error("suppression leaked across name")
	}
}
