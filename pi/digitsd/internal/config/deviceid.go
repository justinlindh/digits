package config

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const deviceIDPath = "/data/digits/device-id"

// LoadOrCreateDeviceID reads the device ID from /data/digits/device-id.
// If the file doesn't exist or contains a non-UUID value (legacy 4-char hex),
// generates a new UUID v4 and persists it.
func LoadOrCreateDeviceID() (string, error) {
	return loadOrCreateDeviceIDAt(deviceIDPath)
}

func loadOrCreateDeviceIDAt(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		id := strings.Trim(string(data), " \t\r\n\x00")
		if isUUIDv4Shape(id) {
			return id, nil
		}
	}

	id, err := generateUUIDv4()
	if err != nil {
		return "", fmt.Errorf("generate device id: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	// Unlink first so we can replace a file owned by another user
	// (legacy images provisioned /data/digits/device-id as root while
	// digitsd runs as the digits user) or a file without write perms.
	// ENOENT is fine: os.WriteFile will create it below.
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("remove stale device id: %w", err)
	}
	if err := os.WriteFile(path, []byte(id+"\n"), 0644); err != nil {
		return "", fmt.Errorf("persist device id: %w", err)
	}

	return id, nil
}

func isUUIDv4Shape(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		case 14:
			if c != '4' {
				return false
			}
		default:
			if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
				return false
			}
		}
	}
	return true
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
