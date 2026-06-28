package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/pottom/hopscotch/internal/tunnel"
	"github.com/pottom/hopscotch/internal/vpn"
)

// trafficEntry is the per-source payload sent over SSE each second.
type trafficEntry struct {
	BpsIn       uint64 `json:"bps_in"`                 // bytes/s received
	BpsOut      uint64 `json:"bps_out"`                // bytes/s sent
	Active      int64  `json:"active"`                 // current open connections
	ReconnectIn *int   `json:"reconnect_in,omitempty"` // seconds until next attempt (connecting only)
}

// vpnEntry is the per-VPN payload sent over SSE each second.
type vpnEntry struct {
	ReconnectIn *int `json:"reconnect_in,omitempty"` // seconds until next attempt (connecting only)
}

// trafficPayload is the full SSE message body.
type trafficPayload struct {
	Tunnels map[string]trafficEntry `json:"tunnels"`
	VPNs    map[string]vpnEntry    `json:"vpns,omitempty"`
	Direct  trafficEntry            `json:"direct"`
}

// trafficState holds the previous snapshot to compute per-second deltas.
type trafficState struct {
	tunnels map[string]tunnel.Stats
	vpns    map[string]vpn.Stats
	direct  tunnel.TrafficSnapshot
}

func (s *Server) collectState() trafficState {
	ts := trafficState{
		tunnels: s.tunnels.AllStats(),
		direct:  s.direct.DirectSnapshot(),
	}
	if s.vpns != nil {
		ts.vpns = s.vpns.AllStats()
	}
	return ts
}

func buildPayload(prev, curr trafficState) trafficPayload {
	p := trafficPayload{
		Tunnels: make(map[string]trafficEntry, len(curr.tunnels)),
	}

	for name, cs := range curr.tunnels {
		ps := prev.tunnels[name]
		e := trafficEntry{
			BpsIn:  delta(ps.BytesIn, cs.BytesIn),
			BpsOut: delta(ps.BytesOut, cs.BytesOut),
			Active: cs.ActiveConns,
		}
		if cs.Status == tunnel.StatusConnecting && !cs.NextReconnectAt.IsZero() {
			secs := int(time.Until(cs.NextReconnectAt).Seconds())
			if secs < 0 {
				secs = 0
			}
			e.ReconnectIn = &secs
		}
		p.Tunnels[name] = e
	}

	if len(curr.vpns) > 0 {
		p.VPNs = make(map[string]vpnEntry, len(curr.vpns))
		for name, vs := range curr.vpns {
			e := vpnEntry{}
			if !vs.NextReconnectAt.IsZero() {
				secs := int(time.Until(vs.NextReconnectAt).Seconds())
				if secs < 0 {
					secs = 0
				}
				e.ReconnectIn = &secs
			}
			p.VPNs[name] = e
		}
	}

	p.Direct = trafficEntry{
		BpsIn:  delta(prev.direct.BytesIn, curr.direct.BytesIn),
		BpsOut: delta(prev.direct.BytesOut, curr.direct.BytesOut),
		Active: curr.direct.ActiveConns,
	}

	return p
}

func delta(prev, curr uint64) uint64 {
	if curr >= prev {
		return curr - prev
	}
	return 0 // counter reset (process restart)
}

// handleTrafficStream serves a Server-Sent Events stream of per-second
// traffic deltas for all tunnels and direct connections.
func (s *Server) handleTrafficStream(w http.ResponseWriter, r *http.Request) {
	// Disable the server's write timeout for this long-lived connection.
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable nginx buffering if proxied

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	prev := s.collectState()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			curr := s.collectState()
			payload := buildPayload(prev, curr)
			prev = curr

			data, err := json.Marshal(payload)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}
