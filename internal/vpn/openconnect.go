package vpn

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/log"

	"hopscotch/internal/keychain"
)

// runOnce starts the openconnect subprocess and blocks until it exits or ctx is cancelled.
func (c *Connection) runOnce(ctx context.Context) error {
	args := c.buildArgs()

	var cmd *exec.Cmd
	if c.cfg.Sudo {
		cmd = exec.CommandContext(ctx, "sudo", append([]string{"openconnect"}, args...)...)
	} else {
		cmd = exec.CommandContext(ctx, "openconnect", args...)
	}

	if pw := c.password(); pw != "" {
		cmd.Stdin = strings.NewReader(pw + "\n")
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}
	cmd.Stdout = io.Discard

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting openconnect: %w", err)
	}
	log.Info("vpn subprocess started", "vpn", c.cfg.Name, "pid", cmd.Process.Pid)

	// Watch stderr lines for status events.
	go c.watchStderr(stderr)

	// Wait for subprocess in a goroutine so we can also watch ctx.
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	if c.cfg.PingHost != "" {
		// Poll ping_host to detect when VPN is up and when it drops.
		go c.pollPingHost(ctx, cmd, done)
	} else {
		// No ping host: assume connected after a short startup delay.
		go func() {
			select {
			case <-time.After(8 * time.Second):
				if c.State() == StateConnecting {
					c.setState(StateConnected)
					log.Info("vpn assumed connected (no ping_host configured)", "vpn", c.cfg.Name)
				}
			case <-ctx.Done():
			}
		}()
	}

	select {
	case err := <-done:
		if err != nil && ctx.Err() == nil {
			log.Warn("vpn subprocess exited", "vpn", c.cfg.Name, "err", err)
		}
		return err
	case <-ctx.Done():
		cmd.Process.Kill()
		<-done
		return ctx.Err()
	}
}

// password returns the VPN password from environment variable or OS keychain.
// Priority: password_env > keychain. Returns empty string if neither is set.
func (c *Connection) password() string {
	if c.cfg.PasswordEnv != "" {
		return os.Getenv(c.cfg.PasswordEnv)
	}
	pw, err := keychain.GetVPNPassword(c.cfg.Name)
	if err != nil {
		return ""
	}
	return pw
}

func (c *Connection) buildArgs() []string {
	args := []string{"--non-inter"}
	if c.cfg.AuthGroup != "" {
		args = append(args, "--authgroup", c.cfg.AuthGroup)
	}
	if c.cfg.User != "" {
		args = append(args, "--user", c.cfg.User)
	}
	if c.cfg.PasswordEnv != "" {
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
func (c *Connection) pollPingHost(ctx context.Context, cmd *exec.Cmd, done <-chan error) {
	host := c.cfg.PingHost
	if !strings.Contains(host, ":") {
		host += ":443"
	}

	var ok, fail int
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-ticker.C:
			conn, err := net.DialTimeout("tcp", host, 2*time.Second)
			if err == nil {
				conn.Close()
				fail = 0
				ok++
				if ok >= 2 && c.State() != StateConnected {
					c.setState(StateConnected)
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
						cmd.Process.Kill()
						return
					}
				}
			}
		}
	}
}

// watchStderr logs openconnect output and promotes connection events.
func (c *Connection) watchStderr(r io.Reader) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		switch {
		case strings.Contains(line, "Established DTLS connection"),
			strings.Contains(line, "Established TLS connection"),
			strings.Contains(line, "Connected as"):
			log.Info("vpn: "+line, "vpn", c.cfg.Name)
			if c.State() != StateConnected {
				c.setState(StateConnected)
			}
		case strings.Contains(line, "error") || strings.Contains(line, "Error") ||
			strings.Contains(line, "failed") || strings.Contains(line, "Failed"):
			log.Error("vpn: "+line, "vpn", c.cfg.Name)
		default:
			log.Debug("vpn: "+line, "vpn", c.cfg.Name)
		}
	}
}
