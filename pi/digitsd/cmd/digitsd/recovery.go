package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/justinlindh/digits/pi/digitsd/internal/audio"
	"github.com/justinlindh/digits/pi/digitsd/internal/bootcount"
	"github.com/justinlindh/digits/pi/digitsd/internal/phone"
)

//go:embed recovery_static
var recoveryStaticFS embed.FS

// recoveryState tracks whether a factory reset is currently running,
// preventing duplicate triggers from the web UI and voice menu.
type recoveryState struct {
	mu       sync.Mutex
	reseting bool
}

func (rs *recoveryState) startReset() bool {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if rs.reseting {
		return false
	}
	rs.reseting = true
	return true
}

func runRecoveryMode() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)
	slog.Info("digitsd: entering recovery mode")

	// Open serial port with retry.
	var sp *phone.SerialPort
	for attempt := 1; attempt <= 10; attempt++ {
		var err error
		sp, err = phone.OpenSerial(*serialDev, 115200, logger)
		if err != nil {
			slog.Warn("serial: open failed, retrying", "attempt", attempt, "error", err)
			time.Sleep(500 * time.Millisecond)
			continue
		}
		if err = sp.Ping(); err != nil {
			slog.Warn("serial: ping failed, retrying", "attempt", attempt, "error", err)
			_ = sp.Close()
			sp = nil
			time.Sleep(500 * time.Millisecond)
			continue
		}
		break
	}
	if sp == nil {
		log.Fatal("serial: failed to open and ping after 10 attempts")
	}
	slog.Info("serial: connected")

	// Set LED state for recovery mode.
	sp.LED("LOCK")
	time.Sleep(50 * time.Millisecond)
	sp.LED("HEARTBEAT")

	// Initialize ALSA playback.
	pbCfg := audio.DefaultPlaybackConfig()
	if *alsaDevice != "" {
		pbCfg.Device = *alsaDevice
	}
	pb, err := audio.NewPlayback(pbCfg)
	if err != nil {
		// Fallback: try the raw hardware device directly.
		slog.Warn("playback: default device failed, trying hw:CARD=digitscodec,DEV=0", "error", err)
		pbCfg.Device = "hw:CARD=digitscodec,DEV=0"
		pb, err = audio.NewPlayback(pbCfg)
		if err != nil {
			log.Fatalf("playback: cannot open any device: %v", err)
		}
	}
	slog.Info("playback: opened", "device", pbCfg.Device)

	// Create mixer and load tones.
	mixer := audio.NewMixer(pb)
	if err := mixer.LoadTonesFromDir(*toneDir); err != nil {
		slog.Warn("mixer: failed to load tones (voice menu will be silent)", "dir", *toneDir, "error", err)
	}
	mixer.Start()
	defer mixer.Stop()

	state := &recoveryState{}

	// Start recovery web UI.
	go serveRecoveryWeb(state, mixer, sp)

	// Run voice menu event loop.
	recoveryEventLoop(sp, mixer, state)
}

// serveRecoveryWeb starts the recovery HTTP server on :80.
func serveRecoveryWeb(state *recoveryState, mixer *audio.Mixer, sp *phone.SerialPort) {
	staticFS, err := fs.Sub(recoveryStaticFS, "recovery_static")
	if err != nil {
		log.Fatalf("recovery web: embed sub: %v", err)
	}

	mux := http.NewServeMux()

	mux.Handle("/", http.FileServer(http.FS(staticFS)))

	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		count, _ := bootcount.Read(bootcount.DefaultPath)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]int{"boot_count": count}) //nolint:errcheck
	})

	mux.HandleFunc("/try-again", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		slog.Info("recovery web: try-again requested")
		_ = bootcount.Clear(bootcount.DefaultPath)
		w.WriteHeader(http.StatusOK)
		go doReboot()
	})

	mux.HandleFunc("/factory-reset", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !state.startReset() {
			http.Error(w, "factory reset already in progress", http.StatusConflict)
			return
		}
		slog.Info("recovery web: factory-reset requested")
		w.WriteHeader(http.StatusOK)
		go doRecoveryFactoryReset(mixer)
	})

	slog.Info("recovery web: listening on :80")
	if err := http.ListenAndServe(":80", mux); err != nil {
		slog.Error("recovery web: listen failed", "error", err)
	}
}

// recoveryEventLoop runs the voice menu on the phone handset.
func recoveryEventLoop(sp *phone.SerialPort, mixer *audio.Mixer, state *recoveryState) {
	events := sp.Events()
	for {
		// Wait for off-hook.
		ev := <-events
		if ev != "HOOK:OFF" {
			continue
		}
		slog.Info("recovery: handset off-hook, playing menu")
		recoveryHandsetSession(sp, mixer, state, events)
	}
}

// recoveryHandsetSession handles one off-hook session: plays menu, waits for input.
func recoveryHandsetSession(sp *phone.SerialPort, mixer *audio.Mixer, state *recoveryState, events <-chan string) {
	for {
		mixer.PlayOnce("recovery_menu")

		// Wait for the menu audio to finish, a key press, or on-hook.
		key, hungUp := waitForKeyOrHangup(events, 30*time.Second)
		if hungUp {
			mixer.StopAll()
			slog.Info("recovery: handset on-hook")
			return
		}

		switch key {
		case "1":
			slog.Info("recovery: user pressed 1, restarting")
			mixer.PlayOnce("restarting")
			waitForOnceComplete(mixer, 5*time.Second)
			_ = bootcount.Clear(bootcount.DefaultPath)
			doReboot()
			return

		case "2":
			slog.Info("recovery: user pressed 2, confirming factory reset")
			mixer.PlayOnce("confirm_factory_reset")
			// Wait for confirmation (KEY:2 again) or bail.
			confirm, hungUp := waitForKeyOrHangup(events, 15*time.Second)
			if hungUp {
				mixer.StopAll()
				return
			}
			if confirm != "2" {
				slog.Info("recovery: factory reset not confirmed, replaying menu")
				continue
			}
			if !state.startReset() {
				slog.Warn("recovery: factory reset already in progress")
				return
			}
			doRecoveryFactoryReset(mixer)
			return

		case "":
			// Timeout: replay menu.
			slog.Info("recovery: menu timeout, replaying")
			continue

		default:
			slog.Info("recovery: unknown key, replaying menu", "key", key)
			continue
		}
	}
}

