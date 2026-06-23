package admin

import (
	"fmt"
	"net/http"
	"time"
)

// handleLogStream serves a Server-Sent Events stream of log lines.
// New clients receive the recent backlog first, then live lines as they arrive.
func (s *Server) handleLogStream(w http.ResponseWriter, r *http.Request) {
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	for _, line := range s.logs.Backlog() {
		fmt.Fprintf(w, "data: %s\n\n", line)
	}
	flusher.Flush()

	ch := s.logs.Subscribe()
	defer s.logs.Unsubscribe(ch)

	for {
		select {
		case <-r.Context().Done():
			return
		case line, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", line)
			flusher.Flush()
		}
	}
}
