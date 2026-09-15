package cmd

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

const trustTestTimeout = 5 * time.Second

func newTestSigner(t *testing.T, kind string) ssh.Signer {
	t.Helper()
	var priv crypto.Signer
	var err error
	switch kind {
	case "ed25519":
		_, priv, err = ed25519.GenerateKey(rand.Reader)
	case "ecdsa":
		priv, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	case "rsa":
		priv, err = rsa.GenerateKey(rand.Reader, 2048)
	default:
		t.Fatalf("unknown key kind %q", kind)
	}
	if err != nil {
		t.Fatalf("%s keygen: %v", kind, err)
	}
	s, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("ssh.NewSignerFromKey(%s): %v", kind, err)
	}
	return s
}

// startTrustTestServer runs an in-process SSH server presenting the given host
// keys and returns its host and port.
func startTrustTestServer(t *testing.T, signers ...ssh.Signer) (string, int) {
	t.Helper()
	serverCfg := &ssh.ServerConfig{NoClientAuth: true}
	for _, s := range signers {
		serverCfg.AddHostKey(s)
	}

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
				defer c.Close()
				sconn, chans, reqs, err := ssh.NewServerConn(c, serverCfg)
				if err != nil {
					return // trust aborts every handshake after the host key
				}
				defer sconn.Close()
				go ssh.DiscardRequests(reqs)
				for newCh := range chans {
					_ = newCh.Reject(ssh.UnknownChannelType, "no channels")
				}
			}(conn)
		}
	}()

	addr := ln.Addr().(*net.TCPAddr)
	return addr.IP.String(), addr.Port
}

func keyTypes(keys []ssh.PublicKey) []string {
	types := make([]string, 0, len(keys))
	for _, k := range keys {
		types = append(types, k.Type())
	}
	sort.Strings(types)
	return types
}

func sortedTypes(types ...string) []string {
	out := append([]string(nil), types...)
	sort.Strings(out)
	return out
}

var (
	serverSignersOnce sync.Once
	serverSigners     map[string]ssh.Signer
)

// sharedSigners generates the three-key host identity once; RSA keygen is slow.
func sharedSigners(t *testing.T) map[string]ssh.Signer {
	serverSignersOnce.Do(func() {
		serverSigners = map[string]ssh.Signer{
			"ed25519": newTestSigner(t, "ed25519"),
			"ecdsa":   newTestSigner(t, "ecdsa"),
			"rsa":     newTestSigner(t, "rsa"),
		}
	})
	return serverSigners
}

func TestFetchHostKeys(t *testing.T) {
	all := sharedSigners(t)

	tests := []struct {
		name    string
		signers []ssh.Signer
		want    []string
	}{
		{
			name:    "all three types",
			signers: []ssh.Signer{all["rsa"], all["ecdsa"], all["ed25519"]},
			want:    sortedTypes(ssh.KeyAlgoED25519, ssh.KeyAlgoECDSA256, ssh.KeyAlgoRSA),
		},
		{
			name:    "ed25519 only",
			signers: []ssh.Signer{all["ed25519"]},
			want:    []string{ssh.KeyAlgoED25519},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			host, port := startTrustTestServer(t, tc.signers...)
			keys, err := fetchHostKeys(net.JoinHostPort(host, strconv.Itoa(port)), trustTestTimeout)
			if err != nil {
				t.Fatalf("fetchHostKeys: %v", err)
			}
			if got := keyTypes(keys); strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("types = %v, want %v", got, tc.want)
			}
			// Every fetched key must be the server's real key, not just the right type.
			for _, k := range keys {
				found := false
				for _, s := range tc.signers {
					if bytes.Equal(s.PublicKey().Marshal(), k.Marshal()) {
						found = true
					}
				}
				if !found {
					t.Errorf("fetched %s key does not belong to the server", k.Type())
				}
			}
		})
	}
}

func TestFetchHostKeysUnreachable(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	if _, err := fetchHostKeys(addr, trustTestTimeout); err == nil {
		t.Fatal("expected an error for an unreachable host")
	}
}

