package proxy

import (
	"context"
	"fmt"

	"hopscotch/pkg/socks5"
)

// Server wraps a SOCKS5 server with a routing-aware dialer.
type Server struct {
	Bind   string
	Port   int
	router *Router
	creds  *socks5.Credentials
}

// NewServer creates a proxy Server. If username is non-empty, SOCKS5
// username/password authentication (RFC 1929) is required on every connection.
func NewServer(bind string, port int, router *Router, username, password string) *Server {
	var creds *socks5.Credentials
	if username != "" {
		creds = &socks5.Credentials{Username: username, Password: password}
	}
	return &Server{Bind: bind, Port: port, router: router, creds: creds}
}

// AuthEnabled reports whether SOCKS5 authentication is configured.
func (s *Server) AuthEnabled() bool { return s.creds != nil }

// ListenAndServe starts the SOCKS5 server. Blocks until ctx is cancelled.
func (s *Server) ListenAndServe(ctx context.Context) error {
	srv := &socks5.Server{
		Addr:        fmt.Sprintf("%s:%d", s.Bind, s.Port),
		Dialer:      s.router,
		Credentials: s.creds,
	}
	return srv.ListenAndServe(ctx)
}
