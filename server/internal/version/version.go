// Package version provides build-time version info for the digits server.
package version

import "fmt"

// These are set at build time via -ldflags.
// Example: go build -ldflags "-X .../version.Version=1.2.3 -X .../version.Commit=abc123"
var (
	Version = "dev"
	Commit  = "unknown"
)

// String returns a human-readable version string.
func String() string {
	return fmt.Sprintf("%s (%s)", Version, Commit)
}
