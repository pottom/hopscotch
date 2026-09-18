package vpn

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/charmbracelet/log"

	"github.com/pottom/hopscotch/internal/keychain"
	"github.com/pottom/hopscotch/internal/msgs"
	"github.com/pottom/hopscotch/internal/netcheck"
)

// runOnce starts the openconnect subprocess and blocks until it exits or ctx is cancelled.
func (c *Connection) runOnce(ctx context.Context) error {
	binary := c.cfg.Binary
	if binary == "" {
		binary = "openconnect"
	}
	pattern := c.procPattern()

	// Kill any orphaned instance of this VPN left over from a previous abrupt
	// shutdown before launching a new one — avoids route/interface conflicts.
	killOrphanedProcs(pattern, c.cfg.Sudo)

	// Reset tunnel interface and snapshot existing tun interfaces before openconnect starts.
	// After connect we diff against this to identify the new VPN interface (macOS utun* detection).
	c.tunIface.Store("")
	c.tunIfaceIndex.Store(0)
	before := map[string]bool{}
	if ifaces, err := net.Interfaces(); err == nil {
		for _, iface := range ifaces {
			if strings.HasPrefix(iface.Name, "utun") || strings.HasPrefix(iface.Name, "tun") {
				before[iface.Name] = true
			}
		}
	}
	c.tunIfacesBefore.Store(before)

	if err := c.runPreConnect(ctx); err != nil {
		return err
	}

	// Resolve the VPN server hostname before starting openconnect.
	// We pass the resolved IP via --resolve so openconnect doesn't need to do
	// its own DNS lookup — this avoids failures when system DNS is temporarily
	// unavailable (e.g. right after a network change or post_disconnect cleanup).
	resolveArg, err := c.resolveServer(ctx)
	if err != nil {
		return err
	}

	// On macOS, openconnect adds a host route for the VPN server IP to prevent
	// routing server traffic through the VPN tunnel. After an abrupt disconnect
	// (vpnc-script didn't run), this route remains and may point to a stale
	// gateway — causing TCP connect to fail immediately on the next attempt.
	if resolveArg != "" && runtime.GOOS == "darwin" {
		if colon := strings.LastIndex(resolveArg, ":"); colon != -1 {
			if ip := resolveArg[colon+1:]; net.ParseIP(ip) != nil {
				c.deleteStaleServerRouteDarwin(ctx, ip)
			}
		}
	}

	// Resolve password once — needed both for --passwd-on-stdin flag and stdin feed.
	pw := c.password()
	args := c.buildArgs(pw != "", resolveArg)

	var cmd *exec.Cmd
	if c.cfg.Sudo {
		cmd = exec.CommandContext(ctx, "sudo", append([]string{binary}, args...)...)
	} else {
		cmd = exec.CommandContext(ctx, binary, args...)
	}

	// Log full command at debug level (mask password via --passwd-on-stdin).
	{
		fullArgs := make([]string, 0, len(args)+2)
		if c.cfg.Sudo {
			fullArgs = append(fullArgs, "sudo")
		}
		fullArgs = append(fullArgs, binary)
		fullArgs = append(fullArgs, args...)
		log.Debug("vpn: launching subprocess", "vpn", c.cfg.Name, "cmd", strings.Join(fullArgs, " "))
	}

	// New process group so we can kill sudo + its children (e.g. openconnect)
	// as a unit on shutdown. Without this, killing sudo leaves openconnect
	// orphaned with the pipe write-end open, which blocks cmd.Wait() forever.
	setProcGroup(cmd)

	if pw != "" {
		// Use os.Pipe so the child stdin is an *os.File — exec then passes it
		// directly to the subprocess without starting an internal goroutine.
		// strings.NewReader would create a goroutine that cmd.Wait() waits for,
		// and that goroutine blocks until all holders of the pipe read-end close
		// it — including orphaned openconnect subprocesses — which would cause
		// cmd.Wait() to hang indefinitely after we kill sudo.
		stdinR, stdinW, pipeErr := os.Pipe()
		if pipeErr != nil {
			return fmt.Errorf("stdin pipe: %w", pipeErr)
		}
		if _, err := stdinW.WriteString(pw + "\n"); err != nil {
			stdinR.Close()
			stdinW.Close()
			return fmt.Errorf("writing password to stdin pipe: %w", err)
		}
		stdinW.Close() // write end closed; child reads password then gets EOF
		cmd.Stdin = stdinR
		defer stdinR.Close()
	}

	// Force English output so log lines are predictable regardless of system locale.
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")

	// openconnect writes errors to stderr but progress ("Connected as",
	// "Set up tun device", ...) to stdout, so both go into one pipe. Both ends
	// are *os.File so exec hands the write end straight to the child: an
	// io.Writer would start a copy goroutine that cmd.Wait() blocks on for as
	// long as an orphaned openconnect keeps the write end open.
	output, outputW, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("output pipe: %w", err)
	}
	cmd.Stdout = outputW
	cmd.Stderr = outputW

	err = cmd.Start()
	outputW.Close() // the child holds its own copy; EOF arrives once it exits
	if err != nil {
		output.Close()
		return fmt.Errorf("starting openconnect: %w", err)
	}
	log.Info("vpn subprocess started", "vpn", c.cfg.Name, "pid", cmd.Process.Pid)
	c.lastError.Store("openconnect starting")

	// outputDone is closed when runOnce() returns so watchOutput stops
	// updating connection state after we've moved on to the reconnect cycle.
	outputDone := make(chan struct{})
	defer close(outputDone)

	// Watch output lines for status events. The read end is closed at EOF, or
	// earlier by the uplink-kill path below.
	go func() {
		c.watchOutput(output, outputDone)
		output.Close()
	}()

	// done carries the cmd.Wait() error; died is closed after Wait() returns so
	// multiple goroutines (pollPingHost, etc.) can all observe subprocess exit
	// without competing for a single channel receive.
	done := make(chan error, 1)
	died := make(chan struct{})
	go func() {
		done <- cmd.Wait()
		close(died)
	}()

	// Kill subprocess if network disappears while it's running.
	// killedByUplink is closed when watchUplink() triggers the kill, so
	// runOnce() can unblock even if cmd.Wait() is stuck (e.g. openconnect in
	// a different process group didn't receive the SIGKILL via the group kill).
	killedByUplink := make(chan struct{})
	go c.watchUplink(ctx, cmd, died, killedByUplink)

	if c.cfg.PingHost != "" {
		// Poll ping_host to detect when VPN is up and when it drops.
		go c.pollPingHost(ctx, cmd, died)
	} else {
		// No ping host: assume connected after a short startup delay.
		go func() {
			select {
			case <-time.After(8 * time.Second):
				if c.State() == StateConnecting {
					c.setState(StateConnected)
					log.Info("vpn assumed connected (no ping_host configured)", "vpn", c.cfg.Name)
				}
			case <-died:
			case <-ctx.Done():
			}
		}()
	}

	select {
	case err := <-done:
		if err != nil && ctx.Err() == nil {
			log.Warn("vpn subprocess exited", "vpn", c.cfg.Name, "err", err)
		}
		c.runPostDisconnect()
		return err
	case <-ctx.Done():
		// SIGTERM first so vpnc-script can run and clean up routes/DNS.
		// This avoids leaving 50+ routes that flushTunRoutesDarwin would have
		// to delete one by one via sudo, which slows down shutdown significantly.
		log.Info("vpn: shutting down, sending SIGTERM to subprocess", "vpn", c.cfg.Name)
		terminateProcs(pattern, c.cfg.Sudo)
		select {
		case <-done:
			// Exited cleanly — vpnc-script ran, routes cleaned up.
			log.Info("vpn: subprocess exited cleanly on SIGTERM", "vpn", c.cfg.Name)
		case <-time.After(4 * time.Second):
			// Didn't exit on SIGTERM; force-kill the whole process group.
			log.Warn("vpn: subprocess did not exit on SIGTERM, sending SIGKILL", "vpn", c.cfg.Name)
			killProcGroup(cmd)
			select {
			case <-done:
				log.Info("vpn: subprocess exited after SIGKILL", "vpn", c.cfg.Name)
			case <-time.After(2 * time.Second):
				log.Warn("vpn subprocess did not exit after kill", "vpn", c.cfg.Name)
			}
		}
		log.Info("vpn: running post-disconnect cleanup", "vpn", c.cfg.Name)
		c.runPostDisconnect()
		log.Info("vpn: shutdown complete", "vpn", c.cfg.Name)
		return ctx.Err()
	case <-killedByUplink:
		// watchUplink() sent SIGKILL to the process group. If openconnect ran
		// in a different process group (e.g. via sudo on some systems), it may
		// not have received the kill, keeping sudo (and so cmd.Wait()) alive.
		// Give it 1 s then close the output pipe — this stops watchOutput from
		// updating state AND sends EPIPE/SIGPIPE to openconnect which should
		// terminate it.
		select {
		case <-done:
		case <-time.After(time.Second):
			log.Warn("vpn subprocess still alive after uplink kill; closing pipe", "vpn", c.cfg.Name)
			output.Close()
			killOrphanedProcs(pattern, c.cfg.Sudo)
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				log.Warn("vpn subprocess orphaned after pipe close; proceeding to reconnect", "vpn", c.cfg.Name)
			}
		}
		c.runPostDisconnect()
		return errors.New("network uplink lost")
	}
}

