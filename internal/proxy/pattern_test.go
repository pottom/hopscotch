package proxy

import "testing"

func TestMatchPattern(t *testing.T) {
	tests := []struct {
		pattern string
		host    string
		want    bool
	}{
		{"*", "anything.com", true},
		{"*", "10.0.0.1", true},
		{"*.example.com", "api.example.com", true},
		{"*.example.com", "deep.api.example.com", true},
		{"*.example.com", "example.com", true},
		{"*.example.com", "other.com", false},
		{"10.0.1.*", "10.0.1.99", true},
		{"10.0.1.*", "10.0.1.0", true},
		{"10.0.1.*", "10.0.2.1", false},
		{"exact.host.com", "exact.host.com", true},
		{"exact.host.com", "other.host.com", false},
		// Generic suffix wildcard (*b.example.com)
		{"*b.tdjsz.local", "ocp4-test-b.tdjsz.local", true},
		{"*b.tdjsz.local", "oauth-openshift.apps.ocp4-test-b.tdjsz.local", true},
		{"*a.tdjsz.local", "ocp4-test-b.tdjsz.local", false},
		{"*a.tdjsz.local", "ocp4-test-a.tdjsz.local", true},
		{"*b.pdjsz.local", "server-b.pdjsz.local", true},
		{"*b.pdjsz.local", "server-a.pdjsz.local", false},
		// CIDR
		{"10.0.1.0/24", "10.0.1.1", true},
		{"10.0.1.0/24", "10.0.1.254", true},
		{"10.0.1.0/24", "10.0.2.1", false},
		{"10.0.0.0/8", "10.255.255.1", true},
		{"10.0.0.0/8", "11.0.0.1", false},
		{"10.0.1.0/24", "not-an-ip", false},
		{"192.168.0.0/16", "192.168.42.10", true},
		{"192.168.0.0/16", "192.169.0.1", false},
	}

	for _, tc := range tests {
		got := matchPattern(tc.pattern, tc.host)
		if got != tc.want {
			t.Errorf("matchPattern(%q, %q) = %v, want %v", tc.pattern, tc.host, got, tc.want)
		}
	}
}
