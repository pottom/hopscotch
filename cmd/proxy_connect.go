package cmd

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"

	"github.com/spf13/cobra"

	"hopscotch/internal/config"
)

var proxyConnectCmd = &cobra.Command{
	Use:   "proxy-connect <host> <port>",
	Short: "SOCKS5 stdio bridge for use as SSH ProxyCommand",
	Long: `Connects to host:port via the running hopscotch SOCKS5 proxy and
bridges stdin/stdout. Intended for use as an SSH ProxyCommand:

  # ~/.ssh/config
  Host 10.215.*
      ProxyCommand hopscotch proxy-connect %h %p

See 'hopscotch ssh-config' to generate the full SSH config block.`,
	Args:               cobra.ExactArgs(2),
	RunE:               runProxyConnect,
	DisableFlagParsing: false,
}

func init() {
	rootCmd.AddCommand(proxyConnectCmd)
}

func runProxyConnect(_ *cobra.Command, args []string) error {
	host := args[0]
	port, err := strconv.Atoi(args[1])
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("invalid port: %s", args[1])
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	proxyAddr := fmt.Sprintf("127.0.0.1:%d", cfg.Proxy.Port)
	conn, err := net.Dial("tcp", proxyAddr)
	if err != nil {
		return fmt.Errorf("hopscotch SOCKS5 proxy not reachable at %s — is hopscotch running?", proxyAddr)
	}
	defer conn.Close()

	if err := socks5Connect(conn, host, uint16(port)); err != nil {
		return fmt.Errorf("SOCKS5 connect %s:%d: %w", host, port, err)
	}

	// Bridge SSH stdin/stdout to the SOCKS5 connection.
	done := make(chan struct{}, 2)
	go func() { io.Copy(conn, os.Stdin); done <- struct{}{} }()   //nolint:errcheck
	go func() { io.Copy(os.Stdout, conn); done <- struct{}{} }() //nolint:errcheck
	<-done
	return nil
}

// socks5Connect performs a SOCKS5 no-auth handshake and sends a CONNECT request.
func socks5Connect(conn net.Conn, host string, port uint16) error {
	// Greeting: version 5, one method: no-auth.
	if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		return err
	}
	resp := make([]byte, 2)
	if _, err := io.ReadFull(conn, resp); err != nil {
		return err
	}
	if resp[0] != 0x05 || resp[1] != 0x00 {
		return fmt.Errorf("unexpected server method: %02x", resp[1])
	}

	// CONNECT request: VER=5 CMD=CONNECT RSV=0 ATYP=DOMAINNAME len(host) host port.
	req := []byte{0x05, 0x01, 0x00, 0x03, byte(len(host))}
	req = append(req, host...)
	req = binary.BigEndian.AppendUint16(req, port)
	if _, err := conn.Write(req); err != nil {
		return err
	}

	// Response header: VER REP RSV ATYP.
	head := make([]byte, 4)
	if _, err := io.ReadFull(conn, head); err != nil {
		return err
	}
	if head[1] != 0x00 {
		return fmt.Errorf("connection rejected (reply 0x%02x)", head[1])
	}
	// Drain the bound address we don't need.
	switch head[3] {
	case 0x01: // IPv4 + port
		io.ReadFull(conn, make([]byte, 6)) //nolint:errcheck
	case 0x03: // domain name length + name + port
		n := make([]byte, 1)
		io.ReadFull(conn, n) //nolint:errcheck
		io.ReadFull(conn, make([]byte, int(n[0])+2)) //nolint:errcheck
	case 0x04: // IPv6 + port
		io.ReadFull(conn, make([]byte, 18)) //nolint:errcheck
	}
	return nil
}