// password returns the VPN password. Priority: password_env > password_cmd > keychain.
// Returns empty string if none is configured or all fail.
func (c *Connection) password() string {
	if c.cfg.PasswordEnv != "" {
		return os.Getenv(c.cfg.PasswordEnv)
	}
	if c.cfg.PasswordCmd != "" {
		var cmd *exec.Cmd
		if runtime.GOOS == "windows" {
			cmd = exec.Command("cmd", "/C", c.cfg.PasswordCmd)
		} else {
			cmd = exec.Command("sh", "-c", c.cfg.PasswordCmd)
		}
		out, err := cmd.Output()
		if err != nil {
			log.Error("password_cmd failed", "vpn", c.cfg.Name, "cmd", c.cfg.PasswordCmd, "err", err)
			return ""
		}
		return strings.TrimRight(string(out), "\r\n")
	}
	pw, err := keychain.GetVPNPassword(c.cfg.Name)
	if err != nil {
		if !errors.Is(err, keychain.ErrNotFound) {
			log.Warn("keychain not available, proceeding without password — set password_cmd or password_env if running in a container",
				"vpn", c.cfg.Name, "err", err)
		}
		return ""
	}
	return pw
}

func (c *Connection) buildArgs(hasPassword bool, resolveArg string) []string {
	var args []string
	if c.cfg.AuthGroup != "" {
		args = append(args, "--authgroup", c.cfg.AuthGroup)
	}
	if c.cfg.User != "" {
		args = append(args, "--user", c.cfg.User)
	}
	if hasPassword {
		args = append(args, "--passwd-on-stdin")
	}
	if c.cfg.Certificate != "" {
		args = append(args, "--certificate", c.cfg.Certificate)
	}
	if c.cfg.Key != "" {
		args = append(args, "--sslkey", c.cfg.Key)
	}
	if resolveArg != "" {
		args = append(args, "--resolve", resolveArg)
	}
	if c.cfg.ScriptWrapper != "" {
		args = append(args, "--script", c.cfg.ScriptWrapper)
	}
	args = append(args, c.cfg.ExtraArgs...)
	args = append(args, c.cfg.Server) // must stay last: procPattern anchors on it
	return args
}

