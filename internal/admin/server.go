// Package admin provides an HTTP server for health checks and Prometheus metrics.
package admin

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/charmbracelet/log"

	"hopscotch/internal/tunnel"
)

// TunnelStatter exposes tunnel statistics to the admin server.
type TunnelStatter interface {
	AllStats() map[string]tunnel.Stats
}

// Server is the HTTP admin server.
type Server struct {
	bind      string
	port      int
	tunnels   TunnelStatter
	startedAt time.Time
}

// NewServer creates an admin Server. Only bind "127.0.0.1" unless the config
// explicitly sets admin.bind to allow external access (needed in containers).
func NewServer(bind string, port int, tunnels TunnelStatter) *Server {
	return &Server{
		bind:      bind,
		port:      port,
		tunnels:   tunnels,
		startedAt: time.Now(),
	}
}

// ListenAndServe starts the admin HTTP server. Blocks until ctx is cancelled.
func (s *Server) ListenAndServe(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /metrics", s.handleMetrics)

	addr := fmt.Sprintf("%s:%d", s.bind, s.port)
	srv := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	log.Info("admin server listening", "addr", addr)

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("admin server: %w", err)
	}
	return nil
}
