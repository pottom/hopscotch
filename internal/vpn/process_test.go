package vpn

import (
	"regexp"
	"testing"
)

// Two VPNs both run a process named openconnect; killing or terminating one
// must never reach the other. Go's regexp agrees with pkill's POSIX ERE on
// every construct procPattern emits, so this checks the real pattern.
func TestProcPatternMatchesOnlyOwnVPN(t *testing.T) {
	re := regexp.MustCompile(procPattern("openconnect", "https://vpn.4ig.hu"))

	for _, cmdline := range []string{
		"openconnect --authgroup 4iG --user 1 --passwd-on-stdin --resolve vpn.4ig.hu:79.120.196.68 https://vpn.4ig.hu",
		"/usr/bin/openconnect https://vpn.4ig.hu",
	} {
		if !re.MatchString(cmdline) {
			t.Errorf("own process not matched: %q", cmdline)
		}
	}

	for _, cmdline := range []string{
		"openconnect --user 1 https://m2c.vpn.4ig.hu",  // the other VPN
		"sudo openconnect --user 1 https://vpn.4ig.hu", // the sudo parent, not openconnect itself
		"openconnect https://vpn.4ig.hu.example",
		"openconnect-wrapper https://vpn.4ig.hu",
	} {
		if re.MatchString(cmdline) {
			t.Errorf("foreign process matched: %q", cmdline)
		}
	}
}