// procPattern returns the pkill -f pattern matching this VPN's openconnect.
func (c *Connection) procPattern() string {
	binary := c.cfg.Binary
	if binary == "" {
		binary = "openconnect"
	}
	return procPattern(filepath.Base(binary), c.cfg.Server)
}

// procPattern builds a POSIX ERE matching exactly one VPN's openconnect command
// line: the binary as argv[0] (bare or with a path — never the "sudo ..."
// parent) and the server URL as the final argument, which buildArgs always puts
// last. Configured VPNs differ in server, so each one only reaches its own process.
func procPattern(binaryBase, server string) string {
	return "^([^ ]*/)?" + regexp.QuoteMeta(binaryBase) + "( .*)? " + regexp.QuoteMeta(server) + "$"
}

// resolveServer resolves the VPN server hostname to an IP address, retrying
// until ctx is cancelled or a 30-second deadline is reached.
// Returns a "hostname:ip" string suitable for openconnect's --resolve flag,
// or empty if the server is already an IP address.
// Progress is stored in lastError so it appears in the TUI MESSAGE column.
func (c *Connection) resolveServer(ctx context.Context) (string, error) {
	u, err := url.Parse(c.cfg.Server)
	if err != nil || u.Hostname() == "" {
		return "", nil
	}
	hostname := u.Hostname()

	// Server is already an IP — no resolution needed.
	if net.ParseIP(hostname) != nil {
		return "", nil
	}

	// Use a public DNS resolver directly — the system DNS may still point to
	// VPN-internal servers after an abrupt disconnect (vpnc-script didn't restore it).
	dnsAddr := c.cfg.DNSResolver
	if dnsAddr == "" {
		dnsAddr = "1.1.1.1:53"
	}
	resolver := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "udp", dnsAddr)
		},
	}

	c.lastError.Store("resolving " + hostname)
	log.Info("vpn: resolving server", "vpn", c.cfg.Name, "hostname", hostname, "via", dnsAddr)

	deadline := time.Now().Add(30 * time.Second)
	for {
		ips, err := resolver.LookupHost(ctx, hostname)
		if err == nil && len(ips) > 0 {
			ip := ips[0]
			log.Info("vpn: DNS resolved", "vpn", c.cfg.Name, "hostname", hostname, "ip", ip)
			c.lastError.Store("")
			return hostname + ":" + ip, nil
		}

		if time.Now().After(deadline) {
			return "", fmt.Errorf("DNS resolution timed out for %s: %w", hostname, err)
		}
		log.Warn("vpn: DNS resolution failed, retrying", "vpn", c.cfg.Name, "hostname", hostname, "err", err)
		c.lastError.Store("DNS retry: " + hostname)

		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// pollPingHost probes host:port via TCP every second while connecting, every
// 3 seconds once connected. The first success marks the VPN connected.
// After 3 consecutive failures (post-connect) it kills the subprocess.
// If connectivity is not confirmed within connect_timeout it restarts — but uses
// SIGTERM first so openconnect can send a clean disconnect to the VPN server,
// preventing stale server sessions that would block the next reconnect.
func (c *Connection) pollPingHost(ctx context.Context, cmd *exec.Cmd, died <-chan struct{}) {
	host := c.cfg.PingHost
	if !strings.Contains(host, ":") {
		host += ":443"
	}

	pattern := c.procPattern()
	timeout := time.Duration(c.cfg.ConnectTimeout) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	var ok, fail int
	var iface string        // the tunnel interface this attempt created, once seen
	var ifaceUpAt time.Time // when iface was first seen
	holding := false        // a dark session kept open for the dark-streak floor (hold_dark_session)

	c.lastError.Store(msgs.WaitingForVPNTunnel)
	log.Info("vpn: waiting for VPN tunnel", "vpn", c.cfg.Name, "host", host)

	// Poll every 1s until connected for fast detection; switch to 3s for keepalive.
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	connectTimeout := time.NewTimer(timeout)
	defer connectTimeout.Stop()

	// stopSession tears the session down. SIGTERM lets openconnect send a proper
	// goodbye to the VPN server so the server session is released immediately.
	stopSession := func() {
		c.setState(StateDisconnected)
		terminateProcs(pattern, c.cfg.Sudo)
		select {
		case <-died:
		case <-time.After(3 * time.Second):
			killProcGroup(cmd)
		}
		killOrphanedProcs(pattern, c.cfg.Sudo)
	}

	// endDark handles a session whose gateway returns no traffic (see dark.go).
	// It reports whether the session was torn down; false means it is being kept
	// open for the dark-streak floor under hold_dark_session and polling continues.
	endDark := func(reason string) bool {
		c.lastAttemptDark.Store(true)
		if hold := c.holdDarkSessionFor(); hold > 0 && !holding {
			holding = true
			c.heldCooldown.Store(true)
			log.Warn("vpn: gateway returns no traffic, holding this session open instead of starting new ones",
				"vpn", c.cfg.Name, "reason", reason, "hold", hold)
			c.lastError.Store("gateway returned no traffic (" + reason + "); holding the session for " + hold.String())
			resetTimer(connectTimeout, hold)
			return false
		}
		log.Warn("vpn: gateway returns no traffic, ending session", "vpn", c.cfg.Name, "reason", reason, "iface", iface)
		c.lastError.Store("gateway returned no traffic (" + reason + ")")
		stopSession()
		return true
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-died:
			return
		case <-connectTimeout.C:
			if c.State() == StateConnected {
				continue
			}
			// Diagnose: is the subprocess still alive?
			procAlive := true
			select {
			case <-died:
				procAlive = false
			default:
			}
			if iface == "" {
				iface = c.newTunIface()
			}

			switch {
			case !procAlive:
				log.Warn("vpn connect timeout: subprocess exited before tunnel was ready, restarting",
					"vpn", c.cfg.Name, "host", host)
			case iface != "":
				rx, tx, countersOK := ifaceCounters(iface)
				if !countersOK || rx == 0 {
					// Nothing came back through the tunnel (or we can't tell, e.g.
					// on macOS): the gateway is dark.
					reason := host + " unreachable for " + timeout.String()
					if countersOK {
						reason = fmt.Sprintf("tx %d, rx 0", tx)
					}
					if !endDark(reason) {
						continue
					}
					return
				}
				// Traffic does come back, so the gateway works and ping_host
				// itself is the problem (wrong host/port, firewalled). Not dark:
				// the normal backoff applies.
				log.Warn("vpn connect timeout: tunnel receives traffic but ping_host is unreachable (check ping_host), restarting",
					"vpn", c.cfg.Name, "host", host, "iface", iface, "rx", rx)
			default:
				log.Warn("vpn connect timeout: subprocess alive but no interface appeared, restarting",
					"vpn", c.cfg.Name, "host", host)
			}

			c.lastError.Store("connect timeout: " + host + " unreachable")
			stopSession()
			return
		case <-ticker.C:
			// Short probe while connecting: until routes exist the SYN is
			// silently dropped, and a 2 s timeout on a 1 s ticker halves the
			// probe rate exactly when fast detection matters.
			dialTimeout := 2 * time.Second
			if c.State() != StateConnected {
				dialTimeout = time.Second
			}
			conn, err := net.DialTimeout("tcp", host, dialTimeout)
			if err == nil {
				conn.Close()
				fail = 0
				ok++
				log.Debug("vpn: ping_host reachable", "vpn", c.cfg.Name, "host", host, "consecutive", ok)
				// One successful TCP handshake to an internal host already
				// proves traffic flows both ways; waiting for a second probe
				// only delayed every connect by a tick.
				if c.State() != StateConnected {
					c.setState(StateConnected)
					c.lastAttemptDark.Store(false)
					c.heldCooldown.Store(false)
					holding = false
					c.detectTunIface()
					connectTimeout.Stop()
					// Switch to a slower keepalive interval to reduce load.
					ticker.Reset(3 * time.Second)
					log.Info("vpn connected", "vpn", c.cfg.Name, "via", host)
				}
			} else {
				ok = 0
				log.Debug("vpn: ping_host unreachable", "vpn", c.cfg.Name, "host", host, "err", err, "state", c.State())
				if c.State() != StateConnected && !holding {
					// Detect a dark gateway from the tunnel's own counters instead
					// of waiting out connect_timeout (Linux only; see ifaceCounters).
					if iface == "" {
						if iface = c.newTunIface(); iface != "" {
							ifaceUpAt = time.Now()
						}
					} else if rx, tx, countersOK := ifaceCounters(iface); countersOK && gatewayDark(rx, tx, time.Since(ifaceUpAt)) {
						if endDark(fmt.Sprintf("tx %d, rx 0 after %s", tx, time.Since(ifaceUpAt).Round(time.Second))) {
							return
						}
						continue
					}
				}
				if c.State() == StateConnected {
					fail++
					log.Debug("vpn ping failed", "vpn", c.cfg.Name, "host", host, "consecutive", fail)
					if fail >= 3 {
						log.Warn("vpn connectivity lost, restarting", "vpn", c.cfg.Name)
						c.setState(StateDisconnected)
						// SIGTERM by name (not PGID) so openconnect can close the
						// stderr pipe cleanly — without this, cmd.Wait() blocks
						// indefinitely because the orphaned openconnect holds the
						// pipe write end open.
						terminateProcs(pattern, c.cfg.Sudo)
						select {
						case <-died:
						case <-time.After(3 * time.Second):
							killProcGroup(cmd)
							killOrphanedProcs(pattern, c.cfg.Sudo)
						}
						return
					}
				}
			}
		}
	}
}

