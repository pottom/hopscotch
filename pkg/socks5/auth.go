package socks5

import (
	"fmt"
	"io"
)

const (
	socks5Version  = 0x05
	authNoAuth     = 0x00
	authUserPass   = 0x02
	authNoAccepted = 0xFF
	authSubVersion = 0x01
)

// Credentials holds optional username/password for SOCKS5 auth (RFC 1929).
// A nil or zero-value Credentials disables authentication entirely.
type Credentials struct {
	Username string
	Password string
}

// negotiateAuth reads the client's method list, selects the appropriate method,
// and performs RFC 1929 subnegotiation when credentials are configured.
func negotiateAuth(rw io.ReadWriter, creds *Credentials) error {
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

	if creds != nil && creds.Username != "" {
		for _, m := range methods {
			if m == authUserPass {
				if _, err := rw.Write([]byte{socks5Version, authUserPass}); err != nil {
					return err
				}
				return subnegotiate(rw, creds)
			}
		}
		_, _ = rw.Write([]byte{socks5Version, authNoAccepted})
		return fmt.Errorf("client does not support username/password auth")
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

// subnegotiate performs the RFC 1929 username/password subnegotiation.
func subnegotiate(rw io.ReadWriter, creds *Credentials) error {
	ver := make([]byte, 1)
	if _, err := io.ReadFull(rw, ver); err != nil {
		return fmt.Errorf("reading subneg version: %w", err)
	}
	if ver[0] != authSubVersion {
		_, _ = rw.Write([]byte{authSubVersion, 0xFF})
		return fmt.Errorf("unsupported subneg version %d", ver[0])
	}

	ulenBuf := make([]byte, 1)
	if _, err := io.ReadFull(rw, ulenBuf); err != nil {
		return fmt.Errorf("reading username length: %w", err)
	}
	uname := make([]byte, ulenBuf[0])
	if _, err := io.ReadFull(rw, uname); err != nil {
		return fmt.Errorf("reading username: %w", err)
	}

	plenBuf := make([]byte, 1)
	if _, err := io.ReadFull(rw, plenBuf); err != nil {
		return fmt.Errorf("reading password length: %w", err)
	}
	passwd := make([]byte, plenBuf[0])
	if _, err := io.ReadFull(rw, passwd); err != nil {
		return fmt.Errorf("reading password: %w", err)
	}

	if string(uname) == creds.Username && string(passwd) == creds.Password {
		_, err := rw.Write([]byte{authSubVersion, 0x00})
		return err
	}
	_, _ = rw.Write([]byte{authSubVersion, 0xFF})
	return fmt.Errorf("invalid credentials")
}
