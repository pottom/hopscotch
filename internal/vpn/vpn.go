// Package vpn manages SSL VPN connections as subprocess lifecycle.
package vpn

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/log"

	"github.com/pottom/hopscotch/internal/msgs"
	"github.com/pottom/hopscotch/internal/netcheck"
)

// Stats is a point-in-time snapshot of one VPN connection.
type Stats struct {
	State               State
	Reconnects          int
	ConnectedAt         time.Time // zero if never connected
	Server              string    // hostname extracted from server URL
	NextReconnectAt     time.Time // non-zero only while waiting to reconnect
	LastError           string    // last error from subprocess; empty when connected
	TunIface            string    // tunnel interface name (e.g. utun2, tun0); empty until detected
	ConsecutiveFailures int       // consecutive failed connection attempts; resets to 0 on success or resume
	AutoPauseThreshold  int       // config value; 0 = auto-pause disabled
	AutoPaused          bool      // true if the current pause (if any) was triggered by auto_pause_threshold, not a manual Pause()
}

// State represents the lifecycle state of a VPN connection.
type State int32

const (
	StateDisconnected State = iota
	StateConnecting
	StateConnected
	StatePaused // manually paused; not retrying until resumed
)

func (s State) String() string {
	switch s {
	case StateConnecting:
		return "connecting"
	case StateConnected:
		return "connected"
	case StatePaused:
		return "paused"
	default:
		return "disconnected"
	}
}

// connConfig holds all parameters for one VPN connection.
type connConfig struct {
	Name               string
	Binary             string // path to openconnect binary; default: "openconnect"
	Server             string
	User               string
	AuthGroup          string
	PasswordEnv        string
	PasswordCmd        string
	Certificate        string
	Key                string
	PingHost           string // host[:port] TCP-probed to confirm VPN connectivity
	ConnectTimeout     int    // seconds ping_host may stay unreachable after launch; <= 0 means 30
	HoldDarkSession    bool   // keep a dark session open during its cooldown instead of tearing it down (experimental)
	ExtraArgs          []string
	PreConnect         []string // commands to run before each connection attempt
	PostDisconnect     []string // commands to run after each VPN disconnect
	Sudo               bool
	DNSResolver        string // host:port; default "1.1.1.1:53"
	ReconnectDelay     int
	ReconnectMaxDelay  int
	AutoPauseThreshold int // consecutive failed connection attempts before auto-pausing; 0 disables
	AutoResumeAfter    int // seconds after an auto-pause before retrying automatically; 0 disables
}

// Connection manages one VPN subprocess.
type Connection struct {
	cfg             connConfig
	state           atomic.Int32
	reconnects      atomic.Int32
	connectedAt     atomic.Value // stores time.Time; zero until first connect
	nextReconnectAt atomic.Value // stores time.Time; non-zero while waiting to reconnect
	lastError       atomic.Value // stores string; last subprocess error
	tunIface        atomic.Value // stores string; tunnel interface name
	tunIfaceIndex   atomic.Int32 // kernel index of tunIface when recorded; 0 = unknown
	tunIfacesBefore atomic.Value // stores map[string]bool; tun interfaces before this runOnce
	forceReconnect  chan struct{}
	paused          atomic.Bool
	pauseRequest    chan struct{} // buffered(1); signals a pause request
	resume          chan struct{} // buffered(1); signals resume from pause

	// lastAttemptDark is set by pollPingHost when the attempt ended because the
	// gateway returned no traffic (see dark.go); cleared when an attempt starts
	// or the VPN connects.
	lastAttemptDark atomic.Bool
	// darkStreak counts dark attempts in a row. Written by Run(), read by
	// pollPingHost (hold_dark_session); reset on connect, resume and force
	// reconnect.
	darkStreak atomic.Int32
	// heldCooldown is set by pollPingHost when hold_dark_session already kept a
	// dark session open for the dark-streak floor, so Run() doesn't wait it out
	// again.
	heldCooldown atomic.Bool
	// sessionIP is the client address the gateway assigned in the current
	// attempt ("Configured as ..."); "" until seen.
	sessionIP atomic.Value

	// history records every attempt and feeds the dark-retry policy
	// (retrypolicy.go). Shared by the Manager's VPNs; nil records nothing.
	history *sessionHistory
	// rnd drives the policy's exploration; replaced in tests.
	rnd func() float64
	// attempt runs one connection attempt: runOnce, replaced in tests.
	attempt func(ctx context.Context) error

	// consecutiveFailures counts connection attempts in a row that never
	// reached StateConnected; reset to 0 on a successful connect or a manual
	// Resume(). Written by both the Run() goroutine and Resume() (a
	// different goroutine), hence atomic.
	consecutiveFailures atomic.Int32
	// autoPaused is true while the current pause was triggered by
	// auto_pause_threshold rather than a manual Pause() call — lets the UI
	// distinguish the two. Written by both the Run() goroutine and Pause()/
	// Resume() (a different goroutine), hence atomic.
	autoPaused atomic.Bool

	// pauseMu serializes every read-then-write transition of the pause state
	// (paused/autoPaused/consecutiveFailures): Pause(), Resume(), Run()'s
	// auto-pause trigger, and Run()'s auto-resume-timer fire all take it for
	// their whole check+mutate sequence, so none of them can interleave and
	// leave the three fields in a self-contradictory combination (e.g. a
	// concurrent Resume() landing between the auto-resume case's re-check of
	// autoPaused and its own Store calls).
	pauseMu sync.Mutex
}

