package tunnel

import (
	"testing"
	"time"

	"hopscotch/internal/config"
)

func TestBackoff(t *testing.T) {
	b := newBackoff(5*time.Second, 30*time.Second)

	got := []time.Duration{b.next(), b.next(), b.next(), b.next(), b.next()}
	want := []time.Duration{5, 10, 20, 30, 30}

	for i, d := range got {
		if d != want[i]*time.Second {
			t.Errorf("step %d: got %v, want %v", i, d, want[i]*time.Second)
		}
	}
}

func TestBackoffReset(t *testing.T) {
	b := newBackoff(5*time.Second, 30*time.Second)
	b.next()
	b.next()
	b.reset(5 * time.Second)

	if got := b.next(); got != 5*time.Second {
		t.Errorf("after reset: got %v, want 5s", got)
	}
}

func TestTunnelStatsInitial(t *testing.T) {
	cfg := testTunnelCfg("mytunnel", 1080)
	tun := New(cfg)

	st := tun.Stats()
	if st.Status != StatusConnecting {
		t.Errorf("initial status = %v, want connecting", st.Status)
	}
	if st.LocalPort != 1080 {
		t.Errorf("local port = %d, want 1080", st.LocalPort)
	}
	if st.ReconnectCount != 0 {
		t.Errorf("reconnect count = %d, want 0", st.ReconnectCount)
	}
}

func TestStatusString(t *testing.T) {
	tests := []struct {
		s    Status
		want string
	}{
		{StatusConnected, "connected"},
		{StatusConnecting, "connecting"},
		{StatusDisconnected, "disconnected"},
	}
	for _, tc := range tests {
		if got := tc.s.String(); got != tc.want {
			t.Errorf("Status(%d).String() = %q, want %q", tc.s, got, tc.want)
		}
	}
}

func testTunnelCfg(name string, localPort int) config.TunnelConfig {
	return config.TunnelConfig{
		Name:              name,
		Host:              "jump.example.com",
		Port:              22,
		User:              "testuser",
		LocalPort:         localPort,
		KeepaliveInterval: 30,
		KeepaliveMaxFails: 3,
		ReconnectDelay:    5,
	}
}
