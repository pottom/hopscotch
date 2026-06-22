package admin

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"hopscotch/internal/tunnel"
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

	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	fmt.Fprint(w, b.String())
}