func newConnection(cfg connConfig) *Connection {
	c := &Connection{
		cfg:            cfg,
		forceReconnect: make(chan struct{}, 1),
		pauseRequest:   make(chan struct{}, 1),
		resume:         make(chan struct{}, 1),
	}
	c.connectedAt.Store(time.Time{})
	c.nextReconnectAt.Store(time.Time{})
	c.lastError.Store("")
	c.tunIface.Store("")
	c.tunIfacesBefore.Store(map[string]bool{})
	c.attempt = c.runOnce
	c.sessionIP.Store("")
	c.rnd = rand.Float64
	return c
}

// ForceReconnect interrupts the current backoff wait, triggering an immediate reconnect.
func (c *Connection) ForceReconnect() {
	select {
	case c.forceReconnect <- struct{}{}:
	default:
	}
}

// Pause stops the VPN from retrying, tearing down an in-flight or active
// subprocess via the same graceful (SIGTERM-then-SIGKILL) shutdown path used
// on ForceReconnect/shutdown. The connection stays paused until Resume.
// Called only from the admin API on a user's explicit request — marks the
// pause as manual (as opposed to Run()'s own auto-pause) so Stats().AutoPaused
// lets the UI show which one happened.
func (c *Connection) Pause() {
	c.pauseMu.Lock()
	defer c.pauseMu.Unlock()
	c.autoPaused.Store(false)
	c.pauseLocked()
}

// pauseLocked is the actual pause mechanics, shared by the manual Pause()
// above and Run()'s auto-pause trigger — kept separate so auto-pause can set
// autoPaused=true first without Pause() immediately overwriting it back to
// false. Callers must hold pauseMu.
func (c *Connection) pauseLocked() {
	c.paused.Store(true)
	select {
	case c.pauseRequest <- struct{}{}:
	default:
	}
}

// Resume clears a paused VPN connection and triggers an immediate reconnect.
// Resets the auto-pause failure counter so a fresh full attempt budget
// starts, regardless of whether the connection was paused manually or
// automatically.
func (c *Connection) Resume() {
	c.pauseMu.Lock()
	defer c.pauseMu.Unlock()
	c.paused.Store(false)
	c.consecutiveFailures.Store(0)
	c.autoPaused.Store(false)
	select {
	case c.resume <- struct{}{}:
	default:
	}
}

// detectTunIface finds the tunnel interface created by openconnect by comparing
// current network interfaces against the snapshot taken before the connection attempt.
// No-op if tunIface is already known (e.g. detected from stderr on Linux).
func (c *Connection) detectTunIface() {
	if c.tunIface.Load().(string) != "" {
		return
	}
	if name := c.newTunIface(); name != "" {
		c.setTunIface(name)
		log.Info("vpn tunnel interface detected", "vpn", c.cfg.Name, "iface", name)
	}
}