// waitForKeyOrHangup waits for a KEY event, HOOK:ON, or timeout.
// Returns the key digit (e.g. "1") and whether the user hung up.
// On timeout, returns ("", false).
func waitForKeyOrHangup(events <-chan string, timeout time.Duration) (string, bool) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case ev := <-events:
			if ev == "HOOK:ON" {
				return "", true
			}
			if len(ev) > 4 && ev[:4] == "KEY:" {
				return ev[4:], false
			}
			// Ignore other events (DIAL:, FSM:, etc.)
		case <-timer.C:
			return "", false
		}
	}
}

// waitForOnceComplete waits up to timeout for all one-shot tones to finish.
func waitForOnceComplete(mixer *audio.Mixer, timeout time.Duration) {
	deadline := time.After(timeout)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-deadline:
			return
		case <-ticker.C:
			if !mixer.OncePlaying() {
				return
			}
		}
	}
}

// doRecoveryFactoryReset performs the full factory reset sequence.
func doRecoveryFactoryReset(mixer *audio.Mixer) {
	slog.Info("recovery: starting factory reset")
	mixer.PlayOnce("factory_reset_in_progress")
	waitForOnceComplete(mixer, 10*time.Second)

	// Restore root filesystem.
	mixer.PlayOnce("restoring_system")
	waitForOnceComplete(mixer, 10*time.Second)

	rootfsImg := "/rootfs.img.zst"
	if _, err := os.Stat(rootfsImg); err != nil {
		slog.Error("recovery: rootfs.img.zst not found", "path", rootfsImg, "error", err)
		mixer.PlayOnce("error_tone")
		return
	}

	slog.Info("recovery: decompressing rootfs to /dev/mmcblk0p2")
	cmd := exec.Command("sh", "-c", fmt.Sprintf("zstd -d -c %s | dd of=/dev/mmcblk0p2 bs=4M conv=fsync", rootfsImg))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		slog.Error("recovery: rootfs restore failed", "error", err)
		mixer.PlayOnce("error_tone")
		return
	}
	slog.Info("recovery: rootfs restored")

	// Format /data partition.
	mixer.PlayOnce("formatting_data")
	waitForOnceComplete(mixer, 10*time.Second)

	slog.Info("recovery: unmounting /data")
	_ = syscall.Unmount("/data", 0)

	slog.Info("recovery: formatting /dev/mmcblk0p4")
	mkfs := exec.Command("mkfs.ext4", "-L", "data", "-F", "/dev/mmcblk0p4")
	mkfs.Stdout = os.Stdout
	mkfs.Stderr = os.Stderr
	if err := mkfs.Run(); err != nil {
		slog.Error("recovery: mkfs.ext4 failed", "error", err)
		mixer.PlayOnce("error_tone")
		return
	}

	// Remount /data and extract skeleton.
	slog.Info("recovery: mounting /data")
	if err := syscall.Mount("/dev/mmcblk0p4", "/data", "ext4", 0, ""); err != nil {
		slog.Error("recovery: mount /data failed", "error", err)
		mixer.PlayOnce("error_tone")
		return
	}

	skeletonPath := "/data-skeleton.tar.zst"
	if _, err := os.Stat(skeletonPath); err == nil {
		slog.Info("recovery: extracting data skeleton")
		tar := exec.Command("sh", "-c", fmt.Sprintf("zstd -d -c %s | tar -xf - -C /data", skeletonPath))
		tar.Stdout = os.Stdout
		tar.Stderr = os.Stderr
		if err := tar.Run(); err != nil {
			slog.Error("recovery: data skeleton extract failed", "error", err)
			// Non-fatal: continue to reboot.
		}
	} else {
		slog.Info("recovery: no data skeleton found, skipping", "path", skeletonPath)
	}

	slog.Info("recovery: factory reset complete")
	mixer.PlayOnce("reset_complete")
	waitForOnceComplete(mixer, 10*time.Second)

	doReboot()
}

// doReboot reboots the system. If running as PID 1 (init in initramfs),
// uses the raw reboot syscall. Otherwise uses systemctl.
func doReboot() {
	slog.Info("recovery: rebooting")
	time.Sleep(500 * time.Millisecond) // let final log flush

	if os.Getpid() == 1 {
		syscall.Sync()
		_ = syscall.Reboot(syscall.LINUX_REBOOT_CMD_RESTART)
	} else {
		cmd := exec.Command("systemctl", "reboot")
		if err := cmd.Run(); err != nil {
			// Last resort: raw reboot.
			slog.Warn("recovery: systemctl reboot failed, trying raw syscall", "error", err)
			syscall.Sync()
			_ = syscall.Reboot(syscall.LINUX_REBOOT_CMD_RESTART)
		}
	}
	// Block forever if reboot doesn't kill us immediately.
	select {}
}
