package updates

// FakeReleaseIndex returns a GitHubReleases instance pre-populated with a
// static release index for e2e testing. Each component has three releases so
// the changelog tabs, adoption counts, and update dot can all be exercised.
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
					Version:  "1.3.0",
					Date:     "2025-06-01",
					Notes:    "Improved audio codec stability.\nReduced echo on long calls.",
					AudioURL: "https://github.com/justinlindh/digits/releases/download/fw/v1.12.0/release-notes-fw-v1.12.0.mp3",
				},
				"1.4.0": {
					Version:  "1.4.0",
					Date:     "2026-01-15",
					Notes:    "Added silent-mode support.\nFixed pairing timeout on slow networks.",
					AudioURL: "https://github.com/justinlindh/digits/releases/download/fw/v1.12.2/release-notes-fw-v1.12.2.mp3",
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
					Version:  "0.6.0",
					Date:     "2026-01-10",
					Notes:    "Faster boot. New OTA progress reporting.",
					AudioURL: "https://github.com/justinlindh/digits/releases/download/pi/v1.20.0/release-notes-pi-v1.20.0.mp3",
				},
				"0.7.0": {
					Version:  "0.7.0",
					Date:     "2026-04-01",
					Notes:    "V2 mixer state shipped via OTA.\nHP DAC volume baseline raised.",
					AudioURL: "https://github.com/justinlindh/digits/releases/download/pi/v1.21.0/release-notes-pi-v1.21.0.mp3",
				},
			},
		},
		Server: ComponentIndex{
			Latest: "1.68.0",
			Releases: map[string]*Release{
				"1.66.0": {
					Version: "1.66.0",
					Date:    "2026-03-20",
					Notes:   "Household invites now span multiple households.",
				},
				"1.67.0": {
					Version:  "1.67.0",
					Date:     "2026-04-10",
					Notes:    "Redis pub/sub for multi-replica signaling.\nUnpaired phones no longer show as online.",
					AudioURL: "https://github.com/justinlindh/digits/releases/download/server/v1.67.0/release-notes-v1.67.0.mp3",
				},
				"1.68.0": {
					Version:  "1.68.0",
					Date:     "2026-05-05",
					Notes:    "What's New changelog modal.\nPhone LEDs breathe during calls.",
					AudioURL: "https://github.com/justinlindh/digits/releases/download/server/v1.68.0/release-notes-v1.68.0.mp3",
				},
			},
		},
	}
	return NewGitHubReleasesWithIndex(idx)
}
