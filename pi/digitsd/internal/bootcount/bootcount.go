package bootcount

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

const DefaultPath = "/data/digits/boot-counter"

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
