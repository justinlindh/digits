package version

import "testing"

func TestDefaults(t *testing.T) {
	if Version == "" {
		t.Error("Version must have a non-empty default")
	}
	if Commit == "" {
		t.Error("Commit must have a non-empty default")
	}
}
