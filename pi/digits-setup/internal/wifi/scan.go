package wifi

import (
	"bufio"
	"bytes"
	"os/exec"
	"strconv"
	"strings"
)

// Network represents a discovered Wi-Fi network.
type Network struct {
	SSID   string `json:"ssid"`
	Signal int    `json:"signal"` // dBm
}

// Scanner discovers nearby Wi-Fi networks.
type Scanner interface {
	Scan() ([]Network, error)
}

// SystemScanner uses `iw dev wlan0 scan` on the host.
type SystemScanner struct{}

func (s *SystemScanner) Scan() ([]Network, error) {
	cmd := exec.Command("iw", "dev", "wlan0", "scan", "-u")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return parseIWScan(out), nil
}

// parseIWScan parses the output of `iw dev wlan0 scan`.
func parseIWScan(data []byte) []Network {
	var networks []Network
	var current *Network
	seen := make(map[string]bool)

	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if strings.HasPrefix(line, "BSS ") {
			// New BSS entry — save previous if valid
			if current != nil && current.SSID != "" && !seen[current.SSID] {
				seen[current.SSID] = true
				networks = append(networks, *current)
			}
			current = &Network{}
		}

		if current == nil {
			continue
		}

		if strings.HasPrefix(line, "signal:") {
			// e.g. "signal: -45.00 dBm"
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				if f, err := strconv.ParseFloat(parts[1], 64); err == nil {
					current.Signal = int(f)
				}
			}
		}

		if strings.HasPrefix(line, "SSID:") {
			ssid := strings.TrimPrefix(line, "SSID:")
			current.SSID = strings.TrimSpace(ssid)
		}
	}

	// Don't forget the last entry
	if current != nil && current.SSID != "" && !seen[current.SSID] {
		networks = append(networks, *current)
	}

	return networks
}
