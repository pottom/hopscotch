package netcheck

import "net"

// RouteInterface returns the name of the interface the kernel would send a
// packet to host through ("" when unknown). host is host:port or a bare host;
// a UDP connect resolves the route without sending anything. Used to tell
// which VPN's interface actually carries traffic to a destination when two
// VPNs push routes for the same networks.
func RouteInterface(host string) string {
	if _, _, err := net.SplitHostPort(host); err != nil {
		host = net.JoinHostPort(host, "53")
	}
	conn, err := net.Dial("udp4", host)
	if err != nil {
		return ""
	}
	defer conn.Close()
	return ifaceForIP(conn.LocalAddr().(*net.UDPAddr).IP)
}

// ifaceForIP returns the interface that has ip assigned, or "".
func ifaceForIP(ip net.IP) string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range ifaces {
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			var got net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				got = v.IP
			case *net.IPAddr:
				got = v.IP
			}
			if got.Equal(ip) {
				return iface.Name
			}
		}
	}
	return ""
}
