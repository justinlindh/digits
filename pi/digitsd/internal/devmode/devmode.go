// Package devmode manages the dev-mode flag and associated toggle files
// on the Digits device data partition. Each flag is a flat file whose
// presence means "on". All functions are safe for concurrent use (they
// operate on independent filesystem paths).
package devmode

import (
	"os"
	"path/filepath"
)

// Default paths on the device data partition.
const (
	DefaultFlagPath           = "/data/digits/dev-mode"
	DefaultSkipFWReflashPath  = "/data/digits/skip-fw-reflash"
	DefaultSkipAutoUpdatePath = "/data/digits/skip-auto-update"
)

// FlagSet reports whether a flag file exists at path.
func FlagSet(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// SetFlag creates or removes a flag file. When set is true the file is
// created (parent dirs included); when false the file is removed. Removing
// a file that does not exist is not an error.
func SetFlag(path string, set bool) error {
	if set {
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

// Convenience aliases -- keep call sites readable and grep-friendly.

func Enabled(path string) bool                    { return FlagSet(path) }
func Enable(path string) error                    { return SetFlag(path, true) }
func Disable(path string) error                   { return SetFlag(path, false) }
func SkipFWReflash(path string) bool              { return FlagSet(path) }
func SetSkipFWReflash(path string, skip bool) error  { return SetFlag(path, skip) }
func SkipAutoUpdate(path string) bool             { return FlagSet(path) }
func SetSkipAutoUpdate(path string, skip bool) error { return SetFlag(path, skip) }
