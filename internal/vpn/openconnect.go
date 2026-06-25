package vpn

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/charmbracelet/log"

	"hopscotch/internal/keychain"
	"hopscotch/internal/netcheck"
)

// runOnce starts the openconnect subprocess and blocks until it exits or ctx is cancelled.
func (c *Connection) runOnce(ctx context.Context) error {
	binary := c.cfg.Binary
	if binary == "" {
		binary = "openconnect"
	}
	binaryBase := filepath.Base(binary)

	// Kill any orphaned instances left over from a previous abrupt shutdown
	// before launching a new one — avoids route/interface conflicts.
	killOrphanedProcs(binaryBase, c.cfg.Sudo)

	if err := c.runPreConnect(ctx); err != nil {
		return err
	}

	// Resolve password once — needed both for --passwd-on-stdin flag and stdin feed.
	pw := c.password()
	args := c.buildArgs(pw != "")

	var cmd *exec.Cmd
	if c.cfg.Sudo {
		cmd = exec.CommandContext(ctx, "sudo", append([]string{binary}, args...)...)
	} else {
		cmd = exec.CommandContext(ctx, binary, args...)
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

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}
	// nil → exec routes child stdout to /dev/null via os.File (no goroutine).
	// io.Discard would create a goroutine that blocks cmd.Wait() similarly.
	cmd.Stdout = nil

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting openconnect: %w", err)
	}
	log.Info("vpn subprocess started", "vpn", c.cfg.Name, "pid", cmd.Process.Pid)

	// stderrDone is closed when runOnce() returns so watchStderr stops
	// updating connection state after we've moved on to the reconnect cycle.
	stderrDone := make(chan struct{})
	defer close(stderrDone)

	// Watch stderr lines for status events.
	go c.watchStderr(stderr, stderrDone)

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
		// Kill entire process group (sudo + openconnect child) so the pipe
		// write-end closes on both sides and cmd.Wait() can return promptly.
		killProcGroup(cmd)
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			log.Warn("vpn subprocess did not exit after kill", "vpn", c.cfg.Name)
		}
		c.runPostDisconnect()
		return ctx.Err()
	case <-killedByUplink:
		// watchUplink() sent SIGKILL to the process group. If openconnect ran
		// in a different process group (e.g. via sudo on some systems), it may
		// not have received the kill and still holds the stderr pipe open,
		// blocking cmd.Wait(). Give it 1 s then close the pipe — this stops
		// watchStderr from updating state AND sends EPIPE/SIGPIPE to openconnect
		// which should terminate it.
		select {
		case <-done:
		case <-time.After(time.Second):
			log.Warn("vpn subprocess still alive after uplink kill; closing pipe", "vpn", c.cfg.Name)
			stderr.Close()
			killOrphanedProcs(binaryBase, c.cfg.Sudo)
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

func (c *Connection) buildArgs(hasPassword bool) []string {
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
	args = append(args, c.cfg.ExtraArgs...)
	args = append(args, c.cfg.Server)
	return args
}

// pollPingHost probes host:port via TCP every 3 seconds.
// After 2 consecutive successes it marks the VPN connected.
// After 3 consecutive failures (post-connect) it kills the subprocess.
// If connectivity is not confirmed within 30 s it restarts — but uses SIGTERM
// first so openconnect can send a clean disconnect to the VPN server, preventing
// stale server sessions that would block the next reconnect.
func (c *Connection) pollPingHost(ctx context.Context, cmd *exec.Cmd, died <-chan struct{}) {
	host := c.cfg.PingHost
	if !strings.Contains(host, ":") {
		host += ":443"
	}

	binary := c.cfg.Binary
	if binary == "" {
		binary = "openconnect"
	}
	binaryBase := filepath.Base(binary)

	var ok, fail int

	// Poll every 1s until connected for fast detection; switch to 3s for keepalive.
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	connectTimeout := time.NewTimer(30 * time.Second)
	defer connectTimeout.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-died:
			return
		case <-connectTimeout.C:
			if c.State() != StateConnected {
				log.Warn("vpn connect timeout: ping_host unreachable, restarting",
					"vpn", c.cfg.Name, "host", host)
				c.lastError.Store("connect timeout: " + host + " unreachable")
				c.setState(StateDisconnected)
				// SIGTERM lets openconnect send a proper goodbye to the VPN server
				// so the server session is released immediately — without this, the
				// stale session blocks ping_host on the next reconnect attempt too.
				terminateByName(binaryBase, c.cfg.Sudo)
				select {
				case <-died:
				case <-time.After(3 * time.Second):
					killProcGroup(cmd)
				}
				killOrphanedProcs(binaryBase, c.cfg.Sudo)
				return
			}
		case <-ticker.C:
			// watchStderr may have already set StateConnected via "Established DTLS" line.
			if c.State() == StateConnected {
				connectTimeout.Stop()
			}
			conn, err := net.DialTimeout("tcp", host, 2*time.Second)
			if err == nil {
				conn.Close()
				fail = 0
				ok++
				if ok >= 2 && c.State() != StateConnected {
					c.setState(StateConnected)
					connectTimeout.Stop()
					// Switch to a slower keepalive interval to reduce load.
					ticker.Reset(3 * time.Second)
					log.Info("vpn connected", "vpn", c.cfg.Name, "via", host)
				}
			} else {
				ok = 0
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
						terminateByName(binaryBase, c.cfg.Sudo)
						select {
						case <-died:
						case <-time.After(3 * time.Second):
							killProcGroup(cmd)
							killOrphanedProcs(binaryBase, c.cfg.Sudo)
						}
						return
					}
				}
			}
		}
	}
}

// runPostDisconnect executes each post_disconnect command after the VPN subprocess exits.
// Uses a fresh background context so commands run even during shutdown.
// Errors are logged but not returned — cleanup should not block reconnect or exit.
func (c *Connection) runPostDisconnect() {
	if len(c.cfg.PostDisconnect) == 0 {
		return
	}
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
}

// runPreConnect executes each pre_connect command in order before connecting.
// On failure it logs and returns an error so the reconnect loop retries.
func (c *Connection) runPreConnect(ctx context.Context) error {
	for _, cmdStr := range c.cfg.PreConnect {
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
			if !netcheck.HasUplink() {
				log.Info("vpn: network down, stopping subprocess", "vpn", c.cfg.Name)
				c.lastError.Store("waiting for network")
				killProcGroup(cmd)
				close(killedByUplink)
				return
			}
		}
	}
}

// watchStderr logs openconnect output and promotes connection events.
// done is closed when runOnce() returns; after that we only log, never update state.
func (c *Connection) watchStderr(r io.Reader, done <-chan struct{}) {
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
		case strings.Contains(line, "Established DTLS connection"),
			strings.Contains(line, "Established TLS connection"),
			strings.Contains(line, "Connected as"):
			log.Info("vpn: "+line, "vpn", c.cfg.Name, "server", c.cfg.Server)
			if c.State() != StateConnected {
				c.setState(StateConnected)
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
