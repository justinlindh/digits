package main

import "golang.org/x/mod/semver"

// hookFlashMinFirmware is the lowest Pico firmware version that emits HOOK:FLASH.
// Firmware prior to this does not implement flash detection, so the feature is
// not available on that device.
const hookFlashMinFirmware = "v1.5.0"

// hookFlashCapable reports whether the given firmware version string supports
// HOOK:FLASH emission. An empty or non-semver string returns false.
// The version string is expected in semver form without the leading "v"
// (e.g. "1.5.0"), matching what the existing VERSION handshake returns.
func hookFlashCapable(ver string) bool {
	if ver == "" {
		return false
	}
	sv := "v" + ver
	if !semver.IsValid(sv) {
		return false
	}
	return semver.Compare(sv, hookFlashMinFirmware) >= 0
}
