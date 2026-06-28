// Package admin provides an HTTP server for health checks, metrics and status.
package admin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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

const sessionCookie = "hs_session"

// Server is the HTTP admin server.
type Server struct {
	bind             string
	port             int
	proxyBind        string
	proxyPort        int
	proxyAuthEnabled bool
	pid              int
	readme           []byte
	tunnels          TunnelStatter
	vpns             VPNStatter // nil when no VPNs configured
	direct           DirectStatter
	routes           RouteStatter
	logs             *logger.Broadcaster
	startedAt        time.Time
	cfg              *config.Config
	cfgMu            sync.Mutex
	ruleUpdater      RuleUpdater
	// admin auth — empty means no auth required
	adminUsername string
	adminPassword string
	sessionToken  string // random token set once at startup; matches cookie value
}

// NewServer creates an admin Server. Only bind "127.0.0.1" unless the config
// explicitly sets admin.bind to allow external access (needed in containers).
func NewServer(bind string, port, proxyPort int, tunnels TunnelStatter, vpns VPNStatter, direct DirectStatter, routes RouteStatter, readme []byte, cfg *config.Config, ruleUpdater RuleUpdater, proxyAuthEnabled bool) *Server {
	var sessionToken string
	if cfg.Admin.Username != "" {
		b := make([]byte, 32)
		_, _ = rand.Read(b)
		sessionToken = hex.EncodeToString(b)
	}
	return &Server{
		bind:             bind,
		port:             port,
		proxyBind:        cfg.Proxy.Bind,
		proxyPort:        proxyPort,
		proxyAuthEnabled: proxyAuthEnabled,
		pid:              os.Getpid(),
		readme:           readme,
		tunnels:          tunnels,
		vpns:             vpns,
		direct:           direct,
		routes:           routes,
		logs:             logger.GetBroadcaster(),
		startedAt:        time.Now(),
		cfg:              cfg,
		ruleUpdater:      ruleUpdater,
		adminUsername:    cfg.Admin.Username,
		adminPassword:    cfg.Admin.Password,
		sessionToken:     sessionToken,
	}
}

func (s *Server) adminAuthEnabled() bool { return s.adminUsername != "" }

// authenticated returns true if the request carries a valid session cookie,
// or if no admin auth is configured.
func (s *Server) authenticated(r *http.Request) bool {
	if !s.adminAuthEnabled() {
		return true
	}
	c, err := r.Cookie(sessionCookie)
	return err == nil && c.Value == s.sessionToken
}

// requireAuth wraps h so that unauthenticated requests are redirected to /login.
// API requests (Accept: application/json or /api/* paths) get a 401 instead.
func (s *Server) requireAuth(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.authenticated(r) {
			h.ServeHTTP(w, r)
			return
		}
		// API paths → 401, browser paths → redirect
		if r.Header.Get("Accept") == "text/event-stream" ||
			len(r.URL.Path) > 4 && r.URL.Path[:4] == "/api" ||
			r.Header.Get("Accept") == "application/json" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	})
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

	// Unauthenticated login endpoints.
	if s.adminAuthEnabled() {
		mux.HandleFunc("GET /login", s.handleLoginPage)
		mux.HandleFunc("POST /api/login", s.handleLogin)
		mux.HandleFunc("POST /api/logout", s.handleLogout)
	}

	protected := http.NewServeMux()
	protected.HandleFunc("GET /health", s.handleHealth)
	protected.HandleFunc("GET /metrics", s.handleMetrics)
	protected.HandleFunc("GET /status", s.handleStatus)
	protected.HandleFunc("GET /readme", s.handleReadme)
	protected.HandleFunc("GET /traffic/stream", s.handleTrafficStream)
	protected.HandleFunc("GET /logs/stream", s.handleLogStream)
	protected.HandleFunc("PUT /api/rules", s.handleRules)
	protected.HandleFunc("GET /api/validate-pattern", s.handleValidatePattern)
	sub, _ := fs.Sub(uiFiles, "ui")
	protected.Handle("GET /", noCacheFS(http.FileServerFS(sub)))

	mux.Handle("/", s.requireAuth(protected))

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

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	user := r.FormValue("username")
	pass := r.FormValue("password")
	if user == s.adminUsername && pass == s.adminPassword {
		http.SetCookie(w, &http.Cookie{
			Name:     sessionCookie,
			Value:    s.sessionToken,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteStrictMode,
		})
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/login?error=1", http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:    sessionCookie,
		Value:   "",
		Path:    "/",
		MaxAge:  -1,
		Expires: time.Unix(0, 0),
	})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
