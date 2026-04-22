package updates

// FakeReleaseIndex returns a GitHubReleases instance pre-populated with a
// static release index for e2e testing. The firmware component has three
// releases: 1.2.0, 1.3.0 (groomed), and 1.4.0 (groomed, latest). The pi
// component is empty. Callers that seed a device at fw 1.2.0 will see the
// update chip with notes for 1.3.0 and 1.4.0.
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
			Latest:   "",
			Releases: make(map[string]*Release),
		},
	}
	return NewGitHubReleasesWithIndex(idx)
}