// resetTimer re-arms t to fire after d, whether or not it already fired.
func resetTimer(t *time.Timer, d time.Duration) {
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
	t.Reset(d)
}

// runPostDisconnect executes each post_disconnect command after the VPN subprocess exits,
// then flushes any routes left on the tunnel interface.
// Uses a fresh background context so commands run even during shutdown.
// Errors are logged but not returned — cleanup should not block reconnect or exit.
func (c *Connection) runPostDisconnect() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, cmdStr := range c.cfg.PostDisconnect {
		log.Info("vpn post_disconnect", "vpn", c.cfg.Name, "cmd", cmdStr)
		var cmd *exec.Cmd
		if runtime.GOOS == "windows" {
			cmd = exec.CommandContext(ctx, "cmd", "/C", cmdStr)
		} else {
			cmd = exec.CommandContext(ctx, "sh", "-c", cmdStr)
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			log.Error("vpn post_disconnect failed", "vpn", c.cfg.Name, "cmd", cmdStr, "err", err, "output", strings.TrimSpace(string(out)))
		}
	}
	// On macOS, vpnc-script sets the DNS servers to VPN-internal resolvers on connect.
	// If openconnect was killed abruptly (vpnc-script disconnect phase didn't run),
	// the system DNS is left pointing at VPN servers that are now unreachable.
	// Reset all network services to DHCP-assigned DNS so the system resolver works again.
	if runtime.GOOS == "darwin" {
		c.restoreSystemDNSDarwin(ctx)
	}
	c.flushTunRoutes(ctx)
	// The interface belonged to the session that just ended; don't keep
	// showing it on a paused or reconnecting VPN.
	c.tunIface.Store("")
	c.tunIfaceIndex.Store(0)
}