// newTunIface returns a tunnel interface that didn't exist when this attempt
// started, or "".
func (c *Connection) newTunIface() string {
	before, _ := c.tunIfacesBefore.Load().(map[string]bool)
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range ifaces {
		if (strings.HasPrefix(iface.Name, "utun") || strings.HasPrefix(iface.Name, "tun")) && !before[iface.Name] {
			return iface.Name
		}
	}
	return ""
}

// holdDarkSessionFor returns how long pollPingHost should keep a dark session
// open instead of tearing it down, or 0. Only with hold_dark_session, and only
// when the dark session about to end reaches the streak floor, where a long
// pause without new sessions is enforced anyway (retrypolicy.go).
func (c *Connection) holdDarkSessionFor() time.Duration {
	if !c.cfg.HoldDarkSession || int(c.darkStreak.Load())+1 < darkRetryFloorStreak {
		return 0
	}
	return darkRetryFloor
}

// setTunIface records the tunnel interface name together with its kernel
// index. Every VPN gets the same name after a switch (tun0 on Linux: the old
// device is gone, so the next one reuses the name), so the index is what tells
// this connection's device apart from a later, same-named one.
func (c *Connection) setTunIface(name string) {
	var index int32
	if iface, err := net.InterfaceByName(name); err == nil {
		index = int32(iface.Index)
	}
	c.tunIface.Store(name)
	c.tunIfaceIndex.Store(index)
}

// State returns the current VPN connection state.
func (c *Connection) State() State { return State(c.state.Load()) }

// Name returns the configured VPN name.
func (c *Connection) Name() string { return c.cfg.Name }

// Stats returns a point-in-time snapshot of the connection.
func (c *Connection) Stats() Stats {
	server := c.cfg.Server
	if u, err := url.Parse(c.cfg.Server); err == nil && u.Host != "" {
		server = u.Host
	}
	return Stats{
		State:               State(c.state.Load()),
		Reconnects:          int(c.reconnects.Load()),
		ConnectedAt:         c.connectedAt.Load().(time.Time),
		Server:              server,
		NextReconnectAt:     c.nextReconnectAt.Load().(time.Time),
		LastError:           c.lastError.Load().(string),
		TunIface:            c.tunIface.Load().(string),
		ConsecutiveFailures: int(c.consecutiveFailures.Load()),
		AutoPauseThreshold:  c.cfg.AutoPauseThreshold,
		AutoPaused:          c.autoPaused.Load(),
	}
}

func (c *Connection) setState(s State) {
	if s == StateConnected {
		if State(c.state.Load()) != StateConnected {
			c.connectedAt.Store(time.Now().Round(0)) // strip monotonic reading so uptime survives system sleep
		}
		c.nextReconnectAt.Store(time.Time{})
		c.lastError.Store("")
	}
	c.state.Store(int32(s))
}

