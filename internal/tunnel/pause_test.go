package tunnel

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// waitForStatus polls until the tunnel reaches want or the timeout elapses.
func waitForStatus(t *testing.T, tun *Tunnel, want Status, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		if got := tun.Stats().Status; got == want {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("status did not reach %v within %v (last status %v)", want, timeout, tun.Stats().Status)
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// writeTestIdentityFile generates a throwaway ed25519 key pair and writes the
// private key to a temp file, so authMethods() succeeds deterministically
// without depending on the test environment's ssh-agent or ~/.ssh keys.
func writeTestIdentityFile(t *testing.T) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "test")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// startTestSSHServer starts a minimal SSH server that accepts any client
// (NoClientAuth) and rejects every channel-open request. Good enough to let
// a real *ssh.Client complete its handshake for tests that don't need actual
// tunneled traffic.
func startTestSSHServer(t *testing.T) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}

	serverCfg := &ssh.ServerConfig{NoClientAuth: true}
	serverCfg.AddHostKey(signer)

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
				sconn, chans, reqs, err := ssh.NewServerConn(c, serverCfg)
				if err != nil {
					return
				}
				defer sconn.Close()
				go ssh.DiscardRequests(reqs)
				for newCh := range chans {
					_ = newCh.Reject(ssh.UnknownChannelType, "no channels")
				}
			}(conn)
		}
	}()

	return ln.Addr().String()
}

// TestTunnelPauseBeforeRunSkipsConnect verifies that pausing a tunnel before
// Run() even starts puts it straight into StatusPaused without attempting a
// connect.
func TestTunnelPauseBeforeRunSkipsConnect(t *testing.T) {
	cfg := testTunnelCfg("pause-before-run", 1089)
	tun := New(cfg)
	tun.Pause()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		tun.Run(ctx)
		close(done)
	}()
	defer func() {
		cancel()
		<-done
	}()

	waitForStatus(t, tun, StatusPaused, 2*time.Second)
}

// TestTunnelPauseDuringHandshakeAbortsImmediately is the "no compromise"
// regression test: Pause() must abort an in-flight SSH handshake right away,
// not wait for it to time out or complete.
func TestTunnelPauseDuringHandshakeAbortsImmediately(t *testing.T) {
	t.Setenv("HOPSCOTCH_INSECURE_SKIP_KNOWN_HOSTS", "true")

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		// Never write the SSH version banner — ssh.NewClientConn blocks until
		// the connection is closed (by the tunnel on Pause, or by us on cleanup).
		accepted <- conn
	}()

	host, portStr, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}

	cfg := testTunnelCfg("pause-handshake", 1090)
	cfg.Host = host
	cfg.Port = port
	cfg.IdentityFile = writeTestIdentityFile(t)
	cfg.DialTimeout = 30 // deliberately long: pause must not depend on this expiring

	tun := New(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		tun.Run(ctx)
		close(done)
	}()
	defer func() {
		cancel()
		<-done
	}()

	select {
	case conn := <-accepted:
		defer conn.Close()
	case <-time.After(2 * time.Second):
		t.Fatal("server never accepted a connection")
	}

	// Give the handshake goroutine a moment to actually block reading the
	// (never sent) server version banner.
	time.Sleep(200 * time.Millisecond)

	start := time.Now()
	tun.Pause()

	waitForStatus(t, tun, StatusPaused, 2*time.Second)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("pause took %v to abort the blocked handshake, want near-immediate", elapsed)
	}
}

// TestTunnelPauseWhileConnectedClosesClient verifies Pause() interrupts an
// active keepalive loop immediately and closes the SSH client.
func TestTunnelPauseWhileConnectedClosesClient(t *testing.T) {
	addr := startTestSSHServer(t)

	rawConn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	sshConn, chans, reqs, err := ssh.NewClientConn(rawConn, addr, &ssh.ClientConfig{
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), //nolint:gosec
	})
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	client := ssh.NewClient(sshConn, chans, reqs)

	cfg := testTunnelCfg("pause-connected", 1091)
	cfg.KeepaliveInterval = 1
	cfg.KeepaliveMaxFails = 1000
	cfg.DialTimeout = 5
	tun := New(cfg)
	tun.client = client
	tun.setStatus(StatusConnected)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	kaDone := make(chan struct{})
	go func() {
		tun.keepalive(ctx)
		close(kaDone)
	}()

	time.Sleep(100 * time.Millisecond)

	start := time.Now()
	tun.Pause()

	select {
	case <-kaDone:
	case <-time.After(2 * time.Second):
		t.Fatal("keepalive did not return after Pause()")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("pause took %v to interrupt keepalive, want near-immediate", elapsed)
	}

	if _, _, err := client.SendRequest("ping", true, nil); err == nil {
		t.Error("expected ssh client to be closed after Pause()")
	}
}

// TestTunnelResumeAfterPause verifies Resume() clears the paused state and
// lets the reconnect loop proceed again.
func TestTunnelResumeAfterPause(t *testing.T) {
	cfg := testTunnelCfg("resume", 1092)
	tun := New(cfg)
	tun.Pause()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		tun.Run(ctx)
		close(done)
	}()
	defer func() {
		cancel()
		<-done
	}()

	waitForStatus(t, tun, StatusPaused, 2*time.Second)

	tun.Resume()

	deadline := time.After(2 * time.Second)
	for tun.Stats().Status == StatusPaused {
		select {
		case <-deadline:
			t.Fatal("tunnel stayed in StatusPaused after Resume()")
		case <-time.After(5 * time.Millisecond):
		}
	}
}
