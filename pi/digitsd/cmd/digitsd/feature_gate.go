package main

import (
	"strings"

	"golang.org/x/mod/semver"
)

// hookFlashMinFirmware is the lowest Pico firmware version that emits HOOK:FLASH.
// Firmware prior to this does not implement flash detection, so the feature is
// not available on that device.
const hookFlashMinFirmware = "v1.5.0"

// hookFlashCapable reports whether the given firmware version string supports
// HOOK:FLASH emission. An empty or non-semver string returns false.
// The version string is expected in semver form without the leading "v"
// (e.g. "1.5.0" or "1.5.0-57-g1c86d2f-dirty" for an in-tree dev build), matching
// what the existing VERSION handshake returns.
//
// Pre-release / build-metadata suffixes are stripped before comparison so that
// dev builds off a commit past the feature-introduction tag are treated as
// capable, not as pre-releases of that tag (semver 2.0 orders "1.5.0-x" before
// "1.5.0", which is the opposite of what we want for feature gating).
func hookFlashCapable(ver string) bool {
	if ver == "" {
		return false
	}
	base := ver
	if i := strings.IndexAny(base, "-+"); i >= 0 {
		base = base[:i]
	}
	sv := "v" + base
	if !semver.IsValid(sv) {
		return false
	}
	return semver.Compare(sv, hookFlashMinFirmware) >= 0
}
