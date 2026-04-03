package phone

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ServiceCodeHandler processes hidden service codes entered via the keypad.
// Codes are detected from a rolling key buffer.
//
// Fixed codes:
//
//	*#*#     → shutdown
//	*##*     → reboot
//	*#73887# → *#SETUP# — Wi-Fi re-provisioning (removes /data/wifi-configured, reboots)
//
// Volume codes: *#*N where N=0-9
//
//	*#*8 → audio test (special)
type ServiceCodeHandler struct {
	buffer      string
	onVolume    func(level int)
	onAudioTest func()
	onShutdown  func()
	onReboot    func()
	onSetup        func()
	onRepair       func()
	onFactoryReset func()
	onUpdate       func()
}

// bufferMaxLen is the maximum number of keys kept in the rolling buffer.
// Must be >= the longest service code (currently *#873283# = 9 chars).
const bufferMaxLen = 9

// NewServiceCodeHandler creates a new handler with no callbacks set.
func NewServiceCodeHandler() *ServiceCodeHandler {
	return &ServiceCodeHandler{}
}

// SetVolumeCallback sets the function called when a volume code (*#*N) is entered.
func (h *ServiceCodeHandler) SetVolumeCallback(fn func(level int)) {
	h.onVolume = fn
}

// SetAudioTestCallback sets the function called for *#*8.
func (h *ServiceCodeHandler) SetAudioTestCallback(fn func()) {
	h.onAudioTest = fn
}

// SetShutdownCallback sets the function called for *#*# (shutdown).
func (h *ServiceCodeHandler) SetShutdownCallback(fn func()) {
	h.onShutdown = fn
}

// SetRebootCallback sets the function called for *##* (reboot).
func (h *ServiceCodeHandler) SetRebootCallback(fn func()) {
	h.onReboot = fn
}

// WifiConfiguredFlag is the sentinel file that indicates Wi-Fi has been
// provisioned. Its absence triggers AP mode on next boot.
const WifiConfiguredFlag = "/data/wifi-configured"

// SetSetupCallback sets the function called for *#73887# (*#SETUP#).
func (h *ServiceCodeHandler) SetSetupCallback(fn func()) {
	h.onSetup = fn
}

// SetRepairCallback sets the function called for *#0* (force re-pairing).
func (h *ServiceCodeHandler) SetRepairCallback(fn func()) {
	h.onRepair = fn
}

// SetFactoryResetCallback sets the function called for *#00000# (factory reset).
func (h *ServiceCodeHandler) SetFactoryResetCallback(fn func()) {
	h.onFactoryReset = fn
}

// SetUpdateCallback sets the function called for *#873283# (*#UPDATE#).
func (h *ServiceCodeHandler) SetUpdateCallback(fn func()) {
	h.onUpdate = fn
}

// AddKey processes a keypress. Returns true if a service code was triggered.
func (h *ServiceCodeHandler) AddKey(key string) bool {
	h.buffer += key
	if len(h.buffer) > bufferMaxLen {
		h.buffer = h.buffer[len(h.buffer)-bufferMaxLen:]
	}
	return h.check()
}

// Reset clears the key buffer (e.g., on hang-up).
func (h *ServiceCodeHandler) Reset() {
	h.buffer = ""
}

