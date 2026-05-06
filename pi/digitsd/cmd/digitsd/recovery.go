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

// debugEntry is one timestamped event in the recovery debug log.
type debugEntry struct {
	Time    string `json:"time"`
	Elapsed string `json:"elapsed"`
	Type    string `json:"type"`
	Detail  string `json:"detail"`
}

// debugLog is a capped ring buffer of recovery events, served via /debug.
type debugLog struct {
	mu      sync.Mutex
	entries []debugEntry
	start   time.Time
}

func newDebugLog() *debugLog {
	return &debugLog{start: time.Now()}
}

func (d *debugLog) add(typ, detail string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := time.Now()
	e := debugEntry{
		Time:    now.Format("15:04:05.000"),
		Elapsed: fmt.Sprintf("%.3fs", now.Sub(d.start).Seconds()),
		Type:    typ,
		Detail:  detail,
	}
	d.entries = append(d.entries, e)
	if len(d.entries) > 500 {
		d.entries = d.entries[len(d.entries)-500:]
	}
	slog.Info("debug", "type", typ, "detail", detail)
}

func (d *debugLog) snapshot() []debugEntry {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]debugEntry, len(d.entries))
	copy(out, d.entries)
	return out
}

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
	dbg := newDebugLog()
	dbg.add("init", fmt.Sprintf("serial=%s alsa=%s tones=%s", *serialDev, pbCfg.Device, *toneDir))
	dbg.add("init", fmt.Sprintf("tones_loaded=%d", len(mixer.ToneNames())))

	// Start recovery web UI.
	go serveRecoveryWeb(state, mixer, sp, dbg)

	// Run voice menu event loop.
	recoveryEventLoop(sp, mixer, state, dbg)
}

// serveRecoveryWeb starts the recovery HTTP server on :80.
func serveRecoveryWeb(state *recoveryState, mixer *audio.Mixer, sp *phone.SerialPort, dbg *debugLog) {
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

	mux.HandleFunc("/debug", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]any{
			"events":        dbg.snapshot(),
			"mixer_active":  mixer.Active(),
			"once_playing":  mixer.OncePlaying(),
			"tone_names":    mixer.ToneNames(),
			"serial_ok":     sp.Ping() == nil,
		}
		json.NewEncoder(w).Encode(resp) //nolint:errcheck
	})

	mux.HandleFunc("/try-again", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		dbg.add("action", "try-again via web")
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
		dbg.add("action", "factory-reset via web")
		w.WriteHeader(http.StatusOK)
		go func() { doRecoveryFactoryReset(mixer, dbg) }()
	})

	slog.Info("recovery web: listening on :80")
	if err := http.ListenAndServe(":80", mux); err != nil {
		slog.Error("recovery web: listen failed", "error", err)
	}
}

// recoveryEventLoop runs the voice menu on the phone handset.
func recoveryEventLoop(sp *phone.SerialPort, mixer *audio.Mixer, state *recoveryState, dbg *debugLog) {
	events := sp.Events()
	dbg.add("loop", "waiting for HOOK:OFF")
	for {
		ev := <-events
		dbg.add("serial", ev)
		if ev != "HOOK:OFF" {
			continue
		}
		recoveryHandsetSession(sp, mixer, state, events, dbg)
		dbg.add("loop", "session ended, waiting for HOOK:OFF")
	}
}

// recoveryHandsetSession handles one off-hook session: plays menu, waits for input.
func recoveryHandsetSession(sp *phone.SerialPort, mixer *audio.Mixer, state *recoveryState, events <-chan string, dbg *debugLog) {
	for {
		dbg.add("audio", "PlayOnce recovery_menu")
		mixer.PlayOnce("recovery_menu")

		key, hungUp := waitForKeyOrHangup(events, 30*time.Second, dbg)
		if hungUp {
			dbg.add("action", "hang-up, stopping all audio")
			mixer.StopAll()
			return
		}

		if key != "" {
			dbg.add("action", fmt.Sprintf("key=%s, stopping all audio", key))
			mixer.StopAll()
			if tone := dtmfToneName(key); tone != "" {
				dbg.add("audio", fmt.Sprintf("PlayOnce %s", tone))
				mixer.PlayOnce(tone)
				waitForOnceComplete(mixer, 500*time.Millisecond)
				dbg.add("audio", fmt.Sprintf("dtmf %s done", tone))
			}
		}

		switch key {
		case "1":
			dbg.add("action", "key 1: restarting")
			mixer.PlayOnce("restarting")
			waitForOnceComplete(mixer, 5*time.Second)
			_ = bootcount.Clear(bootcount.DefaultPath)
			doReboot()
			return

		case "2":
			dbg.add("action", "key 2: awaiting factory reset confirmation")
			mixer.PlayOnce("confirm_factory_reset")
			confirm, hungUp := waitForKeyOrHangup(events, 15*time.Second, dbg)
			if hungUp {
				mixer.StopAll()
				return
			}
			if confirm != "" {
				dbg.add("action", fmt.Sprintf("confirm key=%s, stopping all audio", confirm))
				mixer.StopAll()
				if tone := dtmfToneName(confirm); tone != "" {
					dbg.add("audio", fmt.Sprintf("PlayOnce %s", tone))
					mixer.PlayOnce(tone)
					waitForOnceComplete(mixer, 500*time.Millisecond)
				}
			}
			if confirm != "2" {
				dbg.add("action", "factory reset not confirmed, replaying menu")
				continue
			}
			if !state.startReset() {
				dbg.add("action", "factory reset already in progress")
				return
			}
			doRecoveryFactoryReset(mixer, dbg)
			return

		case "":
			dbg.add("action", "timeout, replaying menu")
			continue

		default:
			dbg.add("action", fmt.Sprintf("unknown key=%s, replaying menu", key))
			continue
		}
	}
}

// waitForKeyOrHangup waits for a KEY event, HOOK:ON, or timeout.
// Returns the key digit (e.g. "1") and whether the user hung up.
// On timeout, returns ("", false).
func waitForKeyOrHangup(events <-chan string, timeout time.Duration, dbg *debugLog) (string, bool) {
	dbg.add("wait", fmt.Sprintf("waiting for key/hangup (timeout=%s)", timeout))
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case ev := <-events:
			dbg.add("serial", ev)
			if ev == "HOOK:ON" {
				return "", true
			}
			if len(ev) > 4 && ev[:4] == "KEY:" {
				dbg.add("wait", fmt.Sprintf("got key: %s", ev[4:]))
				return ev[4:], false
			}
			dbg.add("wait", fmt.Sprintf("ignored event: %s", ev))
		case <-timer.C:
			dbg.add("wait", "timeout")
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
func doRecoveryFactoryReset(mixer *audio.Mixer, dbg *debugLog) {
	dbg.add("reset", "starting factory reset")
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
