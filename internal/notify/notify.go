// Package notify sends native OS desktop notifications when a tunnel or VPN
// connection undergoes a meaningful state transition (unexpected disconnect,
// recovery, or auto-pause after repeated failures).
package notify

import (
	_ "embed"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/log"
	"github.com/gen2brain/beeep"

	"github.com/pottom/hopscotch/internal/config"
)

// suppressWindow is how long a Suppress call shields a name from
// disconnect/reconnect notifications — long enough to cover a normal manual
// reconnect (dial + handshake, typically well under 5s) with margin.
const suppressWindow = 15 * time.Second

// icon is the hopscotch logo (rasterized from docs/logo.svg — regenerate with
// `magick -background none -density 384 docs/logo.svg -resize 256x256
// internal/notify/assets/icon.png` if the logo changes) shown alongside
// notifications on platforms whose backend supports a custom icon (Linux via
// D-Bus, Windows toast). On macOS this has no effect: modern macOS (confirmed
// on 26.5.1) blocks custom notification icons for unbundled CLI/daemon tools
// even via `terminal-notifier`'s `-appIcon`/`-sender` with permission granted
// — a real custom icon there would require shipping hopscotch as a signed
// .app bundle and posting via UNUserNotificationCenter (cgo, plus a real
// bundle identity), which conflicts with the CGO_ENABLED=0 single-runner
// cross-compile pipeline (see build.sh) and isn't worth it for this. macOS
// notifications still show correctly, just with the generic/terminal icon.
//
//go:embed assets/icon.png
var icon []byte

// Notifier sends gated, best-effort desktop notifications. Delivery failures
// (no notification daemon on a headless Linux box, a failing osascript call,
// etc.) are logged and swallowed — they must never affect the tunnel/VPN
// control loops that trigger them.
//
// cfg is held in an atomic.Value so SetConfig (driven by a live TUI/web UI
// edit) can be called concurrently with the frequent reads in Disconnected/
// Reconnected/AutoPaused/Suppress without a lock on the hot path.
type Notifier struct {
	cfg atomic.Value // config.NotificationsConfig

	mu              sync.Mutex
	suppressedUntil map[string]time.Time // key: kind+"/"+name
}

// New creates a Notifier. Safe to construct and use unconditionally even when
// cfg.Enabled is false — every method becomes a cheap no-op.
func New(cfg config.NotificationsConfig) *Notifier {
	n := &Notifier{suppressedUntil: make(map[string]time.Time)}
	n.cfg.Store(cfg)
	return n
}

// Config returns the currently active notification settings.
func (n *Notifier) Config() config.NotificationsConfig {
	return n.cfg.Load().(config.NotificationsConfig)
}

// SetConfig atomically replaces the active notification settings — e.g. after
// a live edit from the TUI or web UI Settings tab. Takes effect immediately:
// Watch's polling loop and the admin reconnect/resume handlers always read
// the latest value, so no restart or reload is needed.
func (n *Notifier) SetConfig(cfg config.NotificationsConfig) {
	n.cfg.Store(cfg)
}

// Suppress marks kind/name's next disconnect/reconnect cycle as intentional
// (e.g. a user-triggered "force reconnect" from the admin API or TUI) so
// Watch does not report it as an unexpected drop. A cheap no-op when
// notifications are disabled or n is nil, so callers can call it
// unconditionally without checking config first.
func (n *Notifier) Suppress(kind, name string) {
	if n == nil || !n.Config().Enabled {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	n.suppressedUntil[kind+"/"+name] = time.Now().Add(suppressWindow)
}

// suppressed reports whether kind/name is currently within a Suppress window,
// pruning the entry once it's stale.
func (n *Notifier) suppressed(kind, name string) bool {
	if n == nil {
		return false
	}
	key := kind + "/" + name
	n.mu.Lock()
	defer n.mu.Unlock()
	until, ok := n.suppressedUntil[key]
	if !ok {
		return false
	}
	if time.Now().After(until) {
		delete(n.suppressedUntil, key)
		return false
	}
	return true
}

func (n *Notifier) send(title, message string) {
	// Alert is identical to Notify except it also plays the OS default
	// notification sound; both take the same (title, message, icon) shape.
	deliver := beeep.Notify
	if n.Config().Sound {
		deliver = beeep.Alert
	}
	if err := deliver(title, message, icon); err != nil {
		log.Debug("desktop notification delivery failed", "err", err)
	}
}

// displayKind renders "tunnel"/"vpn" the way it should read in a notification
// body: capitalized, with VPN as an acronym.
func displayKind(kind string) string {
	if kind == "vpn" {
		return "VPN"
	}
	return "Tunnel"
}

// Disconnected reports that a previously-connected tunnel or VPN unexpectedly
// lost its connection. kind is "tunnel" or "vpn". The tunnel/VPN name is the
// title so it's the first thing seen; the body states just the fact.
func (n *Notifier) Disconnected(kind, name string) {
	cfg := n.Config()
	if !cfg.Enabled || !cfg.OnDisconnect {
		return
	}
	n.send(name, displayKind(kind)+" disconnected")
}

// Reconnected reports that a tunnel or VPN recovered after being down.
func (n *Notifier) Reconnected(kind, name string) {
	cfg := n.Config()
	if !cfg.Enabled || !cfg.OnReconnect {
		return
	}
	n.send(name, displayKind(kind)+" reconnected")
}

// AutoPaused reports that a tunnel or VPN was auto-paused after repeated
// consecutive failed connection attempts.
func (n *Notifier) AutoPaused(kind, name string, failures int) {
	cfg := n.Config()
	if !cfg.Enabled || !cfg.OnAutoPause {
		return
	}
	n.send(name, fmt.Sprintf("%s auto-paused after %d failures", displayKind(kind), failures))
}