// Run manages the VPN subprocess lifecycle with exponential backoff reconnects.
// Blocks until ctx is cancelled.
func (c *Connection) Run(ctx context.Context) error {
	initial := time.Duration(c.cfg.ReconnectDelay) * time.Second
	b := &backoff{
		initial: initial,
		current: initial,
		max:     time.Duration(c.cfg.ReconnectMaxDelay) * time.Second,
	}

	for {
		if c.paused.Load() {
			c.setState(StatePaused)
			c.lastError.Store("")
			c.nextReconnectAt.Store(time.Time{})

			// Only an auto-pause (never a manual Pause()) is eligible to retry on
			// its own — a human's explicit pause stays paused until they act.
			var autoResume <-chan time.Time
			if c.autoPaused.Load() && c.cfg.AutoResumeAfter > 0 {
				autoResume = time.After(time.Duration(c.cfg.AutoResumeAfter) * time.Second)
			}

			select {
			case <-ctx.Done():
				c.setState(StateDisconnected)
				return nil
			case <-c.resume:
				b.reset()
				c.darkStreak.Store(0)
			case <-autoResume:
				// Re-check under pauseMu: a manual Pause()/Resume() may have landed
				// while the cooldown was armed, and must not be clobbered by this
				// timer firing concurrently with it.
				c.pauseMu.Lock()
				if c.autoPaused.Load() {
					log.Info("vpn auto-resuming after cooldown",
						"vpn", c.cfg.Name,
						"cooldown", c.cfg.AutoResumeAfter,
					)
					c.paused.Store(false)
					c.consecutiveFailures.Store(0)
					c.autoPaused.Store(false)
					b.reset()
					c.darkStreak.Store(0)
				}
				c.pauseMu.Unlock()
			}
			continue
		}

		// Discard a stale pause signal left over from a Pause() call whose effect
		// (the paused-wait above, or a teardown already in progress) was already
		// applied — otherwise a later select in this loop could misread it as a
		// brand new pause request.
		select {
		case <-c.pauseRequest:
		default:
		}

		c.setState(StateConnecting)
		c.lastAttemptDark.Store(false)
		c.heldCooldown.Store(false)

		beforeRun := time.Now()
		c.sessionIP.Store("")
		session := c.history.startContext(c.cfg.Name, beforeRun)
		session.DarkStreak = int(c.darkStreak.Load())

		// Run the subprocess in a goroutine so forceReconnect/Pause can interrupt
		// it even while the VPN is connected (not just during the backoff countdown).
		runCtx, cancelRun := context.WithCancel(ctx)
		errCh := make(chan error, 1)
		go func() { errCh <- c.attempt(runCtx) }()

		forceSkipDelay := false
		pausedThisRound := false
		select {
		case err := <-errCh:
			cancelRun()
			if ctx.Err() != nil {
				c.setState(StateDisconnected)
				c.nextReconnectAt.Store(time.Time{})
				return nil
			}
			if err != nil {
				if c.lastError.Load().(string) == "" {
					c.lastError.Store(err.Error())
				}
			}
		case <-c.forceReconnect:
			// Signal the UI immediately — don't wait for the subprocess to exit first.
			c.setState(StateConnecting)
			log.Info("force reconnect requested", "vpn", c.cfg.Name)
			c.darkStreak.Store(0)
			cancelRun()
			<-errCh // wait for subprocess to exit
			forceSkipDelay = true
		case <-c.pauseRequest:
			// Reuse the same graceful subprocess teardown as shutdown/ForceReconnect.
			log.Info("pause requested", "vpn", c.cfg.Name)
			cancelRun()
			<-errCh // wait for subprocess to exit
			pausedThisRound = true
		}

		if !forceSkipDelay && !pausedThisRound {
			c.setState(StateDisconnected)
		}
		c.reconnects.Add(1)

		connectedThisRun := c.connectedAt.Load().(time.Time).After(beforeRun)
		dark := !connectedThisRun && c.lastAttemptDark.Load()
		c.recordSession(session, time.Now(), connectedThisRun, dark, forceSkipDelay || pausedThisRound)
		if dark {
			c.darkStreak.Add(1)
		}

		// If the VPN reached StateConnected during this run, reset the backoff —
		// only runs that never connected (e.g. auth failures, bad routes) should
		// accumulate reconnect delay.
		if connectedThisRun {
			b.reset()
			c.darkStreak.Store(0)
			c.consecutiveFailures.Store(0)
		} else if !pausedThisRound && c.cfg.AutoPauseThreshold > 0 {
			n := c.consecutiveFailures.Add(1)
			if int(n) >= c.cfg.AutoPauseThreshold {
				log.Warn("vpn auto-paused: too many consecutive connection failures",
					"vpn", c.cfg.Name,
					"failures", n,
					"threshold", c.cfg.AutoPauseThreshold,
				)
				c.pauseMu.Lock()
				c.autoPaused.Store(true)
				c.pauseLocked()
				c.pauseMu.Unlock()
				pausedThisRound = true
			}
		}

		if pausedThisRound {
			// Status will be set to StatePaused at the top of the loop.
			continue
		}

		if forceSkipDelay {
			continue
		}

		// If there's no network at all, wait for it before the next attempt.
		// Skip the backoff countdown after restore — waiting for the network
		// already served as the delay.
		if !netcheck.HasUplink() {
			c.lastError.Store(msgs.WaitingForNetwork)
			log.Info("vpn waiting for network", "vpn", c.cfg.Name)
			if err := netcheck.WaitForUplink(ctx); err != nil {
				c.nextReconnectAt.Store(time.Time{})
				return nil
			}
			c.lastError.Store("")
			b.reset()
			log.Info("network up, reconnecting vpn immediately", "vpn", c.cfg.Name)
			continue
		}

		var delay time.Duration
		if dark {
			// How long to wait after a dark session is learned from the
			// recorded history (retrypolicy.go); the exponential backoff below
			// is for other failures.
			streak := int(c.darkStreak.Load())
			decision := chooseDarkRetry(c.history.snapshot(), c.cfg.Name, streak, time.Now(), c.rnd)
			delay = decision.Delay
			if c.heldCooldown.Swap(false) && delay >= darkRetryFloor {
				// hold_dark_session already kept the session open for the floor.
				delay = darkRetryDefault
			}
			c.lastError.Store(fmt.Sprintf("gateway returned no traffic (%d session(s) in a row); next session in %s — %s",
				streak, delay.Round(time.Second), decision.Reason))
			log.Warn("vpn: gateway returned no traffic, waiting before the next session",
				"vpn", c.cfg.Name, "dark_in_a_row", streak, "delay", delay, "why", decision.Reason, "evidence", decision.Detail)
		} else {
			delay = b.next()
			log.Warn("vpn disconnected, reconnecting", "vpn", c.cfg.Name, "delay", delay)
		}
		c.nextReconnectAt.Store(time.Now().Add(delay))
		select {
		case <-ctx.Done():
			c.nextReconnectAt.Store(time.Time{})
			return nil
		case <-time.After(delay):
			c.nextReconnectAt.Store(time.Time{})
		case <-c.forceReconnect:
			c.nextReconnectAt.Store(time.Time{})
			c.darkStreak.Store(0)
			log.Info("force reconnect requested, skipping delay", "vpn", c.cfg.Name)
		case <-c.pauseRequest:
			c.nextReconnectAt.Store(time.Time{})
			log.Info("pause requested, skipping delay", "vpn", c.cfg.Name)
		}
	}
}

