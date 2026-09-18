package admin

import (
	"context"
	"net/http"

	"github.com/charmbracelet/log"
)

// TunnelReconnecter triggers an immediate reconnect, or pauses/resumes, a named tunnel.
type TunnelReconnecter interface {
	ForceReconnect(name string) bool
	Pause(name string) bool
	Resume(name string) bool
}

// VPNReconnecter triggers an immediate reconnect, pauses/resumes, or switches
// to a named VPN connection.
type VPNReconnecter interface {
	ForceReconnect(name string) bool
	Pause(name string) bool
	Resume(name string) bool
	Has(name string) bool
	// Switch resumes the named VPN and, once its own interface carries the
	// traffic, pauses the connected VPNs it took the routes from, returning
	// their names. See vpn.Manager.Switch.
	Switch(ctx context.Context, name string) ([]string, error)
}

func (s *Server) handleTunnelReconnect(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	s.suppressNotify("tunnel", name)
	if !s.reconnecter.ForceReconnect(name) {
		http.Error(w, "tunnel not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// suppressNotify tells the notifier (if configured) that the next
// disconnect/reconnect blip for kind/name is a manual, intentional action —
// see NotifyController.
func (s *Server) suppressNotify(kind, name string) {
	if s.notifyCtl != nil {
		s.notifyCtl.Suppress(kind, name)
	}
}

func (s *Server) handleTunnelPause(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !s.reconnecter.Pause(name) {
		http.Error(w, "tunnel not found", http.StatusNotFound)
		return
	}
	s.pausedTracker.SetTunnelPaused(name, true)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleTunnelResume(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	s.suppressNotify("tunnel", name)
	if !s.reconnecter.Resume(name) {
		http.Error(w, "tunnel not found", http.StatusNotFound)
		return
	}
	s.pausedTracker.SetTunnelPaused(name, false)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleVPNReconnect(w http.ResponseWriter, r *http.Request) {
	if s.vpnReconnecter == nil {
		http.Error(w, "no vpns configured", http.StatusNotFound)
		return
	}
	name := r.PathValue("name")
	s.suppressNotify("vpn", name)
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
	s.pausedTracker.SetVPNPaused(name, true)
	w.WriteHeader(http.StatusNoContent)
}

// handleVPNSwitch starts a switch to the named VPN (vpn.Manager.Switch) and
// returns at once; the switch itself runs in the background because it waits
// for the VPN to come up. Pause state is persisted like the manual
// pause/resume handlers do: the target counts as resumed immediately, the VPNs
// the switch pauses once it has paused them.
func (s *Server) handleVPNSwitch(w http.ResponseWriter, r *http.Request) {
	if s.vpnReconnecter == nil {
		http.Error(w, "no vpns configured", http.StatusNotFound)
		return
	}
	name := r.PathValue("name")
	if !s.vpnReconnecter.Has(name) {
		http.Error(w, "vpn not found", http.StatusNotFound)
		return
	}
	s.suppressNotify("vpn", name)
	s.pausedTracker.SetVPNPaused(name, false)
	go func() {
		paused, err := s.vpnReconnecter.Switch(context.Background(), name)
		for _, p := range paused {
			s.suppressNotify("vpn", p)
			s.pausedTracker.SetVPNPaused(p, true)
		}
		if err != nil {
			log.Warn("vpn switch did not complete", "vpn", name, "err", err)
		}
	}()
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) handleVPNResume(w http.ResponseWriter, r *http.Request) {
	if s.vpnReconnecter == nil {
		http.Error(w, "no vpns configured", http.StatusNotFound)
		return
	}
	name := r.PathValue("name")
	s.suppressNotify("vpn", name)
	if !s.vpnReconnecter.Resume(name) {
		http.Error(w, "vpn not found", http.StatusNotFound)
		return
	}
	s.pausedTracker.SetVPNPaused(name, false)
	w.WriteHeader(http.StatusNoContent)
}
