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
	}

	for _, tc := range tests {
		got := matchPattern(tc.pattern, tc.host)
		if got != tc.want {
			t.Errorf("matchPattern(%q, %q) = %v, want %v", tc.pattern, tc.host, got, tc.want)
		}
	}
}