func (h *ServiceCodeHandler) check() bool {
	// --- 9-character codes ---
	if len(h.buffer) >= 9 {
		last9 := h.buffer[len(h.buffer)-9:]
		if last9 == "*#873283#" {
			log.Println("service code: *#873283# (*#UPDATE#) → check for updates")
			if h.onUpdate != nil {
				go h.onUpdate()
			}
			h.buffer = ""
			return true
		}
	}

	// --- 8-character codes (checked before 4-char to avoid false positives) ---

	if len(h.buffer) >= 8 {
		last8 := h.buffer[len(h.buffer)-8:]
		switch last8 {
		case "*#00000#":
			log.Println("service code: *#00000# → factory reset")
			if h.onFactoryReset != nil {
				go h.onFactoryReset()
			}
			h.buffer = ""
			return true
		}
		if last8 == "*#73887#" {
			log.Println("service code: *#73887# (*#SETUP#) → Wi-Fi re-provisioning")
			if h.onSetup != nil {
				go h.onSetup()
			} else {
				log.Println("service code: *#73887# triggered but no setup callback registered — ignoring")
			}
			h.buffer = ""
			return true
		}
	}


	// --- 4-character codes ---

	if len(h.buffer) < 4 {
		return false
	}
	last4 := h.buffer[len(h.buffer)-4:]

	switch last4 {
	case "*#0*":
		log.Println("service code: *#0* → force re-pair")
		if h.onRepair != nil {
			go h.onRepair()
		}
		h.buffer = ""
		return true
	case "*#*#":
		log.Println("service code: *#*# → shutdown")
		if h.onShutdown != nil {
			go h.onShutdown()
		}
		h.buffer = ""
		return true
	case "*##*":
		log.Println("service code: *##* → reboot")
		if h.onReboot != nil {
			go h.onReboot()
		}
		h.buffer = ""
		return true
	}

	// Volume codes: *#*N where N is 0-9
	if strings.HasPrefix(last4, "*#*") {
		ch := last4[3]
		if ch >= '0' && ch <= '9' {
			level := int(ch - '0')
			if level == 8 && h.onAudioTest != nil {
				log.Println("service code: *#*8 → audio test")
				go h.onAudioTest()
				h.buffer = ""
				return true
			}
			if h.onVolume != nil {
				log.Printf("service code: *#*%d → volume %d", level, level)
				h.onVolume(level)
			}
			h.buffer = ""
			return true
		}
	}

	return false
}



const (
	volumeFile     = "/data/digits/volume"
	mixerStateFile = "/data/digits_mixer.state"
	defaultVolume  = 5
)

// volumeToALSA converts a 0-9 volume level to ALSA Lineout value (20-58).
func volumeToALSA(level int) int {
	if level < 0 {
		level = 0
	}
	if level > 9 {
		level = 9
	}
	return 20 + (level * (58 - 20) / 9)
}

// SetVolume sets Lineout volume. Level 0-9 maps to ALSA 20-58.
// Persists the level to /data/digits/volume and saves full mixer state.
func SetVolume(level int) error {
	alsaVal := volumeToALSA(level)
	cmd := exec.Command("amixer", "-c", "1", "sset", "Lineout", fmt.Sprintf("%d", alsaVal))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("amixer: %s: %w", strings.TrimSpace(string(out)), err)
	}
	// Persist volume level
	os.MkdirAll(filepath.Dir(volumeFile), 0755)
	if err := os.WriteFile(volumeFile, []byte(fmt.Sprintf("%d\n", level)), 0644); err != nil {
		log.Printf("volume: persist failed: %v", err)
	}
	// Save full mixer state
	cmd = exec.Command("sudo", "alsactl", "store", "1", "-f", mixerStateFile)
	cmd.Run() // best-effort
	log.Printf("volume: %d/9 (Lineout=%d, persisted)", level, alsaVal)
	return nil
}

// RestoreVolume loads the persisted volume level and applies it.
// If no persisted level exists, applies defaultVolume (5).
func RestoreVolume() {
	level := defaultVolume
	data, err := os.ReadFile(volumeFile)
	if err == nil {
		var v int
		if _, err := fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &v); err == nil && v >= 0 && v <= 9 {
			level = v
		}
	}
	alsaVal := volumeToALSA(level)
	cmd := exec.Command("amixer", "-c", "1", "sset", "Lineout", fmt.Sprintf("%d", alsaVal))
	if out, err := cmd.CombinedOutput(); err != nil {
		log.Printf("volume restore: amixer: %s: %v", strings.TrimSpace(string(out)), err)
		return
	}
	log.Printf("volume restored: %d/9 (Lineout=%d)", level, alsaVal)
}
