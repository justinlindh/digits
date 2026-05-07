// Package devmode manages the dev-mode flag and associated toggle files
// on the Digits device data partition.
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

// Enabled reports whether the dev-mode flag file exists at path.
func Enabled(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Enable creates the dev-mode flag file at path, creating parent
// directories as needed.
func Enable(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte("1\n"), 0644)
}

// Disable removes the dev-mode flag file. No error if already absent.
func Disable(path string) error {
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// SkipFWReflash reports whether the firmware reflash skip flag is set.
func SkipFWReflash(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// SetSkipFWReflash creates or removes the firmware reflash skip flag.
func SetSkipFWReflash(path string, skip bool) error {
	if skip {
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

// SkipAutoUpdate reports whether the auto-update skip flag is set.
func SkipAutoUpdate(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// SetSkipAutoUpdate creates or removes the auto-update skip flag.
func SetSkipAutoUpdate(path string, skip bool) error {
	if skip {
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
