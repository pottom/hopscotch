package updater

import "testing"

func TestIsNewer(t *testing.T) {
	for _, tc := range []struct {
		current, tag string
		want         bool
	}{
		// The regression: a string compare saw "0.10.0" as older than "0.9.1".
		{"v0.9.1", "v0.10.0", true},
		{"v0.9.1-2-g7f16327-dirty", "v0.10.0", true}, // git-describe dev build
		{"v0.10.9", "v0.10.10", true},
		{"v0.10.0", "v1.0.0", true},
		{"0.10.0", "v0.10.1", true}, // no leading v

		{"v0.10.0", "v0.10.0", false},
		{"v0.10.0-3-gabc1234-dirty", "v0.10.0", false}, // dev build on top of the latest release
		{"v0.10.0", "v0.9.1", false},
		{"v1.0.0", "v0.99.0", false},

		{"dev", "v0.10.0", false}, // unversioned build never self-updates
		{"v0.10.0", "latest", false},
		{"v0.10", "v0.11.0", false},
	} {
		if got := IsNewer(tc.current, tc.tag); got != tc.want {
			t.Errorf("IsNewer(%q, %q) = %v, want %v", tc.current, tc.tag, got, tc.want)
		}
	}
}
