package updates

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeReleasesJSON(t *testing.T, dir string, idx *ReleaseIndex) {
	t.Helper()
	data, err := json.Marshal(idx)
	if err != nil {
		t.Fatalf("marshal releases.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "releases.json"), data, 0644); err != nil {
		t.Fatalf("write releases.json: %v", err)
	}
}

func sampleIndex(piLatest string) *ReleaseIndex {
	return &ReleaseIndex{
		Pi: ComponentIndex{
			Latest: piLatest,
			Releases: map[string]*Release{
				piLatest: {Version: piLatest, URL: "https://example.com/digitsd", Date: "2026-04-01"},
			},
		},
		Firmware: ComponentIndex{
			Latest: "0.5.0",
			Releases: map[string]*Release{
				"0.5.0": {Version: "0.5.0", URL: "https://example.com/firmware.elf", Date: "2026-04-01"},
			},
		},
	}
}

func TestReleaseIndex_FromDisk(t *testing.T) {
	dir := t.TempDir()
	writeReleasesJSON(t, dir, sampleIndex("1.0.0"))

	s := NewStore(dir)
	idx := s.ReleaseIndex()
	if idx == nil {
		t.Fatal("expected non-nil ReleaseIndex")
	}
	if idx.Pi.Latest != "1.0.0" {
		t.Errorf("Pi.Latest = %q, want 1.0.0", idx.Pi.Latest)
	}
	if idx.Firmware.Latest != "0.5.0" {
		t.Errorf("Firmware.Latest = %q, want 0.5.0", idx.Firmware.Latest)
	}
}

func TestReleaseIndex_Missing(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	if idx := s.ReleaseIndex(); idx != nil {
		t.Errorf("expected nil for missing releases.json, got %+v", idx)
	}
}

func TestReleaseIndex_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "releases.json"), []byte("not json{{{"), 0644)
	s := NewStore(dir)
	if idx := s.ReleaseIndex(); idx != nil {
		t.Errorf("expected nil for invalid JSON, got %+v", idx)
	}
}

func TestReleaseIndex_HotReload(t *testing.T) {
	dir := t.TempDir()
	writeReleasesJSON(t, dir, sampleIndex("1.0.0"))

	s := NewStore(dir)
	idx1 := s.ReleaseIndex()
	if idx1 == nil || idx1.Pi.Latest != "1.0.0" {
		t.Fatalf("first read: expected 1.0.0, got %v", idx1)
	}

	// Overwrite with newer version
	writeReleasesJSON(t, dir, sampleIndex("1.1.0"))

	idx2 := s.ReleaseIndex()
	if idx2 == nil || idx2.Pi.Latest != "1.1.0" {
		t.Fatalf("second read: expected 1.1.0 after hot-reload, got %v", idx2)
	}
}

func TestSortedReleases(t *testing.T) {
	idx := &ReleaseIndex{
		Pi: ComponentIndex{
			Latest: "1.2.0",
			Releases: map[string]*Release{
				"1.0.0": {Version: "1.0.0"},
				"1.2.0": {Version: "1.2.0"},
				"1.1.0": {Version: "1.1.0"},
			},
		},
	}
	releases := idx.SortedReleases("pi")
	if len(releases) != 3 {
		t.Fatalf("expected 3 releases, got %d", len(releases))
	}
	// Newest first
	want := []string{"1.2.0", "1.1.0", "1.0.0"}
	for i, r := range releases {
		if r.Version != want[i] {
			t.Errorf("releases[%d].Version = %q, want %q", i, r.Version, want[i])
		}
	}
}

func TestSortedReleases_UnknownComponent(t *testing.T) {
	idx := &ReleaseIndex{}
	if got := idx.SortedReleases("bogus"); got != nil {
		t.Errorf("expected nil for unknown component, got %v", got)
	}
}

func TestCompareSemver(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.2.0", "1.1.0", 1},
		{"1.1.0", "1.2.0", -1},
		{"1.2.0", "1.2.0", 0},
		{"2.0.0", "1.9.9", 1},
		{"10.0.0", "9.0.0", 1},  // multi-digit
		{"1.10.0", "1.9.0", 1},  // multi-digit minor
		{"0.5.0", "0.4.9", 1},
	}
	for _, c := range cases {
		got := compareSemver(c.a, c.b)
		if got != c.want {
			t.Errorf("compareSemver(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestStore_LatestManifest(t *testing.T) {
	dir := t.TempDir()
	manifest := `{"pi_version":"1.0.0","pi_commit":"abc123","firmware_version":"0.3.0","firmware_commit":"def456","pi_sha256":"aaa","firmware_sha256":"bbb"}`
	os.WriteFile(filepath.Join(dir, "latest.json"), []byte(manifest), 0644)

	s := NewStore(dir)
	m, err := s.Latest()
	if err != nil {
		t.Fatalf("Latest() error: %v", err)
	}
	if m.PiVersion != "1.0.0" {
		t.Errorf("PiVersion = %q, want 1.0.0", m.PiVersion)
	}
	if m.FirmwareVersion != "0.3.0" {
		t.Errorf("FirmwareVersion = %q, want 0.3.0", m.FirmwareVersion)
	}
	if m.PiSHA256 != "aaa" {
		t.Errorf("PiSHA256 = %q, want aaa", m.PiSHA256)
	}
}

func TestStore_LatestMissing(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	_, err := s.Latest()
	if err == nil {
		t.Fatal("expected error for missing latest.json")
	}
}
