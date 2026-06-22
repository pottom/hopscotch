// Package state manages the runtime state file (PID, tunnel status snapshot).
package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"hopscotch/internal/tunnel"
	"hopscotch/internal/version"
)

// TunnelState is the per-tunnel snapshot stored in the state file.
type TunnelState struct {
	Name           string    `json:"name"`
	Status         string    `json:"status"`
	ConnectedAt    time.Time `json:"connected_at"`
	ReconnectCount int       `json:"reconnect_count"`
	LocalPort      int       `json:"local_port"`
}

// State is the full runtime state written to disk.
type State struct {
	Version    string        `json:"version"`
	PID        int           `json:"pid"`
	StartedAt  time.Time     `json:"started_at"`
	ProxyPort  int           `json:"proxy_port"`
	Tunnels    []TunnelState `json:"tunnels"`
}

// Manager handles reading and writing the state file.
type Manager struct {
	path string
}

// NewManager returns a Manager using the default state file path.
func NewManager() (*Manager, error) {
	dir, err := stateDir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("creating state dir: %w", err)
	}
	return &Manager{path: filepath.Join(dir, "state.json")}, nil
}

// PIDFile returns the path of the PID file.
func (m *Manager) PIDFile() string {
	return filepath.Join(filepath.Dir(m.path), "hopscotch.pid")
}

// Write saves current state to disk.
func (m *Manager) Write(proxyPort int, allStats map[string]tunnel.Stats) error {
	var tunnelStates []TunnelState
	for name, st := range allStats {
		tunnelStates = append(tunnelStates, TunnelState{
			Name:           name,
			Status:         st.Status.String(),
			ConnectedAt:    st.ConnectedAt,
			ReconnectCount: st.ReconnectCount,
			LocalPort:      st.LocalPort,
		})
	}

	state := &State{
		Version:   version.Version,
		PID:       os.Getpid(),
		StartedAt: time.Now(),
		ProxyPort: proxyPort,
		Tunnels:   tunnelStates,
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling state: %w", err)
	}

	return os.WriteFile(m.path, data, 0o644)
}

// Read loads the state file from disk.
func (m *Manager) Read() (*State, error) {
	data, err := os.ReadFile(m.path)
	if err != nil {
		return nil, fmt.Errorf("reading state file: %w", err)
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parsing state file: %w", err)
	}
	return &s, nil
}

// WritePID writes the current process PID to the PID file.
func (m *Manager) WritePID() error {
	return os.WriteFile(m.PIDFile(), fmt.Appendf(nil, "%d\n", os.Getpid()), 0o644)
}

// ReadPID reads the PID from the PID file.
func (m *Manager) ReadPID() (int, error) {
	data, err := os.ReadFile(m.PIDFile())
	if err != nil {
		return 0, fmt.Errorf("reading PID file: %w", err)
	}
	var pid int
	if _, err := fmt.Sscanf(string(data), "%d", &pid); err != nil {
		return 0, fmt.Errorf("parsing PID file: %w", err)
	}
	return pid, nil
}

// Remove deletes state and PID files on clean shutdown.
func (m *Manager) Remove() {
	_ = os.Remove(m.path)
	_ = os.Remove(m.PIDFile())
}

func stateDir() (string, error) {
	// Containers write ephemeral state to /tmp.
	if os.Getenv("HOPSCOTCH_CONTAINER") == "true" {
		return "/tmp", nil
	}
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("finding cache dir: %w", err)
	}
	return filepath.Join(cacheDir, "hopscotch"), nil
}
