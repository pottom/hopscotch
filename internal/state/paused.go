package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/charmbracelet/log"
)

// PersistedState is hopscotch's on-disk app-state file, stored as state.json
// next to config.yaml. Only "paused" is populated today, but it's nested
// under its own key so future state can be added as sibling fields without a
// schema change.
type PersistedState struct {
	Paused PausedState `json:"paused"`
}

// PausedState is the set of tunnel/VPN names currently paused, persisted so
// pauses survive an app restart.
type PausedState struct {
	Tunnels []string `json:"tunnels"`
	VPNs    []string `json:"vpns"`
}

// PausedTracker owns the in-memory paused set and persists every change to
// disk. Unlike state.json's PID-file sibling in internal/state.go, this file
// is never removed on shutdown — the whole point is to survive a restart.
type PausedTracker struct {
	path string // "" if path resolution failed; persistence silently disabled

	mu      sync.Mutex
	tunnels map[string]bool
	vpns    map[string]bool
}

// NewPausedTracker loads state.json from configDir (typically
// filepath.Dir(cfg.Path)), if present. A missing file is normal (nothing has
// ever been paused, or a fresh install); any other read or parse error is
// logged as a warning and treated the same as a missing file — hopscotch
// always starts up with a best-effort "nothing paused" fallback rather than
// failing.
func NewPausedTracker(configDir string) *PausedTracker {
	return newPausedTrackerAt(filepath.Join(configDir, "state.json"))
}

func newPausedTrackerAt(path string) *PausedTracker {
	t := &PausedTracker{path: path, tunnels: map[string]bool{}, vpns: map[string]bool{}}

	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Warn("state file: reading failed, starting with nothing paused", "path", path, "err", err)
		}
		return t
	}
	var s PersistedState
	if err := json.Unmarshal(data, &s); err != nil {
		log.Warn("state file: parsing failed, starting with nothing paused", "path", path, "err", err)
		return t
	}
	for _, n := range s.Paused.Tunnels {
		t.tunnels[n] = true
	}
	for _, n := range s.Paused.VPNs {
		t.vpns[n] = true
	}
	return t
}

// Tunnels returns the names of tunnels that should start paused.
// A nil receiver (tracker not wired up, e.g. in tests) behaves as empty.
func (t *PausedTracker) Tunnels() []string {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return sortedKeys(t.tunnels)
}

// VPNs returns the names of VPNs that should start paused.
// A nil receiver (tracker not wired up, e.g. in tests) behaves as empty.
func (t *PausedTracker) VPNs() []string {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return sortedKeys(t.vpns)
}

// SetTunnelPaused records name's paused state and persists the change. A
// persistence failure is logged but never propagated — the in-memory
// pause/resume the caller already performed on the tunnel stays in effect
// regardless of whether it could be saved for next restart. A nil receiver
// (tracker not wired up, e.g. in tests) is a no-op.
func (t *PausedTracker) SetTunnelPaused(name string, paused bool) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if paused {
		t.tunnels[name] = true
	} else {
		delete(t.tunnels, name)
	}
	t.save()
}

// SetVPNPaused records name's paused state and persists the change. Same
// fallback behavior as SetTunnelPaused.
func (t *PausedTracker) SetVPNPaused(name string, paused bool) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if paused {
		t.vpns[name] = true
	} else {
		delete(t.vpns, name)
	}
	t.save()
}

// save writes the current paused set to disk. Called with mu held.
func (t *PausedTracker) save() {
	if t.path == "" {
		return
	}

	s := PersistedState{Paused: PausedState{Tunnels: sortedKeys(t.tunnels), VPNs: sortedKeys(t.vpns)}}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		log.Warn("state file: marshalling failed, not persisted", "err", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(t.path), 0o755); err != nil {
		log.Warn("state file: creating directory failed, not persisted", "path", t.path, "err", err)
		return
	}
	if err := os.WriteFile(t.path, data, 0o644); err != nil {
		log.Warn("state file: writing failed, not persisted", "path", t.path, "err", err)
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
