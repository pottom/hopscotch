package admin

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/pottom/hopscotch/internal/tunnel"
)

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	allStats := s.tunnels.AllStats()

	var b strings.Builder

	writeLine := func(format string, args ...any) {
		fmt.Fprintf(&b, format+"\n", args...)
	}

	writeLine("# HELP hopscotch_tunnel_status Tunnel connection status (1=connected, 0=other)")
	writeLine("# TYPE hopscotch_tunnel_status gauge")
	for name, st := range allStats {
		v := 0
		if st.Status == tunnel.StatusConnected {
			v = 1
		}
		writeLine(`hopscotch_tunnel_status{tunnel=%q} %d`, name, v)
	}

	writeLine("# HELP hopscotch_tunnel_reconnects_total Total reconnect attempts per tunnel")
	writeLine("# TYPE hopscotch_tunnel_reconnects_total counter")
	for name, st := range allStats {
		writeLine(`hopscotch_tunnel_reconnects_total{tunnel=%q} %d`, name, st.ReconnectCount)
	}

	writeLine("# HELP hopscotch_tunnel_uptime_seconds Tunnel uptime in seconds")
	writeLine("# TYPE hopscotch_tunnel_uptime_seconds gauge")
	for name, st := range allStats {
		uptime := 0.0
		if st.Status == tunnel.StatusConnected && !st.ConnectedAt.IsZero() {
			uptime = time.Since(st.ConnectedAt).Seconds()
		}
		writeLine(`hopscotch_tunnel_uptime_seconds{tunnel=%q} %.0f`, name, uptime)
	}

	writeLine("# HELP hopscotch_tunnel_bytes_in_total Cumulative bytes received through tunnel")
	writeLine("# TYPE hopscotch_tunnel_bytes_in_total counter")
	for name, st := range allStats {
		writeLine(`hopscotch_tunnel_bytes_in_total{tunnel=%q} %d`, name, st.BytesIn)
	}

	writeLine("# HELP hopscotch_tunnel_bytes_out_total Cumulative bytes sent through tunnel")
	writeLine("# TYPE hopscotch_tunnel_bytes_out_total counter")
	for name, st := range allStats {
		writeLine(`hopscotch_tunnel_bytes_out_total{tunnel=%q} %d`, name, st.BytesOut)
	}

	writeLine("# HELP hopscotch_tunnel_active_connections Active connections through tunnel")
	writeLine("# TYPE hopscotch_tunnel_active_connections gauge")
	for name, st := range allStats {
		writeLine(`hopscotch_tunnel_active_connections{tunnel=%q} %d`, name, st.ActiveConns)
	}

	writeLine("# HELP hopscotch_tunnel_keepalive_failures Consecutive keepalive failures (resets to 0 on success or reconnect)")
	writeLine("# TYPE hopscotch_tunnel_keepalive_failures gauge")
	for name, st := range allStats {
		writeLine(`hopscotch_tunnel_keepalive_failures{tunnel=%q} %d`, name, st.KeepaliveFailures)
	}

	directSnap := s.direct.DirectSnapshot()
	writeLine("# HELP hopscotch_direct_bytes_in_total Cumulative bytes received via direct connections")
	writeLine("# TYPE hopscotch_direct_bytes_in_total counter")
	writeLine(`hopscotch_direct_bytes_in_total %d`, directSnap.BytesIn)

	writeLine("# HELP hopscotch_direct_bytes_out_total Cumulative bytes sent via direct connections")
	writeLine("# TYPE hopscotch_direct_bytes_out_total counter")
	writeLine(`hopscotch_direct_bytes_out_total %d`, directSnap.BytesOut)

	writeLine("# HELP hopscotch_direct_active_connections Active direct connections")
	writeLine("# TYPE hopscotch_direct_active_connections gauge")
	writeLine(`hopscotch_direct_active_connections %d`, directSnap.ActiveConns)

	if s.vpns != nil {
		vpnStats := s.vpns.AllStats()

		writeLine("# HELP hopscotch_vpn_status VPN connection state (0=disconnected 1=connecting 2=connected)")
		writeLine("# TYPE hopscotch_vpn_status gauge")
		for name, st := range vpnStats {
			writeLine(`hopscotch_vpn_status{vpn=%q} %d`, name, int(st.State))
		}

		writeLine("# HELP hopscotch_vpn_reconnects_total Total reconnect attempts per VPN")
		writeLine("# TYPE hopscotch_vpn_reconnects_total counter")
		for name, st := range vpnStats {
			writeLine(`hopscotch_vpn_reconnects_total{vpn=%q} %d`, name, st.Reconnects)
		}

		writeLine("# HELP hopscotch_vpn_uptime_seconds Seconds since VPN last connected")
		writeLine("# TYPE hopscotch_vpn_uptime_seconds gauge")
		for name, st := range vpnStats {
			uptime := 0.0
			if !st.ConnectedAt.IsZero() {
				uptime = time.Since(st.ConnectedAt).Seconds()
			}
			writeLine(`hopscotch_vpn_uptime_seconds{vpn=%q} %.0f`, name, uptime)
		}
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	fmt.Fprint(w, b.String())
}
