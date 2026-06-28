// Package socks5 implements a SOCKS5 proxy server per RFC 1928.
// Only NO AUTH (0x00) and CONNECT command are supported.
package socks5

import (
	"context"
	"fmt"
	"net"

	"github.com/charmbracelet/log"
)

// Server is a SOCKS5 proxy server that delegates dialing to a [Dialer].
type Server struct {
	// Addr is the TCP address to listen on, e.g. "0.0.0.0:8888".
	Addr string
	// Dialer selects and opens the upstream connection for each CONNECT request.
	Dialer Dialer
	// Credentials enables RFC 1929 username/password auth. Nil means no auth.
	Credentials *Credentials
}

// ListenAndServe starts accepting connections on s.Addr.
// Blocks until ctx is cancelled.
func (s *Server) ListenAndServe(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.Addr)
	if err != nil {
		return fmt.Errorf("socks5 listen %s: %w", s.Addr, err)
	}
	defer ln.Close()

	log.Info("SOCKS5 proxy listening", "addr", s.Addr)

	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil // clean shutdown
			}
			log.Warn("socks5 accept error", "err", err)
			continue
		}

		go func(c net.Conn) {
			if err := handle(ctx, c, s.Dialer, s.Credentials); err != nil {
				log.Debug("socks5 connection closed", "err", err)
			}
		}(conn)
	}
}
