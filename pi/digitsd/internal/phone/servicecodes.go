package phone

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/justinlindh/digits/pi/digitsd/internal/audio"
)

// ServiceCodeResult tells the caller whether a service code matched and
// whether the daemon is shutting down afterwards (terminal) or the user
// remains off-hook (non-terminal).
type ServiceCodeResult int

const (
	ServiceCodeNone        ServiceCodeResult = iota
	ServiceCodeTerminal                      // daemon going down
	ServiceCodeNonTerminal                   // user still off-hook
)

// ServiceCodeHandler processes hidden service codes entered via the keypad.
// Codes are detected from a rolling key buffer. Callers assign the OnXxx
// callbacks directly after construction; nil entries make the matching code
// a no-op (the code is still consumed and the buffer cleared).
//
// Fixed codes:
//
//	*#*#     → shutdown
//	*##*     → reboot
//	*#8378#  → *#TEST# — audio test (records clip, plays back)
//	*#73887# → *#SETUP# — Wi-Fi re-provisioning (removes /data/wifi-configured, reboots)
//	*#0*     → force re-pair
//	*#00000# → factory reset
//	*#873283# → *#UPDATE# — check for updates
//
// Volume codes: *#*N where N=0-9
type ServiceCodeHandler struct {
	OnVolume       func(level int)
	OnAudioTest    func()
	OnShutdown     func()
	OnReboot       func()
	OnSetup        func()
	OnRepair       func()
	OnFactoryReset func()
	OnUpdate       func()

	buffer string
}

// bufferMaxLen is the maximum number of keys kept in the rolling buffer.
// Must be >= the longest service code (currently *#873283# = 9 chars).
const bufferMaxLen = 9

// WifiConfiguredFlag is the sentinel file that indicates Wi-Fi has been
// provisioned. Its absence triggers AP mode on next boot.
const WifiConfiguredFlag = "/data/wifi-configured"

// NewServiceCodeHandler creates a new handler with no callbacks set.
func NewServiceCodeHandler() *ServiceCodeHandler {
	return &ServiceCodeHandler{}
}

