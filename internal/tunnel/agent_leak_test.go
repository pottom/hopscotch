package tunnel

import (
	"context"
	"net"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

// TestDialClosesAgentConnection is a regression test for a leak where every
// dial() attempt opened a new connection to SSH_AUTH_SOCK (e.g. gpg-agent
// emulating ssh-agent) via agentAuthMethod() and never closed it. On a tunnel
// that keeps failing to connect (e.g. a YubiKey-backed identity with the key
// not inserted), every reconnect attempt leaked one more open connection,
// which can pile up enough to make the agent unresponsive.
func TestDialClosesAgentConnection(t *testing.T) {
	ln, err := net.Listen("unix", t.TempDir()+"/agent.sock")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	var closed atomic.Int32
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				// Block reading; dial() never sends any agent protocol bytes
				// here since the TCP dial below fails before SSH auth even
				// starts — so the only way this Read unblocks is c being
				// closed by the client, which is exactly what we're testing.
				buf := make([]byte, 1)
				if _, err := c.Read(buf); err != nil {
					closed.Add(1)
				}
				c.Close()
			}(conn)
		}
	}()

	t.Setenv("SSH_AUTH_SOCK", ln.Addr().String())

	// A listener we immediately close gives a port that refuses connections
	// right away, so dial() fails fast instead of waiting out a timeout.
	refused, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := refused.Addr().String()
	refused.Close()
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}

	cfg := testTunnelCfg("agent-leak", 1093)
	cfg.Host = host
	cfg.Port = port
	cfg.DialTimeout = 5
	tun := New(cfg)

	const attempts = 3
	for range attempts {
		if err := tun.dial(context.Background()); err == nil {
			t.Fatal("dial unexpectedly succeeded against a closed port")
		}
	}

	deadline := time.After(2 * time.Second)
	for closed.Load() < attempts {
		select {
		case <-deadline:
			t.Fatalf("only %d of %d agent connections were closed after failed dial attempts (leak not fixed)", closed.Load(), attempts)
		case <-time.After(10 * time.Millisecond):
		}
	}
}