// flushTunRoutes removes any routes still attached to the tunnel interface after disconnect.
// This cleans up stale routes that openconnect leaves behind when killed abruptly,
// preventing them from interfering with the next connection attempt.
func (c *Connection) flushTunRoutes(ctx context.Context) {
	iface, _ := c.tunIface.Load().(string)
	if iface == "" {
		return
	}
	// Skip only when the name now refers to a different device: when switching
	// VPNs the next openconnect can already have created its own interface
	// under the same name (tun0, or the same utunN on macOS), and flushing it
	// would wipe the new VPN's routes. A device that is gone is still flushed —
	// on macOS routes can outlive an abruptly killed utun.
	if cur, err := net.InterfaceByName(iface); err == nil {
		if want := c.tunIfaceIndex.Load(); want == 0 || int32(cur.Index) != want {
			log.Debug("vpn route flush skipped: interface name now belongs to another device", "vpn", c.cfg.Name, "iface", iface)
			return
		}
	}
	switch runtime.GOOS {
	case "darwin":
		c.flushTunRoutesDarwin(ctx, iface)
	case "linux":
		args := []string{"ip", "route", "flush", "dev", iface}
		if c.cfg.Sudo {
			args = append([]string{"sudo"}, args...)
		}
		cmd := exec.CommandContext(ctx, args[0], args[1:]...)
		if out, err := cmd.CombinedOutput(); err != nil {
			log.Warn("vpn route flush failed", "vpn", c.cfg.Name, "iface", iface, "err", err, "output", strings.TrimSpace(string(out)))
		} else {
			log.Info("vpn routes flushed", "vpn", c.cfg.Name, "iface", iface)
		}
	}
}

