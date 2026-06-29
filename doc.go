// Command hopscotch is an SSH tunnel manager and SOCKS5 proxy router.
//
// It keeps multiple SSH tunnels alive simultaneously and routes outgoing
// connections through them based on configurable pattern rules (hostname
// globs, CIDR ranges, or exact matches). An optional VPN (OpenConnect /
// Cisco AnyConnect) is managed as a subprocess and tunnels can declare a
// VPN dependency so they wait for it before dialing.
//
// # Quick start
//
//	hopscotch start          # start daemon in the background
//	hopscotch status         # show tunnel/VPN state
//	hopscotch logs           # tail live log stream
//	hopscotch stop           # graceful shutdown
//
// # Configuration
//
// hopscotch reads ~/.config/hopscotch/config.yaml (or the path given by
// --config). A minimal example:
//
//	proxy:
//	  port: 8888
//	tunnels:
//	  - name: prod
//	    host: jump.prod.example.com
//	    user: alice
//	    local_port: 1080
//	    rules:
//	      - pattern: "*.prod.internal"
//	        via: prod
//
// Configure curl, git, or any HTTP client to use SOCKS5 at 127.0.0.1:8888.
//
// # Architecture
//
// Each tunnel runs a dedicated goroutine with exponential-backoff reconnect
// logic. A SOCKS5 server accepts client connections and the router matches
// the target address against ordered rules to select the right tunnel.
// An embedded web UI and REST API are available at the admin address
// (default 127.0.0.1:9090).
//
// See https://github.com/pottom/hopscotch for full documentation.
package main
