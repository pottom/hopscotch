package admin

import (
	"encoding/json"
	"net/http"
	"time"

	"hopscotch/internal/tunnel"
	"hopscotch/internal/version"
)

// TunnelStatusJSON is the per-tunnel block in the /status response.
type TunnelStatusJSON struct {
	Status            string  `json:"status"`
	Host              string  `json:"host"`
	LocalPort         int     `json:"local_port"`
	ReconnectCount    int     `json:"reconnect_count"`
	UptimeSeconds     float64 `json:"uptime_seconds"`
	KeepaliveFailures int     `json:"keepalive_failures,omitempty"`
	LastError         string  `json:"last_error,omitempty"`
}

// StatusResponse is the full /status JSON response.
type StatusResponse struct {
	Status    string                      `json:"status"`
	Version   string                      `json:"version"`
	Uptime    string                      `json:"uptime"`
	PID       int                         `json:"pid"`
	ProxyPort int                         `json:"proxy_port"`
	AdminPort int                         `json:"admin_port"`
	Tunnels   map[string]TunnelStatusJSON `json:"tunnels"`
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	allStats := s.tunnels.AllStats()

	tunnels := make(map[string]TunnelStatusJSON, len(allStats))
	allConnected := true
	for name, st := range allStats {
		uptime := 0.0
		if st.Status == tunnel.StatusConnected && !st.ConnectedAt.IsZero() {
			uptime = time.Since(st.ConnectedAt).Seconds()
		}
		if st.Status != tunnel.StatusConnected {
			allConnected = false
		}
		tunnels[name] = TunnelStatusJSON{
			Status:            st.Status.String(),
			Host:              st.Host,
			LocalPort:         st.LocalPort,
			ReconnectCount:    st.ReconnectCount,
			UptimeSeconds:     uptime,
			KeepaliveFailures: st.KeepaliveFailures,
			LastError:         st.LastError,
		}
	}

	overall := "healthy"
	if !allConnected {
		overall = "degraded"
	}

	resp := StatusResponse{
		Status:    overall,
		Version:   version.Version,
		Uptime:    time.Since(s.startedAt).Round(time.Second).String(),
		PID:       s.pid,
		ProxyPort: s.proxyPort,
		AdminPort: s.port,
		Tunnels:   tunnels,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