// flushTunRoutesDarwin deletes routes for the given interface on macOS.
// macOS route(8) has no -ifp flag; we enumerate routes via netstat and delete each
// one. Deletions run in parallel so flushing 50+ routes takes ~1s instead of ~25s.
func (c *Connection) flushTunRoutesDarwin(ctx context.Context, iface string) {
	out, err := exec.CommandContext(ctx, "netstat", "-rn", "-f", "inet").Output()
	if err != nil {
		log.Warn("vpn route flush: netstat failed", "vpn", c.cfg.Name, "err", err)
		return
	}

	var dests []string
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		// netstat -rn inet columns: Destination Gateway Flags Netif [Expire]
		if len(fields) >= 4 && fields[3] == iface {
			dests = append(dests, fields[0])
		}
	}
	if len(dests) == 0 {
		return
	}
	log.Info("vpn route flush: deleting routes", "vpn", c.cfg.Name, "iface", iface, "count", len(dests))

	type result struct {
		dest string
		ok   bool
		err  error
	}
	results := make(chan result, len(dests))
	for _, dest := range dests {
		dest := dest
		log.Debug("vpn route flush: queuing delete", "vpn", c.cfg.Name, "iface", iface, "dest", dest)
		go func() {
			args := []string{"route", "delete", dest}
			if c.cfg.Sudo {
				args = append([]string{"sudo"}, args...)
			}
			// Per-route timeout: when Wi-Fi is completely down, route delete can
			// hang indefinitely waiting for a dead interface. 5 s is enough for a
			// normal delete; if it hangs we skip it rather than block the cleanup.
			routeCtx, routeCancel := context.WithTimeout(ctx, 5*time.Second)
			defer routeCancel()
			err := exec.CommandContext(routeCtx, args[0], args[1:]...).Run()
			results <- result{dest: dest, ok: err == nil, err: err}
		}()
	}

	var deleted int
	for range dests {
		r := <-results
		if r.ok {
			deleted++
			log.Debug("vpn route flush: deleted", "vpn", c.cfg.Name, "iface", iface, "dest", r.dest)
		} else {
			log.Debug("vpn route flush: delete failed", "vpn", c.cfg.Name, "iface", iface, "dest", r.dest, "err", r.err)
		}
	}
	if deleted > 0 {
		log.Info("vpn routes flushed", "vpn", c.cfg.Name, "iface", iface, "routes", deleted)
	}
}