func writeTestKnownHosts(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ssh", "known_hosts")
	if len(lines) == 0 {
		return path // deliberately absent: trust must create it
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func readFileOrEmpty(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(data)
}

func TestTrustHost(t *testing.T) {
	all := sharedSigners(t)
	host, port := startTrustTestServer(t, all["ed25519"], all["ecdsa"], all["rsa"])
	entry := "[" + host + "]:" + strconv.Itoa(port)
	allTypes := sortedTypes(ssh.KeyAlgoED25519, ssh.KeyAlgoECDSA256, ssh.KeyAlgoRSA)
	otherEd25519 := newTestSigner(t, "ed25519").PublicKey()

	tests := []struct {
		name        string
		lines       func() []string
		decline     bool
		wantErr     bool
		wantAdded   []string // key types expected to be appended
		wantPrompt  bool
		wantOutput  []string
		wantNoWrite bool
	}{
		{
			name:       "empty known_hosts adds every type",
			lines:      func() []string { return nil },
			wantAdded:  allTypes,
			wantPrompt: true,
			wantOutput: []string{"Add 3 key(s) to known_hosts?"},
		},
		{
			name: "pinned ecdsa leaves ed25519 and rsa as candidates",
			lines: func() []string {
				return []string{knownhosts.Line([]string{entry}, all["ecdsa"].PublicKey())}
			},
			wantAdded:  sortedTypes(ssh.KeyAlgoED25519, ssh.KeyAlgoRSA),
			wantPrompt: true,
			wantOutput: []string{"Pinned:      " + ssh.KeyAlgoECDSA256, "Add 2 key(s)"},
		},
		{
			name: "hashed ecdsa entry is recognised",
			lines: func() []string {
				return []string{knownhosts.Line([]string{knownhosts.HashHostname(entry)}, all["ecdsa"].PublicKey())}
			},
			wantAdded:  sortedTypes(ssh.KeyAlgoED25519, ssh.KeyAlgoRSA),
			wantPrompt: true,
		},
		{
			name: "different ed25519 key is a mismatch and nothing is written",
			lines: func() []string {
				return []string{
					knownhosts.Line([]string{entry}, all["ecdsa"].PublicKey()),
					knownhosts.Line([]string{entry}, otherEd25519),
				}
			},
			wantErr:     true,
			wantNoWrite: true,
			wantOutput:  []string{"HOST KEY MISMATCH", "known_hosts:2", fingerprintSHA256(otherEd25519)},
		},
		{
			name: "host that is a substring of a pinned host is not trusted",
			lines: func() []string {
				other := "[" + host + "0]:" + strconv.Itoa(port) // 127.0.0.10
				return []string{
					"# " + entry + " was decommissioned",
					knownhosts.Line([]string{other}, all["ed25519"].PublicKey()),
					knownhosts.Line([]string{other}, all["ecdsa"].PublicKey()),
					knownhosts.Line([]string{other}, all["rsa"].PublicKey()),
				}
			},
			wantAdded:  allTypes,
			wantPrompt: true,
		},
		{
			name: "fully trusted host is left alone",
			lines: func() []string {
				return []string{
					knownhosts.Line([]string{entry}, all["ed25519"].PublicKey()),
					knownhosts.Line([]string{entry}, all["ecdsa"].PublicKey()),
					knownhosts.Line([]string{entry}, all["rsa"].PublicKey()),
				}
			},
			wantNoWrite: true,
			wantOutput:  []string{"already fully trusted"},
		},
		{
			name:        "declined prompt writes nothing",
			lines:       func() []string { return nil },
			decline:     true,
			wantPrompt:  true,
			wantNoWrite: true,
			wantOutput:  []string{"Skipped."},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := writeTestKnownHosts(t, tc.lines()...)
			before := readFileOrEmpty(t, path)

			var out bytes.Buffer
			prompted := false
			confirm := func(prompt string) bool {
				prompted = true
				out.WriteString(prompt) // trustOne prints it to stdout
				return !tc.decline
			}

			err := trustHost(&out, host, port, "test-tunnel", path, trustTestTimeout, confirm)
			if tc.wantErr != (err != nil) {
				t.Fatalf("err = %v, wantErr %v\noutput:\n%s", err, tc.wantErr, out.String())
			}
			if prompted != tc.wantPrompt {
				t.Errorf("prompted = %v, want %v", prompted, tc.wantPrompt)
			}
			for _, want := range tc.wantOutput {
				if !strings.Contains(out.String(), want) {
					t.Errorf("output missing %q:\n%s", want, out.String())
				}
			}
			if tc.wantErr && strings.Contains(out.String(), "hopscotch trust") {
				t.Errorf("mismatch output steers towards trusting the key:\n%s", out.String())
			}

			after := readFileOrEmpty(t, path)
			if tc.wantNoWrite {
				if after != before {
					t.Fatalf("known_hosts changed:\nbefore:\n%s\nafter:\n%s", before, after)
				}
				return
			}

			if !strings.HasPrefix(after, before) {
				t.Fatalf("existing known_hosts content was not preserved")
			}
			added, err := parseKnownHostsLines(strings.TrimPrefix(after, before))
			if err != nil {
				t.Fatal(err)
			}
			if got := keyTypes(added); strings.Join(got, ",") != strings.Join(tc.wantAdded, ",") {
				t.Fatalf("added types = %v, want %v", got, tc.wantAdded)
			}

			// Round-trip: afterwards every server key verifies for host:port.
			cb, err := knownhosts.New(path)
			if err != nil {
				t.Fatalf("knownhosts.New on result: %v", err)
			}
			remote := &net.TCPAddr{IP: net.ParseIP(host), Port: port}
			addr := net.JoinHostPort(host, strconv.Itoa(port))
			for kind, s := range all {
				if err := cb(addr, remote, s.PublicKey()); err != nil {
					t.Errorf("%s key does not verify after trust: %v", kind, err)
				}
			}
		})
	}
}

// parseKnownHostsLines returns the keys on the given known_hosts lines.
func parseKnownHostsLines(s string) ([]ssh.PublicKey, error) {
	var keys []ssh.PublicKey
	for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
		if line == "" {
			continue
		}
		_, _, key, _, _, err := ssh.ParseKnownHosts([]byte(line))
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, nil
}

// Port 22 uses bare host entries, where the old strings.Contains check matched
// 127.0.0.1 inside 127.0.0.10.
func TestClassifyHostKeysPort22Substring(t *testing.T) {
	key := newTestSigner(t, "ed25519").PublicKey()
	path := writeTestKnownHosts(t, knownhosts.Line([]string{"127.0.0.10"}, key))

	plan, err := classifyHostKeys(path, "127.0.0.1", 22, []ssh.PublicKey{key})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.missing) != 1 || len(plan.pinned) != 0 || len(plan.mismatches) != 0 {
		t.Fatalf("plan = %+v, want the key as a missing candidate", plan)
	}

	plan, err = classifyHostKeys(path, "127.0.0.10", 22, []ssh.PublicKey{key})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.pinned) != 1 {
		t.Fatalf("plan = %+v, want the key pinned for 127.0.0.10", plan)
	}
}

