// Package version holds build-time variables injected via -ldflags.
package version

// Version, GitCommit and BuildDate are set at build time:
//
//	-ldflags "-X hopscotch/internal/version.Version=v1.2.3 ..."
var (
	Version   = "dev"
	GitCommit = "unknown"
	BuildDate = "unknown"
)

// String returns a human-readable version string.
func String() string {
	return Version + " (commit: " + GitCommit + ", built: " + BuildDate + ")"
}