// runPreConnect executes each pre_connect command in order before connecting.
// On failure it logs and returns an error so the reconnect loop retries.
func (c *Connection) runPreConnect(ctx context.Context) error {
	for _, cmdStr := range c.cfg.PreConnect {
		c.lastError.Store("pre_connect: " + cmdStr)
		log.Info("vpn pre_connect", "vpn", c.cfg.Name, "cmd", cmdStr)
		var cmd *exec.Cmd
		if runtime.GOOS == "windows" {
			cmd = exec.CommandContext(ctx, "cmd", "/C", cmdStr)
		} else {
			cmd = exec.CommandContext(ctx, "sh", "-c", cmdStr)
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			log.Error("vpn pre_connect failed", "vpn", c.cfg.Name, "cmd", cmdStr, "err", err, "output", strings.TrimSpace(string(out)))
			return fmt.Errorf("pre_connect %q: %w", cmdStr, err)
		}
	}
	return nil
}

// watchUplink polls for network connectivity and kills the subprocess when the
// uplink disappears. This lets the Run() loop handle "waiting for network"
// instead of letting openconnect spin on its own internal reconnect logic.
// killedByUplink is closed after killProcGroup() so runOnce() can unblock
// even if cmd.Wait() is stuck due to an orphaned subprocess.
func (c *Connection) watchUplink(ctx context.Context, cmd *exec.Cmd, died <-chan struct{}, killedByUplink chan<- struct{}) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-died:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			up := netcheck.HasUplink()
			log.Debug("vpn: uplink check", "vpn", c.cfg.Name, "up", up)
			if !up {
				log.Info("vpn: network down, stopping subprocess", "vpn", c.cfg.Name)
				c.lastError.Store(msgs.WaitingForNetwork)
				killProcGroup(cmd)
				close(killedByUplink)
				return
			}
		}
	}
}

// restoreSystemDNSDarwin resets DNS to DHCP for every active network service.
// vpnc-script sets DNS to VPN-internal resolvers on connect; if openconnect is
// killed abruptly (vpnc-script disconnect didn't run), those resolvers remain
// and break all hostname resolution until the network interface is restarted.
// networksetup -setdnsservers <service> Empty instructs macOS to use DHCP-
// assigned DNS, which is safe to call even when DNS is already correct.
func (c *Connection) restoreSystemDNSDarwin(ctx context.Context) {
	out, err := exec.CommandContext(ctx, "networksetup", "-listallnetworkservices").Output()
	if err != nil {
		log.Warn("vpn: DNS restore: could not list network services", "vpn", c.cfg.Name, "err", err)
		return
	}
	var reset int
	for _, svc := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		svc = strings.TrimSpace(svc)
		if svc == "" || strings.HasPrefix(svc, "An asterisk") {
			continue
		}

		// Log current DNS before resetting so the debug log shows what vpnc-script set.
		if cur, err := exec.CommandContext(ctx, "networksetup", "-getdnsservers", svc).Output(); err == nil {
			current := strings.TrimSpace(string(cur))
			if current != "" && current != "There aren't any DNS Servers set on "+svc+"." {
				log.Debug("vpn: DNS restore: found custom DNS", "vpn", c.cfg.Name, "service", svc, "servers", strings.ReplaceAll(current, "\n", ","))
			} else {
				log.Debug("vpn: DNS restore: already DHCP", "vpn", c.cfg.Name, "service", svc)
			}
		}

		args := []string{"networksetup", "-setdnsservers", svc, "Empty"}
		if c.cfg.Sudo {
			args = append([]string{"sudo"}, args...)
		}
		if err := exec.CommandContext(ctx, args[0], args[1:]...).Run(); err != nil {
			log.Debug("vpn: DNS restore: reset failed", "vpn", c.cfg.Name, "service", svc, "err", err)
		} else {
			log.Debug("vpn: DNS restore: reset to DHCP", "vpn", c.cfg.Name, "service", svc)
			reset++
		}
	}
	if reset > 0 {
		log.Info("vpn: DNS restored to DHCP", "vpn", c.cfg.Name, "services", reset)
	}
}

