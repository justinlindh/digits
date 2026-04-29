package assets

import (
	"io/fs"
	"strings"
	"testing"
)

// TestEmbedHasMixerStateVariants ensures the per-PCB-variant mixer state files
// are present in the embedded FS at the paths the digitsd mixer-render step
// reads from. Skipped when `make embed` has not populated the embed tree
// (committed `.gitkeep` is the only thing left at rest, so a presence-of-rootfs
// probe is the right "embed was populated" signal).
func TestEmbedHasMixerStateVariants(t *testing.T) {
	embedded := SubFS()
	if _, err := fs.Stat(embedded, "rootfs"); err != nil {
		t.Skip("embed/ not populated; run `make embed`")
	}
	want := []string{"mixer/v1.state", "mixer/v2.state"}
	for _, p := range want {
		data, err := fs.ReadFile(embedded, p)
		if err != nil {
			t.Errorf("missing %s: %v", p, err)
			continue
		}
		if !strings.Contains(string(data), "state.") {
			t.Errorf("%s does not look like an alsactl state file (no 'state.' header)", p)
		}
	}
}

// TestEmbedMixerStatesAreNotAutoExtracted guards the placement under embed/mixer/
// rather than embed/data/. The Extractor walks rootfs/ and data/ only, so
// mixer/ files stay in the binary but are not written to /data on extract.
// Moving them under embed/data/ would create duplicate copies on disk that
// drift from the canonical render target.
func TestEmbedMixerStatesAreNotAutoExtracted(t *testing.T) {
	embedded := SubFS()
	for _, prefix := range []string{"data", "rootfs"} {
		err := fs.WalkDir(embedded, prefix, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				return nil
			}
			if strings.Contains(path, "mixer_v") || strings.HasSuffix(path, ".state") {
				t.Errorf("mixer state file %q is under auto-extracted prefix %q; should be under embed/mixer/", path, prefix)
			}
			return nil
		})
		if err != nil {
			t.Logf("walk %s: %v", prefix, err)
		}
	}
}