// recordSession completes the start context of an attempt with its outcome,
// adds it to the history and logs one summary line per attempt.
func (c *Connection) recordSession(r SessionRecord, end time.Time, connected, dark, cut bool) {
	r.End = end
	switch {
	case connected:
		r.Outcome = OutcomeOK
		if at := c.connectedAt.Load().(time.Time); at.After(r.Start) {
			r.ConnectedAfter = at.Sub(r.Start).Seconds()
		}
	case dark:
		r.Outcome = OutcomeDark
	case cut:
		r.Outcome = OutcomeCut
	default:
		r.Outcome = OutcomeFailed
	}
	r.IP, _ = c.sessionIP.Load().(string)
	c.history.add(r)
	log.Info("vpn session",
		"vpn", r.VPN,
		"outcome", r.Outcome,
		"lasted", end.Sub(r.Start).Round(time.Second),
		"since_own", fmtSince(r.SinceOwn),
		"prev_dark", r.PrevDark,
		"dark_streak", r.DarkStreak,
		"starts_10m", r.Starts10m,
		"after_switch", r.AfterSwitch,
		"ip", r.IP,
	)
}

func fmtSince(seconds float64) string {
	if seconds < 0 {
		return "none"
	}
	return time.Duration(seconds * float64(time.Second)).Round(time.Second).String()
}

type backoff struct {
	initial time.Duration
	current time.Duration
	max     time.Duration
}

func (b *backoff) next() time.Duration {
	d := b.current
	b.current = min(b.current*2, b.max)
	return d
}

func (b *backoff) reset() {
	b.current = b.initial
}
