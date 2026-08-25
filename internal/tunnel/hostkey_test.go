package tunnel

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/pottom/hopscotch/internal/config"
)

const testHost = "10.215.11.2"

func newEd25519Key(t *testing.T) ssh.PublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("ed25519 keygen: %v", err)
	}
	k, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("ssh.NewPublicKey: %v", err)
	}
	return k
}

func newRSAKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa keygen: %v", err)
	}
	k, err := ssh.NewPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatalf("ssh.NewPublicKey: %v", err)
	}
	return k
}

// writeKnownHosts writes a known_hosts pinning exactly the given keys for
// testHost and returns a tunnel configured to use it.
func writeKnownHosts(t *testing.T, keys ...ssh.PublicKey) *Tunnel {
	t.Helper()
	path := filepath.Join(t.TempDir(), "known_hosts")
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(knownhosts.Line([]string{testHost}, k) + "\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatalf("writing known_hosts: %v", err)
	}
	return New(config.TunnelConfig{
		Name:           "go-a-preprod-jump",
		Host:           testHost,
		Port:           22,
		KnownHostsFile: path,
	})
}

// A host pinned only as ed25519 must constrain negotiation to ed25519.
// Leaving HostKeyAlgorithms empty is what let the server answer with ssh-rsa
// and produced a bogus "key mismatch".
func TestHostKeyAlgorithmsFollowKnownHosts(t *testing.T) {
	tun := writeKnownHosts(t, newEd25519Key(t))

	_, algos, err := tun.hostKeyCallback()
	if err != nil {
		t.Fatalf("hostKeyCallback: %v", err)
	}
	if len(algos) != 1 || algos[0] != ssh.KeyAlgoED25519 {
		t.Fatalf("algorithms = %v, want [%s]", algos, ssh.KeyAlgoED25519)
	}
}

// An unknown host pins nothing, so negotiation must stay unconstrained rather
// than be narrowed to an empty set.
func TestHostKeyAlgorithmsEmptyForUnknownHost(t *testing.T) {
	tun := writeKnownHosts(t)

	_, algos, err := tun.hostKeyCallback()
	if err != nil {
		t.Fatalf("hostKeyCallback: %v", err)
	}
	if len(algos) != 0 {
		t.Fatalf("algorithms = %v, want none", algos)
	}
}

func TestHostKeyErrors(t *testing.T) {
	pinned := newEd25519Key(t)
	remote := &net.TCPAddr{IP: net.ParseIP(testHost), Port: 22}
	addr := testHost + ":22"

	t.Run("pinned key verifies", func(t *testing.T) {
		tun := writeKnownHosts(t, pinned)
		cb, _, err := tun.hostKeyCallback()
		if err != nil {
			t.Fatalf("hostKeyCallback: %v", err)
		}
		if err := cb(addr, remote, pinned); err != nil {
			t.Fatalf("pinned key rejected: %v", err)
		}
	})

	// The regression: an algorithm we simply have no entry for must not be
	// reported as a mismatch, and must point at the command that fixes it.
	t.Run("unpinned algorithm reported as missing entry", func(t *testing.T) {
		tun := writeKnownHosts(t, pinned)
		cb, _, err := tun.hostKeyCallback()
		if err != nil {
			t.Fatalf("hostKeyCallback: %v", err)
		}

		err = cb(addr, remote, newRSAKey(t))
		if err == nil {
			t.Fatal("unpinned algorithm accepted")
		}
		msg := err.Error()
		if strings.Contains(msg, "host key mismatch") {
			t.Errorf("missing entry reported as a mismatch: %s", msg)
		}
		for _, want := range []string{ssh.KeyAlgoRSA, "hopscotch trust go-a-preprod-jump"} {
			if !strings.Contains(msg, want) {
				t.Errorf("error does not mention %q: %s", want, msg)
			}
		}
	})

	// The genuine security case must still be called out as such.
	t.Run("same algorithm different key flagged", func(t *testing.T) {
		tun := writeKnownHosts(t, pinned)
		cb, _, err := tun.hostKeyCallback()
		if err != nil {
			t.Fatalf("hostKeyCallback: %v", err)
		}

		err = cb(addr, remote, newEd25519Key(t))
		if err == nil {
			t.Fatal("changed host key accepted")
		}
		msg := err.Error()
		if !strings.Contains(msg, "host key mismatch") {
			t.Errorf("changed host key not reported as a mismatch: %s", msg)
		}
		if strings.Contains(msg, "hopscotch trust") {
			t.Errorf("possible MITM steered towards trusting the key: %s", msg)
		}
	})
}
