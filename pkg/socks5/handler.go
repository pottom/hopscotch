package socks5

import (
	"context"
	"fmt"
	"io"
	"net"
)

const (
	cmdConnect = 0x01

	atypIPv4   = 0x01
	atypDomain = 0x03
	atypIPv6   = 0x04

	replySuccess         = 0x00
	replyGeneralFailure  = 0x01
	replyNetUnreachable  = 0x03
	replyHostUnreachable = 0x04
	replyConnRefused     = 0x05
	replyCmdNotSupported = 0x07
	replyAddrNotSupported = 0x08
)

// Request holds the parsed SOCKS5 CONNECT request.
type Request struct {
	// Host is the destination hostname or IP address.
	Host string
	// Port is the destination TCP port.
	Port int
}

// Dialer dials a network connection for the given address.
// Implement this interface to control how the proxy reaches upstream hosts —
// for example to route through an SSH tunnel based on the target address.
type Dialer interface {
	DialContext(ctx context.Context, network, addr string) (net.Conn, error)
}

// parseRequest reads and validates the CONNECT request after auth.
func parseRequest(r io.Reader) (*Request, error) {
	// +----+-----+-------+------+----------+----------+
	// |VER | CMD |  RSV  | ATYP | DST.ADDR | DST.PORT |
	// +----+-----+-------+------+----------+----------+
	// | 1  |  1  | X'00' |  1   | Variable |    2     |
	// +----+-----+-------+------+----------+----------+
	header := make([]byte, 4)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, fmt.Errorf("reading request header: %w", err)
	}

	if header[0] != socks5Version {
		return nil, fmt.Errorf("unexpected SOCKS version %d", header[0])
	}
	if header[1] != cmdConnect {
		return nil, fmt.Errorf("unsupported command %d", header[1])
	}

	host, err := readAddr(r, header[3])
	if err != nil {
		return nil, err
	}

	portBuf := make([]byte, 2)
	if _, err := io.ReadFull(r, portBuf); err != nil {
		return nil, fmt.Errorf("reading port: %w", err)
	}
	port := int(portBuf[0])<<8 | int(portBuf[1])

	return &Request{Host: host, Port: port}, nil
}

func readAddr(r io.Reader, atyp byte) (string, error) {
	switch atyp {
	case atypIPv4:
		buf := make([]byte, 4)
		if _, err := io.ReadFull(r, buf); err != nil {
			return "", fmt.Errorf("reading IPv4: %w", err)
		}
		return net.IP(buf).String(), nil

	case atypDomain:
		lenBuf := make([]byte, 1)
		if _, err := io.ReadFull(r, lenBuf); err != nil {
			return "", fmt.Errorf("reading domain length: %w", err)
		}
		domain := make([]byte, lenBuf[0])
		if _, err := io.ReadFull(r, domain); err != nil {
			return "", fmt.Errorf("reading domain: %w", err)
		}
		return string(domain), nil

	case atypIPv6:
		buf := make([]byte, 16)
		if _, err := io.ReadFull(r, buf); err != nil {
			return "", fmt.Errorf("reading IPv6: %w", err)
		}
		return net.IP(buf).String(), nil

	default:
		return "", fmt.Errorf("unsupported address type %d", atyp)
	}
}

// sendReply writes a SOCKS5 reply to the client.
func sendReply(w io.Writer, replyCode byte) error {
	// Bind address is always 0.0.0.0:0 — we don't expose it.
	reply := []byte{
		socks5Version, replyCode, 0x00, atypIPv4,
		0, 0, 0, 0, // bind addr
		0, 0, // bind port
	}
	_, err := w.Write(reply)
	return err
}

// handle processes a single SOCKS5 client connection end-to-end.
func handle(ctx context.Context, client net.Conn, dialer Dialer, creds *Credentials) error {
	defer client.Close()

	if err := negotiateAuth(client, creds); err != nil {
		return fmt.Errorf("auth: %w", err)
	}

	req, err := parseRequest(client)
	if err != nil {
		_ = sendReply(client, replyGeneralFailure)
		return fmt.Errorf("parse request: %w", err)
	}

	addr := fmt.Sprintf("%s:%d", req.Host, req.Port)
	upstream, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		code := replyHostUnreachable
		_ = sendReply(client, byte(code))
		return fmt.Errorf("dial %s: %w", addr, err)
	}
	defer upstream.Close()

	if err := sendReply(client, replySuccess); err != nil {
		return fmt.Errorf("send reply: %w", err)
	}

	return relay(client, upstream)
}

// relay copies data between client and upstream until either side closes.
func relay(a, b net.Conn) error {
	done := make(chan error, 2)

	go func() {
		_, err := io.Copy(a, b)
		done <- err
	}()
	go func() {
		_, err := io.Copy(b, a)
		done <- err
	}()

	<-done
	return nil
}