func TestAppendKnownHost(t *testing.T) {
	ed := newTestSigner(t, "ed25519").PublicKey()
	ec := newTestSigner(t, "ecdsa").PublicKey()

	tests := []struct {
		name     string
		port     int
		existing string // written without a trailing newline when non-empty
		wantHost string
	}{
		{name: "port 22 uses bare host", port: 22, wantHost: "db.internal"},
		{name: "other port uses bracket form", port: 2222, wantHost: "[db.internal]:2222"},
		{name: "missing trailing newline is repaired", port: 22, existing: "# comment", wantHost: "db.internal"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "ssh")
			path := filepath.Join(dir, "known_hosts")
			if tc.existing != "" {
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(tc.existing), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			if err := appendKnownHost("db.internal", tc.port, []ssh.PublicKey{ed, ec}, path); err != nil {
				t.Fatalf("appendKnownHost: %v", err)
			}

			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
			if tc.existing != "" {
				if lines[0] != tc.existing {
					t.Fatalf("existing line altered: %q", lines[0])
				}
				lines = lines[1:]
			}
			if len(lines) != 2 {
				t.Fatalf("got %d appended lines, want 2:\n%s", len(lines), data)
			}
			for _, l := range lines {
				if !strings.HasPrefix(l, tc.wantHost+" ") {
					t.Errorf("line %q does not start with %q", l, tc.wantHost)
				}
			}

			cb, err := knownhosts.New(path)
			if err != nil {
				t.Fatalf("knownhosts.New: %v", err)
			}
			addr := net.JoinHostPort("db.internal", strconv.Itoa(tc.port))
			remote := &net.TCPAddr{IP: net.IPv4zero, Port: tc.port}
			for _, k := range []ssh.PublicKey{ed, ec} {
				if err := cb(addr, remote, k); err != nil {
					t.Errorf("%s does not verify for %s: %v", k.Type(), addr, err)
				}
			}
			if err := cb("db.internal:2200", remote, ed); err == nil && tc.port != 2200 {
				t.Errorf("key unexpectedly verifies for a different port")
			}

			if runtime.GOOS != "windows" && tc.existing == "" {
				for p, want := range map[string]os.FileMode{path: 0o600, dir: 0o700} {
					info, err := os.Stat(p)
					if err != nil {
						t.Fatal(err)
					}
					if got := info.Mode().Perm(); got != want {
						t.Errorf("%s mode = %v, want %v", p, got, want)
					}
				}
			}
		})
	}
}
