package socks5

import (
	"fmt"
	"io"
)

const (
	socks5Version   = 0x05
	authNoAuth      = 0x00
	authNoAccepted  = 0xFF
)

// negotiateAuth reads the client's method list and selects NO AUTH (0x00).
// Returns an error if the client does not support NO AUTH.
func negotiateAuth(rw io.ReadWriter) error {
	// +----+----------+----------+
	// |VER | NMETHODS | METHODS  |
	// +----+----------+----------+
	// | 1  |    1     | 1 to 255 |
	// +----+----------+----------+
	header := make([]byte, 2)
	if _, err := io.ReadFull(rw, header); err != nil {
		return fmt.Errorf("reading auth header: %w", err)
	}

	if header[0] != socks5Version {
		return fmt.Errorf("unsupported SOCKS version %d", header[0])
	}

	nMethods := int(header[1])
	methods := make([]byte, nMethods)
	if _, err := io.ReadFull(rw, methods); err != nil {
		return fmt.Errorf("reading auth methods: %w", err)
	}

	for _, m := range methods {
		if m == authNoAuth {
			_, err := rw.Write([]byte{socks5Version, authNoAuth})
			return err
		}
	}

	_, _ = rw.Write([]byte{socks5Version, authNoAccepted})
	return fmt.Errorf("client does not support NO AUTH method")
}
