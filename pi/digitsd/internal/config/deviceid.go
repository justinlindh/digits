package config

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const deviceIDPath = "/data/digits/device-id"

// LoadOrCreateDeviceID reads the device ID from /data/digits/device-id,
// validating that it has UUID v4 shape. If the file is missing, unreadable,
// or contains anything that isn't a UUID v4 (legacy short ids, NUL-filled
// post-crash artifacts, stray text), it generates a fresh UUID v4 and
// atomically writes it in place.
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

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", dir, err)
	}
	// atomicWrite fsyncs the temp file and the parent directory before and
	// after the rename, so a power cut mid-write cannot leave a device-id
	// with un-flushed (NUL-filled) contents that the load path would reject
	// and regenerate, silently changing the device identity. The temp+rename
	// also lets us replace a file owned by another user: rename needs only
	// write permission on the parent directory. Legacy images provisioned
	// /data/digits/device-id as root while digitsd runs as the digits user,
	// so a plain in-place write would hit EACCES.
	if err := atomicWrite(path, []byte(id+"\n"), 0644); err != nil {
		return "", fmt.Errorf("write device id: %w", err)
	}

	return id, nil
}

func isUUIDv4Shape(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := 0; i < 36; i++ {
		c := s[i]
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		case 14:
			if c != '4' {
				return false
			}
		case 19:
			// UUID v4 variant: high bits are 10, so the nibble is 8..b.
			if c != '8' && c != '9' && c != 'a' && c != 'b' && c != 'A' && c != 'B' {
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
