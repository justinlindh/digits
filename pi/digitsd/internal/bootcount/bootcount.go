package bootcount

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

const DefaultPath = "/data/digits/boot-counter"

// AutoFactoryResetFlag signals the recovery server to skip its
// Try Again / Factory Reset menu and run Factory Reset directly.
// Written by digitsd's confirmed *#00000# path before reboot.
// Cleared automatically when /data is reformatted during the wipe.
const AutoFactoryResetFlag = "/data/digits/auto-factory-reset"

func Read(path string) (int, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("parse boot counter: %w", err)
	}
	return n, nil
}

func Write(path string, count int) error {
	// Remove first in case the file is owned by root (created by initramfs).
	// The parent directory is owned by the digits user, so remove succeeds.
	_ = os.Remove(path)
	return os.WriteFile(path, []byte(strconv.Itoa(count)), 0644)
}

func Clear(path string) error {
	return Write(path, 0)
}

// SetThreshold writes the threshold value directly to the boot counter file,
// which causes the initramfs boot-check to immediately enter recovery mode
// on the next reboot (since count >= threshold).
func SetThreshold(path string, threshold int) error {
	return Write(path, threshold)
}
