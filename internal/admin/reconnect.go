package admin

import "net/http"

// TunnelReconnecter triggers an immediate reconnect for a named tunnel.
type TunnelReconnecter interface {
	ForceReconnect(name string) bool
}

func (s *Server) handleTunnelReconnect(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !s.reconnecter.ForceReconnect(name) {
		http.Error(w, "tunnel not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
