package tunnel

import (
	"context"
	"crypto/ed25519"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// startAuthFailSSHServer stands up a minimal SSH server on 127.0.0.1 whose
// clients fail the handshake (unknown host key) — enough to make dial()'s error
// contain "handshake failed", so isAuthError() is true and the reconnect loop
// arms watchAgentKeys() on every backoff cycle.
func startAuthFailSSHServer(t *testing.T) (host string, port int) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	srvCfg := &ssh.ServerConfig{NoClientAuth: true}
	srvCfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				// Complete KEX so the client reaches its host-key check, which
				// fails because our ephemeral key isn't in known_hosts.
				if sconn, chans, reqs, err := ssh.NewServerConn(c, srvCfg); err == nil {
					go ssh.DiscardRequests(reqs)
					for nc := range chans {
						nc.Reject(ssh.Prohibited, "no")
					}
					sconn.Close()
				}
				c.Close()
			}(conn)
		}
	}()
	addr := ln.Addr().(*net.TCPAddr)
	return "127.0.0.1", addr.Port
}

// countWatcherGoroutines returns how many watchAgentKeys polling goroutines are
// currently alive, by scanning the full goroutine stack dump.
func countWatcherGoroutines() int {
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)
	return strings.Count(string(buf[:n]), "tunnel.watchAgentKeys.func1")
}

// TestReconnectDoesNotLeakAgentWatchers is a regression test for a leak where
// each auth-failure backoff cycle armed watchAgentKeys() on the tunnel-lifetime
// ctx and never cancelled it, so one 2s-polling goroutine piled up per cycle —
// enough SSH_AUTH_SOCK (gpg-agent) connections to make the agent unresponsive.
// With ReconnectDelay=1s a leaking loop grows the count 1,2,3,...; the fix keeps
// at most one watcher alive at a time.
func TestReconnectDoesNotLeakAgentWatchers(t *testing.T) {
	host, port := startAuthFailSSHServer(t)

	// Fake SSH agent so SSH_AUTH_SOCK is set and watchAgentKeys has something to
	// poll. It just accepts and closes; agent.List() failing is fine.
	// Keep the socket path short — macOS caps unix sun_path at ~104 bytes, and
	// t.TempDir() paths embed the (long) test name.
	sockDir, err := os.MkdirTemp("", "hs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(sockDir) })
	agLn, err := net.Listen("unix", filepath.Join(sockDir, "a.sock"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { agLn.Close() })
	go func() {
		for {
			c, err := agLn.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	t.Setenv("SSH_AUTH_SOCK", agLn.Addr().String())
	// Force host-key verification ON (default) so the handshake fails.
	t.Setenv("HOPSCOTCH_INSECURE_SKIP_KNOWN_HOSTS", "false")

	cfg := testTunnelCfg("watcher-leak", 1234)
	cfg.Host = host
	cfg.Port = port
	cfg.DialTimeout = 3
	cfg.ReconnectDelay = 1
	cfg.ReconnectMaxDelay = 1

	tun := New(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { tun.Run(ctx); close(done) }()
	defer func() {
		cancel()
		<-done
	}()

	// Sample across several backoff cycles. A brief overlap of 2 is tolerable
	// while a cancelled watcher winds down; anything higher means they leak.
	const maxAllowed = 2
	peak := 0
	for range 6 {
		time.Sleep(1 * time.Second)
		if n := countWatcherGoroutines(); n > peak {
			peak = n
		}
	}
	if peak > maxAllowed {
		t.Fatalf("agent watcher goroutines peaked at %d over the run (want <= %d); "+
			"watchAgentKeys is leaking one goroutine per auth-failure backoff cycle", peak, maxAllowed)
	}
}
