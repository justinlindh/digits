package config

import (
	"crypto/rand"
	"fmt"
	"os"
	"strings"
)

const deviceIDPath = "/data/digits/device-id"

// LoadOrCreateDeviceID reads the device ID from /data/digits/device-id.
// If the file doesn't exist or contains a non-UUID value (legacy 4-char hex),
// generates a new UUID v4 and persists it.
func LoadOrCreateDeviceID() (string, error) {
	data, err := os.ReadFile(deviceIDPath)
	if err == nil {
		id := strings.TrimSpace(string(data))
		if len(id) == 36 {
			return id, nil
		}
	}

	id, err := generateUUIDv4()
	if err != nil {
		return "", fmt.Errorf("generate device id: %w", err)
	}

	if err := os.MkdirAll("/data/digits", 0755); err != nil {
		return "", fmt.Errorf("mkdir /data/digits: %w", err)
	}
	if err := os.WriteFile(deviceIDPath, []byte(id+"\n"), 0644); err != nil {
		return "", fmt.Errorf("persist device id: %w", err)
	}

	return id, nil
}

func generateUUIDv4() (string, error) {
	var uuid [16]byte
	if _, err := rand.Read(uuid[:]); err != nil {
		return "", err
	}
	uuid[6] = (uuid[6] & 0x0f) | 0x40
	uuid[8] = (uuid[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		uuid[0:4], uuid[4:6], uuid[6:8], uuid[8:10], uuid[10:16]), nil
}
