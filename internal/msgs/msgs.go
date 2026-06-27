// Package msgs defines shared message string constants that are set by the
// tunnel and vpn packages and checked by the tui and admin packages.
// JS mirrors: isTunnelProgressMsg / isVPNProgressMsg in app.js.
package msgs

// Status strings — mirror tunnel.Status.String() and vpn.State.String().
const (
	StatusConnected    = "connected"
	StatusConnecting   = "connecting"
	StatusDisconnected = "disconnected"
)

// Progress/error messages stored in LastError during connect phases.
const (
	// WaitingForNetwork is stored as LastError while waiting for a network uplink.
	// Used by both tunnel and vpn packages.
	WaitingForNetwork = "waiting for network"

	// WaitingForVPNPrefix is the prefix stored as LastError when a tunnel is
	// blocked on its required VPN. The VPN name follows the prefix.
	WaitingForVPNPrefix = "waiting for VPN: "

	// WaitingForVPNTunnel is stored by the VPN subprocess while probing for a
	// working layer-3 interface after openconnect reports "connected".
	WaitingForVPNTunnel = "waiting for VPN tunnel"
)
