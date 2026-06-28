// Package devmode manages the dev-mode flag and associated toggle files
// on the Digits device data partition. Each flag is a flat file whose
// presence means "on". All functions are safe for concurrent use (they
// operate on independent filesystem paths).
package devmode

import (
	"os"
	"path/filepath"
)

// Default paths on the device data partition. The path identifies the flag;
// IsSet and Set work on any of them.
const (
	DefaultFlagPath           = "/data/digits/dev-mode"
	DefaultSkipFWReflashPath  = "/data/digits/skip-fw-reflash"
	DefaultSkipAutoUpdatePath = "/data/digits/skip-auto-update"
)

// IsSet reports whether the flag file at path exists.
func IsSet(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Set creates or removes the flag file at path. When on is true the file is
// created (parent dirs included); when false it is removed. Removing a file
// that does not exist is not an error.
func Set(path string, on bool) error {
	if on {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return err
		}
		return os.WriteFile(path, []byte("1\n"), 0644)
	}
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
