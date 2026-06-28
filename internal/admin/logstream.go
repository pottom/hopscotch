package admin

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/charmbracelet/log"

	"github.com/pottom/hopscotch/internal/logger"
)

const logStreamHeartbeat = 15 * time.Second

// handleLogStream serves a Server-Sent Events stream of log lines.
// New clients receive the recent backlog first, then live lines as they arrive.
// The optional ?level=DEBUG|INFO|WARN|ERROR query parameter sets the minimum
// log level; defaults to DEBUG (send everything, let clients filter).
func (s *Server) handleLogStream(w http.ResponseWriter, r *http.Request) {
	minLevel := parseLogLevel(r.URL.Query().Get("level"))

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

	for _, line := range s.logs.Backlog(minLevel) {
		fmt.Fprintf(w, "data: %s\n\n", line)
	}
	flusher.Flush()

	ch := s.logs.Subscribe()
	defer s.logs.Unsubscribe(ch)

	heartbeat := time.NewTicker(logStreamHeartbeat)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case line, ok := <-ch:
			if !ok {
				return
			}
			if logger.LineLevel(line) >= minLevel {
				fmt.Fprintf(w, "data: %s\n\n", line)
				flusher.Flush()
			}
		case <-heartbeat.C:
			fmt.Fprintf(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

func parseLogLevel(s string) log.Level {
	switch strings.ToUpper(s) {
	case "DEBUG", "DEBU":
		return log.DebugLevel
	case "WARN":
		return log.WarnLevel
	case "ERROR", "ERRO":
		return log.ErrorLevel
	default:
		return log.DebugLevel // send everything; clients pick their filter
	}
}
