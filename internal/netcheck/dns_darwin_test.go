package netcheck

import (
	"reflect"
	"testing"
)

func TestParseScutilDNS(t *testing.T) {
	out := `DNS configuration

resolver #1
  search domain[0] : corp.example
  nameserver[0] : 10.4.60.100
  nameserver[1] : 10.4.60.101
  if_index : 25 (utun4)
  flags    : Request A records, Request AAAA records
  reach    : 0x00000002 (Reachable)

resolver #2
  domain   : local
  nameserver[0] : 224.0.0.251
  options  : mdns
`
	got := parseScutilDNS(out)
	want := []string{"10.4.60.100", "10.4.60.101"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseScutilDNS = %v, want %v", got, want)
	}
}
