package wififallback

import (
	"strings"
	"time"
)

// APController brings the setup-mode access point up and down and reports
// whether a client is associated with it.
type APController interface {
	Up() error
	Down() error
	HasClient() (bool, error)
}

type scriptAPController struct {
	run    func(args ...string) error
	runOut func(args ...string) ([]byte, error)
}

// NewScriptAPController returns an APController backed by
// /usr/local/bin/digits-ap-check for Up/Down and `iw dev wlan0 station dump`
// for HasClient.
func NewScriptAPController() APController {
	return &scriptAPController{
		run: func(args ...string) error {
			// digits-ap-check can take a while: stopping NetworkManager,
			// starting hostapd, dnsmasq, and the captive portal on a Pi Zero.
			_, err := runCmd(30*time.Second, args[0], args[1:]...)
			return err
		},
		runOut: func(args ...string) ([]byte, error) {
			return runCmd(5*time.Second, args[0], args[1:]...)
		},
	}
}

func (s *scriptAPController) Up() error {
	return s.run("/usr/local/bin/digits-ap-check", "up")
}

func (s *scriptAPController) Down() error {
	return s.run("/usr/local/bin/digits-ap-check", "down")
}

func (s *scriptAPController) HasClient() (bool, error) {
	out, err := s.runOut("iw", "dev", "wlan0", "station", "dump")
	if err != nil {
		return false, err
	}
	// iw's `station dump` output starts each station block with "Station <mac>"
	// at column 0. Anchor the check to line-start to avoid false positives from
	// any future iw field label that happens to contain "Station ".
	str := string(out)
	return strings.HasPrefix(str, "Station ") || strings.Contains(str, "\nStation "), nil
}
