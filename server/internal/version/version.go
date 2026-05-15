// Package version provides build-time version info for the digits server.
package version

// These are set at build time via -ldflags.
// Example: go build -ldflags "-X .../version.Version=1.2.3 -X .../version.Commit=abc123"
var (
	Version = "dev"
	Commit  = "unknown"
)

// test
// trigger
// v2
// v3
// v4
// v5
// v6
