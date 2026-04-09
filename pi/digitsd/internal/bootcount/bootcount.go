package bootcount

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

const DefaultPath = "/boot/firmware/boot-counter"

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

func SetThreshold(path string, threshold int) error {
	return Write(path, threshold)
}
