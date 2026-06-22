package admin

import (
	"encoding/json"
	"net/http"
	"time"

	"hopscotch/internal/tunnel"
	"hopscotch/internal/version"
)

type healthResponse struct {
	Status  string            `json:"status"`
	Version string            `json:"version"`
	Uptime  string            `json:"uptime"`
	Tunnels map[string]string `json:"tunnels"`
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	allStats := s.tunnels.AllStats()

	tunnelMap := make(map[string]string, len(allStats))
	allConnected := true
	for name, st := range allStats {
		tunnelMap[name] = st.Status.String()
		if st.Status != tunnel.StatusConnected {
			allConnected = false
		}
	}

	status := "healthy"
	httpCode := http.StatusOK
	if !allConnected {
		status = "degraded"
		httpCode = http.StatusServiceUnavailable
	}

	resp := healthResponse{
		Status:  status,
		Version: version.Version,
		Uptime:  time.Since(s.startedAt).Round(time.Second).String(),
		Tunnels: tunnelMap,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpCode)
	_ = json.NewEncoder(w).Encode(resp)
}