// AddKey processes a keypress. Returns the kind of code that was triggered
// (or ServiceCodeNone if the key extended the buffer without matching anything).
func (h *ServiceCodeHandler) AddKey(key string) ServiceCodeResult {
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

// InCode reports whether the user is mid-entry of a service code. Every
// service code begins with the two-character prefix "*#", so requiring both
// chars (rather than just '*') keeps the suppression window tight: a lone
// '*' followed by digits stays in normal-dial mode, where easter eggs like
// "0000" still fire as expected. Callers use this to suppress competing key
// consumers that would otherwise eat keys belonging to the code.
func (h *ServiceCodeHandler) InCode() bool {
	return len(h.buffer) >= 2 && h.buffer[0] == '*' && h.buffer[1] == '#'
}

func (h *ServiceCodeHandler) check() ServiceCodeResult {
	// --- 9-character codes ---
	if len(h.buffer) >= 9 {
		last9 := h.buffer[len(h.buffer)-9:]
		if last9 == "*#873283#" {
			slog.Info("service code: *#873283# (*#UPDATE#) -> check for updates")
			if h.OnUpdate != nil {
				go h.OnUpdate()
			}
			h.buffer = ""
			return ServiceCodeTerminal
		}
	}

	// --- 8-character codes (checked before 4-char to avoid false positives) ---

	if len(h.buffer) >= 8 {
		last8 := h.buffer[len(h.buffer)-8:]
		switch last8 {
		case "*#00000#":
			slog.Info("service code: *#00000# -> factory reset")
			if h.OnFactoryReset != nil {
				go h.OnFactoryReset()
			}
			h.buffer = ""
			return ServiceCodeTerminal
		}
		if last8 == "*#73887#" {
			slog.Info("service code: *#73887# (*#SETUP#) -> Wi-Fi re-provisioning")
			if h.OnSetup != nil {
				go h.OnSetup()
			} else {
				slog.Info("service code: *#73887# triggered but no setup callback registered -- ignoring")
			}
			h.buffer = ""
			return ServiceCodeTerminal
		}
	}


	// --- 7-character codes ---

	if len(h.buffer) >= 7 {
		last7 := h.buffer[len(h.buffer)-7:]
		if last7 == "*#8378#" {
			slog.Info("service code: *#8378# (*#TEST#) -> audio test")
			if h.OnAudioTest != nil {
				go h.OnAudioTest()
			}
			h.buffer = ""
			return ServiceCodeNonTerminal
		}
	}

	// --- 4-character codes ---

	if len(h.buffer) < 4 {
		return ServiceCodeNone
	}
	last4 := h.buffer[len(h.buffer)-4:]

	switch last4 {
	case "*#0*":
		slog.Info("service code: *#0* -> force re-pair")
		if h.OnRepair != nil {
			go h.OnRepair()
		}
		h.buffer = ""
		return ServiceCodeTerminal
	case "*#*#":
		slog.Info("service code: *#*# -> shutdown")
		if h.OnShutdown != nil {
			go h.OnShutdown()
		}
		h.buffer = ""
		return ServiceCodeTerminal
	case "*##*":
		slog.Info("service code: *##* -> reboot")
		if h.OnReboot != nil {
			go h.OnReboot()
		}
		h.buffer = ""
		return ServiceCodeTerminal
	}

	// Volume codes: *#*N where N is 0-9
	if strings.HasPrefix(last4, "*#*") {
		ch := last4[3]
		if ch >= '0' && ch <= '9' {
			level := int(ch - '0')
			if h.OnVolume != nil {
				slog.Info("service code: volume", "level", level)
				h.OnVolume(level)
			}
			h.buffer = ""
			return ServiceCodeNonTerminal
		}
	}

	return ServiceCodeNone
}



const volumeFile = "/data/digits/volume"

// volumeToALSA converts a 0-9 volume level to ALSA value for the detected codec.
func volumeToALSA(level int) int {
	if level < 0 {
		level = 0
	}
	if level > 9 {
		level = 9
	}
	min, max := audio.CodecALSARange()
	return min + (level * (max - min) / 9)
}

// SetVolume sets volume on the detected codec. Level 0-9.
// Persists the user-facing level to /data/digits/volume; the rest of the
// mixer state (gain stages, routing) is the canonical embedded copy that
// digitsd renders to /data/digits_mixer.state at startup, so we no longer
// snapshot live mixer state on every volume change.
func SetVolume(level int) error {
	alsaVal := volumeToALSA(level)
	card := audio.CodecCardName()
	mixer := audio.CodecMixerName()
	cmd := exec.Command("amixer", "-c", card, "sset", mixer, fmt.Sprintf("%d", alsaVal))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("amixer: %s: %w", strings.TrimSpace(string(out)), err)
	}
	if err := os.MkdirAll(filepath.Dir(volumeFile), 0755); err != nil {
		slog.Warn("volume: mkdir failed", "error", err)
	}
	if err := os.WriteFile(volumeFile, []byte(fmt.Sprintf("%d\n", level)), 0644); err != nil {
		slog.Warn("volume: persist failed", "error", err)
	}
	slog.Info("volume set", "level", level, "max", 9, "mixer", mixer, "alsa", alsaVal, "persisted", true)
	return nil
}

// RestoreVolume loads the persisted volume level and applies it.
// If no persisted level exists, applies the codec-specific default returned
// by audio.CodecDefaultVolumeLevel(). The default is calibrated per codec
// because the same level step on the V2 TLV320AIC3104's PCM mixer produces
// a noticeably quieter handset output than on V1's DA7212 Lineout.
func RestoreVolume() {
	level := audio.CodecDefaultVolumeLevel()
	data, err := os.ReadFile(volumeFile)
	if err == nil {
		var v int
		if _, err := fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &v); err == nil && v >= 0 && v <= 9 {
			level = v
		}
	}
	alsaVal := volumeToALSA(level)
	card := audio.CodecCardName()
	mixer := audio.CodecMixerName()
	cmd := exec.Command("amixer", "-c", card, "sset", mixer, fmt.Sprintf("%d", alsaVal))
	if out, err := cmd.CombinedOutput(); err != nil {
		slog.Warn("volume restore: amixer failed", "output", strings.TrimSpace(string(out)), "error", err)
		return
	}
	slog.Info("volume restored", "level", level, "max", 9, "mixer", mixer, "alsa", alsaVal)
}