// deleteStaleServerRouteDarwin removes any host route for the VPN server IP that
// was left over from a previous connection. openconnect adds this route to ensure
// VPN server traffic bypasses the tunnel; after an abrupt disconnect (vpnc-script
// didn't run), it may point to an unreachable gateway and cause TCP connect to the
// server to fail immediately on the next reconnect attempt.
func (c *Connection) deleteStaleServerRouteDarwin(ctx context.Context, ip string) {
	// Check if a host route exists before attempting to delete it.
	if out, err := exec.CommandContext(ctx, "netstat", "-rn", "-f", "inet").Output(); err == nil {
		found := false
		for _, line := range strings.Split(string(out), "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 2 && fields[0] == ip {
				log.Debug("vpn: found stale server host route", "vpn", c.cfg.Name, "ip", ip, "gateway", fields[1], "flags", func() string {
					if len(fields) >= 3 {
						return fields[2]
					}
					return ""
				}(), "iface", func() string {
					if len(fields) >= 4 {
						return fields[3]
					}
					return ""
				}())
				found = true
				break
			}
		}
		if !found {
			log.Debug("vpn: no stale server host route found", "vpn", c.cfg.Name, "ip", ip)
			return
		}
	}

	args := []string{"route", "delete", "-host", ip}
	if c.cfg.Sudo {
		args = append([]string{"sudo"}, args...)
	}
	routeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := exec.CommandContext(routeCtx, args[0], args[1:]...).Run(); err == nil {
		log.Info("vpn: cleared stale server host route", "vpn", c.cfg.Name, "ip", ip)
	} else {
		log.Warn("vpn: failed to clear stale server host route", "vpn", c.cfg.Name, "ip", ip, "err", err)
	}
}

// watchOutput logs openconnect output (stdout and stderr) and promotes
// connection events. done is closed when runOnce() returns; after that we only
// log, never update state.
func (c *Connection) watchOutput(r io.Reader, done <-chan struct{}) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		// After runOnce() has returned, only log — don't touch connection state.
		select {
		case <-done:
			log.Debug("vpn (orphaned): "+line, "vpn", c.cfg.Name)
			continue
		default:
		}
		switch {
		case strings.HasPrefix(line, "Configured as "):
			// "Configured as 10.4.1.126, with SSL connected and DTLS connected"
			if fields := strings.Fields(line); len(fields) >= 3 {
				c.sessionIP.Store(strings.TrimSuffix(fields[2], ","))
			}
			log.Debug("vpn: "+line, "vpn", c.cfg.Name, "server", c.cfg.Server)
		case strings.Contains(line, "Established DTLS connection"),
			strings.Contains(line, "Established TLS connection"),
			strings.Contains(line, "Connected as"):
			log.Info("vpn: "+line, "vpn", c.cfg.Name, "server", c.cfg.Server)
			// With ping_host configured these lines only prove the session is
			// up, not that internal hosts answer: right after switching between
			// two VPNs into the same network, ping_host stayed unreachable for
			// 15-35 s. ping_host stays authoritative then, so tunnels don't dial
			// (and burn auto-pause attempts) into a network that isn't routed yet.
			if c.cfg.PingHost == "" && c.State() != StateConnected {
				c.setState(StateConnected)
				c.detectTunIface()
			}
		case strings.HasPrefix(line, "Set up tun device "):
			// Extract interface name (e.g. "Set up tun device utun2" → "utun2").
			if fields := strings.Fields(line); len(fields) >= 5 {
				c.setTunIface(fields[4])
				log.Info("vpn: "+line, "vpn", c.cfg.Name)
			}
		case strings.Contains(line, "error") || strings.Contains(line, "Error") ||
			strings.Contains(line, "failed") || strings.Contains(line, "Failed"):
			log.Error("vpn: "+line, "vpn", c.cfg.Name, "server", c.cfg.Server)
			if c.State() != StateConnected {
				c.lastError.Store(line)
			}
		case strings.Contains(line, "writing to routing socket: File exists"):
			// normal during reconnect — routes already present, not an error
		default:
			log.Debug("vpn: "+line, "vpn", c.cfg.Name, "server", c.cfg.Server)
		}
	}
}
