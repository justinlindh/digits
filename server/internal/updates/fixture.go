package updates

// FakeReleaseIndex returns a GitHubReleases instance pre-populated with a
// static release index for e2e testing. Firmware has three releases (1.2.0,
// 1.3.0 groomed, 1.4.0 groomed/latest) and pi has three releases (0.5.0,
// 0.6.0 groomed, 0.7.0 groomed/latest). Callers that seed a device at fw
// 1.2.0 or pi 0.5.0 will see the update chip with notes for the newer ones.
func FakeReleaseIndex() *GitHubReleases {
	idx := &ReleaseIndex{
		Firmware: ComponentIndex{
			Latest: "1.4.0",
			Releases: map[string]*Release{
				"1.2.0": {
					Version: "1.2.0",
					Date:    "2025-01-01",
					Notes:   "",
				},
				"1.3.0": {
					Version: "1.3.0",
					Date:    "2025-06-01",
					Notes:   "Improved audio codec stability.\nReduced echo on long calls.",
				},
				"1.4.0": {
					Version: "1.4.0",
					Date:    "2026-01-15",
					Notes:   "Added silent-mode support.\nFixed pairing timeout on slow networks.",
				},
			},
		},
		Pi: ComponentIndex{
			Latest: "0.7.0",
			Releases: map[string]*Release{
				"0.5.0": {
					Version: "0.5.0",
					Date:    "2025-09-01",
					Notes:   "",
				},
				"0.6.0": {
					Version: "0.6.0",
					Date:    "2026-01-10",
					Notes:   "Faster boot. New OTA progress reporting.",
				},
				"0.7.0": {
					Version: "0.7.0",
					Date:    "2026-04-01",
					Notes:   "V2 mixer state shipped via OTA.\nHP DAC volume baseline raised.",
				},
			},
		},
	}
	return NewGitHubReleasesWithIndex(idx)
}
