// Package admin provides an HTTP server for health checks, metrics and status.
package admin

import (
	"context"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/charmbracelet/log"

	"hopscotch/internal/config"
	"hopscotch/internal/logger"
	"hopscotch/internal/tunnel"
	"hopscotch/internal/vpn"
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

// VPNStatter exposes VPN connection statistics to the admin server.
type VPNStatter interface {
	AllStats() map[string]vpn.Stats
}

// Server is the HTTP admin server.
type Server struct {
	bind        string
	port        int
	proxyBind   string
	proxyPort   int
	pid         int
	readme      []byte
	tunnels     TunnelStatter
	vpns        VPNStatter // nil when no VPNs configured
	direct      DirectStatter
	routes      RouteStatter
	logs        *logger.Broadcaster
	startedAt   time.Time
	cfg         *config.Config
	cfgMu       sync.Mutex
	ruleUpdater RuleUpdater
}

// NewServer creates an admin Server. Only bind "127.0.0.1" unless the config
// explicitly sets admin.bind to allow external access (needed in containers).
func NewServer(bind string, port, proxyPort int, tunnels TunnelStatter, vpns VPNStatter, direct DirectStatter, routes RouteStatter, readme []byte, cfg *config.Config, ruleUpdater RuleUpdater) *Server {
	return &Server{
		bind:        bind,
		port:        port,
		proxyBind:   cfg.Proxy.Bind,
		proxyPort:   proxyPort,
		pid:         os.Getpid(),
		readme:      readme,
		tunnels:     tunnels,
		vpns:        vpns,
		direct:      direct,
		routes:      routes,
		logs:        logger.GetBroadcaster(),
		startedAt:   time.Now(),
		cfg:         cfg,
		ruleUpdater: ruleUpdater,
	}
}

func noCacheFS(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		h.ServeHTTP(w, r)
	})
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
	mux.HandleFunc("PUT /api/rules", s.handleRules)
	mux.HandleFunc("GET /api/validate-pattern", s.handleValidatePattern)
	sub, _ := fs.Sub(uiFiles, "ui")
	mux.Handle("GET /", noCacheFS(http.FileServerFS(sub)))

	addr := fmt.Sprintf("%s:%d", s.bind, s.port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("admin server: %w", err)
	}
	defer ln.Close()

	log.Info("admin server listening", "addr", addr)

	srv := &http.Server{
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("admin server: %w", err)
	}
	return nil
}
