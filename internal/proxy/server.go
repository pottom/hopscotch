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
}

// NewServer creates a proxy Server bound to the given address and port.
func NewServer(bind string, port int, router *Router) *Server {
	return &Server{Bind: bind, Port: port, router: router}
}

// ListenAndServe starts the SOCKS5 server. Blocks until ctx is cancelled.
func (s *Server) ListenAndServe(ctx context.Context) error {
	srv := &socks5.Server{
		Addr:   fmt.Sprintf("%s:%d", s.Bind, s.Port),
		Dialer: s.router,
	}
	return srv.ListenAndServe(ctx)
}
