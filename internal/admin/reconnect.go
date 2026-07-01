package admin

import "net/http"

// TunnelReconnecter triggers an immediate reconnect, or pauses/resumes, a named tunnel.
type TunnelReconnecter interface {
	ForceReconnect(name string) bool
	Pause(name string) bool
	Resume(name string) bool
}

// VPNReconnecter triggers an immediate reconnect, or pauses/resumes, a named VPN connection.
type VPNReconnecter interface {
	ForceReconnect(name string) bool
	Pause(name string) bool
	Resume(name string) bool
}

func (s *Server) handleTunnelReconnect(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !s.reconnecter.ForceReconnect(name) {
		http.Error(w, "tunnel not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleTunnelPause(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !s.reconnecter.Pause(name) {
		http.Error(w, "tunnel not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleTunnelResume(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !s.reconnecter.Resume(name) {
		http.Error(w, "tunnel not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleVPNReconnect(w http.ResponseWriter, r *http.Request) {
	if s.vpnReconnecter == nil {
		http.Error(w, "no vpns configured", http.StatusNotFound)
		return
	}
	name := r.PathValue("name")
	if !s.vpnReconnecter.ForceReconnect(name) {
		http.Error(w, "vpn not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleVPNPause(w http.ResponseWriter, r *http.Request) {
	if s.vpnReconnecter == nil {
		http.Error(w, "no vpns configured", http.StatusNotFound)
		return
	}
	name := r.PathValue("name")
	if !s.vpnReconnecter.Pause(name) {
		http.Error(w, "vpn not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleVPNResume(w http.ResponseWriter, r *http.Request) {
	if s.vpnReconnecter == nil {
		http.Error(w, "no vpns configured", http.StatusNotFound)
		return
	}
	name := r.PathValue("name")
	if !s.vpnReconnecter.Resume(name) {
		http.Error(w, "vpn not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
