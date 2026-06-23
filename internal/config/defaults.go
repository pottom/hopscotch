package config

const (
	DefaultSSHPort             = 22
	DefaultDialTimeout         = 30 // seconds
	DefaultKeepaliveInterval   = 30 // seconds
	DefaultKeepaliveMaxFails   = 3
	DefaultReconnectDelay      = 5  // seconds
	DefaultReconnectMaxDelay   = 30 // seconds
	DefaultProxyPort           = 8080
	DefaultAdminPort           = 9090
	DefaultAdminBind           = "127.0.0.1"
	DefaultTunnelWaitTimeout   = 30 // seconds – proxy waits for connecting tunnel
	DefaultPingHost            = "example.com"
	DefaultPingCount           = 3
	DefaultPingTunnelWait      = 10 // seconds
)
