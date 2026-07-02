package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPausedTracker_MissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")

	tr := newPausedTrackerAt(path)

	if got := tr.Tunnels(); len(got) != 0 {
		t.Errorf("Tunnels() = %v, want empty", got)
	}
	if got := tr.VPNs(); len(got) != 0 {
		t.Errorf("VPNs() = %v, want empty", got)
	}
}

func TestPausedTracker_CorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	tr := newPausedTrackerAt(path)

	if got := tr.Tunnels(); len(got) != 0 {
		t.Errorf("Tunnels() = %v, want empty fallback", got)
	}
	if got := tr.VPNs(); len(got) != 0 {
		t.Errorf("VPNs() = %v, want empty fallback", got)
	}
}

func TestPausedTracker_SetAndReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")

	tr := newPausedTrackerAt(path)
	tr.SetTunnelPaused("go-a-scb", true)
	tr.SetTunnelPaused("go-b-scb", true)
	tr.SetVPNPaused("4ig", true)

	reloaded := newPausedTrackerAt(path)

	wantTunnels := []string{"go-a-scb", "go-b-scb"}
	if got := reloaded.Tunnels(); !equalStrings(got, wantTunnels) {
		t.Errorf("Tunnels() = %v, want %v", got, wantTunnels)
	}
	wantVPNs := []string{"4ig"}
	if got := reloaded.VPNs(); !equalStrings(got, wantVPNs) {
		t.Errorf("VPNs() = %v, want %v", got, wantVPNs)
	}

	// Resuming removes the entry and persists that too.
	tr.SetTunnelPaused("go-a-scb", false)
	reloaded = newPausedTrackerAt(path)
	wantTunnels = []string{"go-b-scb"}
	if got := reloaded.Tunnels(); !equalStrings(got, wantTunnels) {
		t.Errorf("Tunnels() after resume = %v, want %v", got, wantTunnels)
	}
}

func TestPausedTracker_UnwritableDir_DoesNotPanic(t *testing.T) {
	// Pointing at a path whose parent can never be created (a file, not a
	// dir) exercises the MkdirAll failure branch in save().
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(blocker, "state.json")

	tr := newPausedTrackerAt(path)
	tr.SetTunnelPaused("go-a-scb", true) // must not panic despite save() failing

	if got := tr.Tunnels(); !equalStrings(got, []string{"go-a-scb"}) {
		t.Errorf("in-memory state should still reflect the pause: got %v", got)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
