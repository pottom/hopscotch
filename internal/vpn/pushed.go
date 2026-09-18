package vpn

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Pushed is what the gateway handed the session, as recorded by the vpnc
// wrapper on connect (vpncscript.go). Zero when unknown.
type Pushed struct {
	VPN     string    `json:"vpn"`
	TunDev  string    `json:"tundev"`
	IP      string    `json:"ip"`
	Gateway string    `json:"gateway"`
	DNS     []string  `json:"dns"`
	Routes  []string  `json:"routes"` // addr/len, as pushed
	At      time.Time `json:"at"`
}

// readPushed loads the wrapper's record for vpnName from dir, but only when it
// describes the tunnel interface the session currently has — a record left
// behind by a crashed daemon (SIGKILL skips the disconnect hook) describes a
// device that is gone or reused.
func readPushed(dir, vpnName, tunIface string) Pushed {
	if dir == "" || tunIface == "" {
		return Pushed{}
	}
	data, err := os.ReadFile(filepath.Join(dir, pushedFileName(vpnName)))
	if err != nil {
		return Pushed{}
	}
	var p Pushed
	if err := json.Unmarshal(data, &p); err != nil || p.TunDev != tunIface {
		return Pushed{}
	}
	return p
}
