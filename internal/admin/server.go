// Package admin provides an HTTP server for health checks, metrics and status.
package admin

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"time"

	"github.com/charmbracelet/log"

	"hopscotch/internal/config"
	"hopscotch/internal/logger"
	"hopscotch/internal/tunnel"
)

// TunnelStatter exposes tunnel statistics to the admin server.
type TunnelStatter interface {
	AllStats() map[string]tunnel.Stats
}

// DirectStatter exposes direct-connection traffic metrics to the admin server.
type DirectStatter interface {
	DirectSnapshot() tunnel.TrafficSnapshot
}

// RouteStatter exposes the proxy routing rules to the admin server.
type RouteStatter interface {
	Rules() []config.Rule
}

// Server is the HTTP admin server.
type Server struct {
	bind      string
	port      int
	proxyPort int
	pid       int
	readme    []byte
	tunnels   TunnelStatter
	direct    DirectStatter
	routes    RouteStatter
	logs      *logger.Broadcaster
	startedAt time.Time
}

// NewServer creates an admin Server. Only bind "127.0.0.1" unless the config
// explicitly sets admin.bind to allow external access (needed in containers).
func NewServer(bind string, port, proxyPort int, tunnels TunnelStatter, direct DirectStatter, routes RouteStatter, readme []byte) *Server {
	return &Server{
		bind:      bind,
		port:      port,
		proxyPort: proxyPort,
		pid:       os.Getpid(),
		readme:    readme,
		tunnels:   tunnels,
		direct:    direct,
		routes:    routes,
		logs:      logger.GetBroadcaster(),
		startedAt: time.Now(),
	}
}

// ListenAndServe starts the admin HTTP server. Blocks until ctx is cancelled.
func (s *Server) ListenAndServe(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /metrics", s.handleMetrics)
	mux.HandleFunc("GET /status", s.handleStatus)
	mux.HandleFunc("GET /readme", s.handleReadme)
	mux.HandleFunc("GET /traffic/stream", s.handleTrafficStream)
	mux.HandleFunc("GET /logs/stream", s.handleLogStream)
	sub, _ := fs.Sub(uiFiles, "ui")
	mux.Handle("GET /", http.FileServerFS(sub))

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
