package admin

import (
	"encoding/json"
	"net/http"
	"time"

	"hopscotch/internal/netcheck"
	"hopscotch/internal/tunnel"
	"hopscotch/internal/version"
	"hopscotch/internal/vpn"
)

// RouteJSON is a single routing rule in the /status response.
type RouteJSON struct {
	Pattern string `json:"pattern"`
	Tunnel  string `json:"tunnel,omitempty"`
	Via     string `json:"via,omitempty"`
}

// TunnelStatusJSON is the per-tunnel block in the /status response.
type TunnelStatusJSON struct {
	Status            string  `json:"status"`
	Host              string  `json:"host"`
	LocalPort         int     `json:"local_port"`
	ReconnectCount    int     `json:"reconnect_count"`
	UptimeSeconds     float64 `json:"uptime_seconds"`
	RequiresVPN       string  `json:"requires_vpn,omitempty"`
	KeepaliveFailures int     `json:"keepalive_failures,omitempty"`
	LastError         string  `json:"last_error,omitempty"`
}

// VPNStatusJSON is the per-VPN block in the /status response.
type VPNStatusJSON struct {
	State         string  `json:"state"`
	Host          string  `json:"host"`
	Reconnects    int     `json:"reconnects"`
	UptimeSeconds float64 `json:"uptime_seconds"`
	TunIface      string  `json:"tun_iface,omitempty"`
	ReconnectIn   *int    `json:"reconnect_in,omitempty"`
	LastError     string  `json:"last_error,omitempty"`
}

// StatusResponse is the full /status JSON response.
type StatusResponse struct {
	Status        string                      `json:"status"`
	Version       string                      `json:"version"`
	LatestVersion string                      `json:"latest_version,omitempty"`
	Uptime        string                      `json:"uptime"`
	PID           int                         `json:"pid"`
	ProxyPort     int                         `json:"proxy_port"`
	ProxyBind     string                      `json:"proxy_bind"`
	AdminPort     int                         `json:"admin_port"`
	AdminBind     string                      `json:"admin_bind"`
	Uplink        bool                        `json:"uplink"`
	UplinkIface   string                      `json:"uplink_iface,omitempty"`
	Internet      bool                        `json:"internet"`
	PublicIP      string                      `json:"public_ip,omitempty"`
	Tunnels       map[string]TunnelStatusJSON `json:"tunnels"`
	VPNs          map[string]VPNStatusJSON    `json:"vpns,omitempty"`
	Routes        []RouteJSON                 `json:"routes"`
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
			RequiresVPN:       st.RequiresVPN,
			KeepaliveFailures: st.KeepaliveFailures,
			LastError:         st.LastError,
		}
	}

	overall := "healthy"
	if !allConnected {
		overall = "degraded"
	}

	var vpnMap map[string]VPNStatusJSON
	if s.vpns != nil {
		vpnStats := s.vpns.AllStats()
		vpnMap = make(map[string]VPNStatusJSON, len(vpnStats))
		for name, st := range vpnStats {
			uptime := 0.0
			if st.State == vpn.StateConnected && !st.ConnectedAt.IsZero() {
				uptime = time.Since(st.ConnectedAt).Seconds()
			}
			if st.State != vpn.StateConnected {
				overall = "degraded"
			}
				var reconnectIn *int
			if !st.NextReconnectAt.IsZero() {
				secs := int(time.Until(st.NextReconnectAt).Seconds())
				if secs < 0 {
					secs = 0
				}
				reconnectIn = &secs
			}
			vpnMap[name] = VPNStatusJSON{
				State:         st.State.String(),
				Host:          st.Server,
				Reconnects:    st.Reconnects,
				UptimeSeconds: uptime,
				TunIface:      st.TunIface,
				ReconnectIn:   reconnectIn,
				LastError:     st.LastError,
			}
		}
	}

	rules := s.routes.Rules()
	routes := make([]RouteJSON, len(rules))
	for i, r := range rules {
		routes[i] = RouteJSON{Pattern: r.Pattern, Tunnel: r.Tunnel, Via: r.Via}
	}

	uplink := netcheck.HasUplink()
	publicIP := ""
	if uplink {
		publicIP = netcheck.PublicIP()
	}
	resp := StatusResponse{
		Status:        overall,
		Version:       version.Version,
		LatestVersion: version.LatestVersion,
		Uptime:        time.Since(s.startedAt).Round(time.Second).String(),
		PID:           s.pid,
		ProxyPort:     s.proxyPort,
		ProxyBind:     s.proxyBind,
		AdminPort:     s.port,
		AdminBind:     s.bind,
		Uplink:        uplink,
		UplinkIface:   netcheck.UplinkInterface(),
		Internet:      uplink && netcheck.HasInternet(),
		PublicIP:      publicIP,
		Tunnels:       tunnels,
		VPNs:          vpnMap,
		Routes:        routes,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
