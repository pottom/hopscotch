package proxy

import (
	"context"
	"fmt"

	"hopscotch/pkg/socks5"
)

// Server wraps a SOCKS5 server with a routing-aware dialer.
type Server struct {
	Port   int
	router *Router
}

// NewServer creates a proxy Server bound to the given port.
func NewServer(port int, router *Router) *Server {
	return &Server{Port: port, router: router}
}

// ListenAndServe starts the SOCKS5 server. Blocks until ctx is cancelled.
func (s *Server) ListenAndServe(ctx context.Context) error {
	srv := &socks5.Server{
		Addr:   fmt.Sprintf("0.0.0.0:%d", s.Port),
		Dialer: s.router,
	}
	return srv.ListenAndServe(ctx)
}
