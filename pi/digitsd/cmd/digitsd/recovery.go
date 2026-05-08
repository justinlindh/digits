package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
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
	"github.com/justinlindh/digits/pi/digitsd/internal/subsystem"
)

//go:embed recovery_static
var recoveryStaticFS embed.FS

// modeLogFile holds the open log file for non-normal modes so the panic
// handler can flush it before halting.
var modeLogFile *os.File

// setupModeLog tees slog to both stderr and a file. The web module's
// /log/raw endpoint reads the same file for the live log viewer.
func setupModeLog(path string) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		slog.Warn("could not open log file", "path", path, "error", err)
		return
	}
	modeLogFile = f
	writers := []io.Writer{os.Stderr, f}
	if crashLogFile != nil {
		writers = append(writers, crashLogFile)
	}
	logger := slog.New(slog.NewTextHandler(io.MultiWriter(writers...), &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)
}

// crashLogFile is a persistent log on /data for post-mortem debugging.
// Included in the slog MultiWriter by setupModeLog when non-nil.
var crashLogFile *os.File

// setupCrashLog opens a persistent crash log on /data that survives reboots.
func setupCrashLog(path string) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return
	}
	crashLogFile = f
	_, _ = fmt.Fprintf(f, "crash log opened at %s\n", time.Now().Format(time.RFC3339))
	_ = f.Sync()
}

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
		trimmed := make([]debugEntry, 500)
		copy(trimmed, d.entries[len(d.entries)-500:])
		d.entries = trimmed
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

// recoveryState tracks the current recovery action so the web UI can
// show the right state on refresh.
type recoveryState struct {
	mu     sync.Mutex
	action string // "", "try-again", "factory-reset"
	status string // human-readable progress
	failed bool
}

func (rs *recoveryState) startReset() bool {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if rs.action == "factory-reset" {
		return false
	}
	rs.action = "factory-reset"
	rs.status = "Starting factory reset..."
	rs.failed = false
	return true
}

func (rs *recoveryState) setStatus(s string) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.status = s
}

func (rs *recoveryState) setFailed(s string) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.status = s
	rs.failed = true
}

func (rs *recoveryState) startTryAgain() {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.action = "try-again"
	rs.status = "Rebooting..."
}

func (rs *recoveryState) snapshot() map[string]any {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return map[string]any{
		"action": rs.action,
		"status": rs.status,
		"failed": rs.failed,
	}
}

func runRecoveryMode(web *subsystem.WebModule, serial *subsystem.SerialModule, audioMod *subsystem.AudioModule) {
	slog.Info("digitsd: entering recovery mode", "pid", os.Getpid())

	defer func() {
		if r := recover(); r != nil {
			slog.Error("recovery: panic", "panic", fmt.Sprintf("%v", r))
			if modeLogFile != nil {
				_ = modeLogFile.Sync()
			}
			select {}
		}
	}()

	var sp *phone.SerialPort
	var mixer *audio.Mixer
	if serial != nil && serial.IsReady() {
		sp = serial.Port()
		sp.StateSet("RECOVERY")
	}
	if audioMod != nil && audioMod.IsReady() {
		mixer = audioMod.Mixer()
	}

	state := &recoveryState{}
	dbg := newDebugLog()
	toneCount := 0
	if mixer != nil {
		toneCount = len(mixer.ToneNames())
	}
	dbg.add("init", fmt.Sprintf("tones_loaded=%d serial=%v audio=%v", toneCount, sp != nil, mixer != nil))

	// Mount recovery-specific routes on the web module's mux.
	mux := web.Mux()
	mountRecoveryRoutes(mux, state, mixer, sp, dbg)

	// Run voice menu event loop (only if serial and audio are available).
	if sp != nil && mixer != nil {
		runRecoveryVoiceLoop(sp, mixer, state, dbg)
	} else {
		slog.Warn("recovery: serial or audio unavailable, voice menu disabled")
		select {}
	}
}

// mountRecoveryRoutes registers recovery-specific HTTP handlers on the
// web module's shared mux. The web module itself handles /status (subsystem
// statuses) and /log/raw (formatted log tail).
func mountRecoveryRoutes(mux *http.ServeMux, state *recoveryState, mixer *audio.Mixer, sp *phone.SerialPort, dbg *debugLog) {
	staticFS, err := fs.Sub(recoveryStaticFS, "recovery_static")
	if err != nil {
		slog.Error("recovery web: embed sub", "error", err)
		syncAndHalt()
	}

	mux.Handle("/", http.FileServer(http.FS(staticFS)))

	mux.HandleFunc("/boot-status", func(w http.ResponseWriter, r *http.Request) {
		count, _ := bootcount.Read(bootcount.DefaultPath)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]int{"boot_count": count}) //nolint:errcheck
	})

	mux.HandleFunc("/debug", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]any{
			"events": dbg.snapshot(),
		}
		if mixer != nil {
			resp["mixer_active"] = mixer.Active()
			resp["once_playing"] = mixer.OncePlaying()
			resp["tone_names"] = mixer.ToneNames()
		}
		if sp != nil {
			resp["serial_ok"] = sp.Ping() == nil
		}
		json.NewEncoder(w).Encode(resp) //nolint:errcheck
	})

	mux.HandleFunc("/log", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(w, `<!DOCTYPE html><html><head><title>Recovery Log</title>
<style>body{background:#111;color:#0f0;font:13px/1.4 monospace;margin:1em;white-space:pre-wrap}
#log{max-height:90vh;overflow-y:auto}</style></head><body><div id="log">Loading...</div>
<script>async function poll(){try{const r=await fetch('/log/raw');
document.getElementById('log').textContent=await r.text();
document.getElementById('log').scrollTop=9999999}catch(e){}
setTimeout(poll,2000)}poll()</script></body></html>`)
	})

	mux.HandleFunc("/action", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(state.snapshot()) //nolint:errcheck
	})

	mux.HandleFunc("/try-again", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		state.startTryAgain()
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
		go func() { doRecoveryFactoryReset(sp, mixer, state, dbg) }()
	})
}

// runRecoveryVoiceLoop runs the voice menu on the phone handset using the
// shared voicePromptLoop, wiring in recovery-specific key handling.
func runRecoveryVoiceLoop(sp *phone.SerialPort, mixer *audio.Mixer, state *recoveryState, dbg *debugLog) {
	events := sp.Events()
	dbg.add("loop", "waiting for HOOK:OFF")

	cfg := voicePromptConfig{
		Clip:           "recovery_menu",
		ReplayInterval: 0, // no pause between replays; timeout is inside waitForKeyOrHangup
		OnKey: func(key string) bool {
			return recoveryHandleKey(sp, key, events, mixer, state, dbg)
		},
	}
	voicePromptLoop(events, mixer, cfg)
}

// recoveryHandleKey processes a key press during the recovery voice menu.
// Returns true to end the session (e.g. after triggering an action).
func recoveryHandleKey(sp *phone.SerialPort, key string, events <-chan string, mixer *audio.Mixer, state *recoveryState, dbg *debugLog) bool {
	switch key {
	case "1":
		dbg.add("action", "key 1: restarting")
		state.startTryAgain()
		slog.Info("recovery: restart triggered via keypad")
		mixer.PlayOnce("restarting")
		waitForOnceComplete(mixer, 5*time.Second)
		_ = bootcount.Clear(bootcount.DefaultPath)
		doReboot()
		return true

	case "2":
		dbg.add("action", "key 2: awaiting factory reset confirmation")
		mixer.PlayOnce("confirm_factory_reset")
		confirm, hungUp := waitForKeyOrHangup(events, 15*time.Second)
		if hungUp {
			mixer.StopAll()
			return true
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
			return false
		}
		if !state.startReset() {
			dbg.add("action", "factory reset already in progress")
			return true
		}
		slog.Info("recovery: factory reset triggered via keypad")
		doRecoveryFactoryReset(sp, mixer, state, dbg)
		return true

	default:
		dbg.add("action", fmt.Sprintf("unknown key=%s, replaying menu", key))
		return false
	}
}

// doRecoveryFactoryReset performs the full factory reset sequence.
func doRecoveryFactoryReset(sp *phone.SerialPort, mixer *audio.Mixer, rs *recoveryState, dbg *debugLog) {
	dbg.add("reset", "starting factory reset")
	slog.Info("recovery: starting factory reset")
	rs.setStatus("Starting factory reset...")

	playOnce := func(name string) {
		if mixer != nil {
			mixer.PlayOnce(name)
			waitForOnceComplete(mixer, 10*time.Second)
		}
	}

	playOnce("factory_reset_in_progress")
	playOnce("restoring_system")

	rootfsImg := "/rootfs.img.zst"
	if _, err := os.Stat(rootfsImg); err != nil {
		slog.Error("recovery: rootfs.img.zst not found", "path", rootfsImg, "error", err)
		rs.setFailed("rootfs image not found")
		playOnce("error_tone")
		return
	}

	rs.setStatus("Restoring rootfs (this takes a few minutes)...")
	slog.Info("recovery: decompressing rootfs to /dev/mmcblk0p2")
	cmd := exec.Command("sh", "-c", fmt.Sprintf("zstd -d -c %s | dd of=/dev/mmcblk0p2 bs=4M conv=fsync", rootfsImg))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		slog.Error("recovery: rootfs restore failed", "error", err)
		rs.setFailed(fmt.Sprintf("rootfs restore failed: %v", err))
		playOnce("error_tone")
		return
	}
	slog.Info("recovery: rootfs restored")

	rs.setStatus("Formatting data partition...")
	playOnce("formatting_data")

	// Flush and close the crash log before unmounting /data.
	slog.Info("recovery: unmounting /data (crash log will stop)")
	if crashLogFile != nil {
		_ = crashLogFile.Sync()
		_ = crashLogFile.Close()
		crashLogFile = nil
	}
	_ = syscall.Unmount("/data", 0)

	slog.Info("recovery: formatting /dev/mmcblk0p4")
	mkfs := exec.Command("mkfs.ext4", "-L", "data", "-F", "/dev/mmcblk0p4")
	mkfs.Stdout = os.Stdout
	mkfs.Stderr = os.Stderr
	if err := mkfs.Run(); err != nil {
		slog.Error("recovery: mkfs.ext4 failed", "error", err)
		rs.setFailed(fmt.Sprintf("data format failed: %v", err))
		playOnce("error_tone")
		return
	}

	rs.setStatus("Restoring default data...")
	slog.Info("recovery: mounting /data")
	if err := syscall.Mount("/dev/mmcblk0p4", "/data", "ext4", 0, ""); err != nil {
		slog.Error("recovery: mount /data failed", "error", err)
		rs.setFailed(fmt.Sprintf("data mount failed: %v", err))
		playOnce("error_tone")
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
		}
	} else {
		slog.Info("recovery: no data skeleton found, skipping", "path", skeletonPath)
	}

	rs.setStatus("Factory reset complete. Rebooting...")
	slog.Info("recovery: factory reset complete")
	if sp != nil {
		sp.StateSet("SETUP")
	}
	playOnce("reset_complete")

	doReboot()
}

// syncAndHalt flushes logs and blocks forever. Used instead of log.Fatal
// when running as PID 1, since os.Exit kills PID 1 and causes a kernel panic
// before the log file is flushed.
func syncAndHalt() {
	if modeLogFile != nil {
		_ = modeLogFile.Sync()
	}
	select {}
}

// clearRecoveryFlags mounts /data temporarily to clear the boot counter
// and recovery-mode flag so the next boot proceeds normally.
func clearRecoveryFlags() {
	_ = os.MkdirAll("/data", 0755)
	if err := syscall.Mount("/dev/mmcblk0p4", "/data", "ext4", 0, ""); err != nil {
		slog.Warn("recovery: mount /data for flag cleanup failed", "error", err)
		return
	}
	_ = bootcount.Clear(bootcount.DefaultPath)
	_ = os.Remove("/data/digits/recovery-mode")
	slog.Info("recovery: boot counter and recovery flag cleared")
	syscall.Sync()
	_ = syscall.Unmount("/data", 0)
}

// doReboot reboots the system. If running as PID 1 (init in initramfs),
// uses the raw reboot syscall. Otherwise uses systemctl.
func doReboot() {
	slog.Info("recovery: rebooting")
	clearRecoveryFlags()
	time.Sleep(500 * time.Millisecond)

	syscall.Sync()
	if os.Getpid() == 1 {
		if err := syscall.Reboot(syscall.LINUX_REBOOT_CMD_RESTART); err != nil {
			slog.Error("recovery: reboot syscall failed", "error", err)
		}
	} else {
		cmd := exec.Command("sudo", "/usr/sbin/reboot")
		if err := cmd.Run(); err != nil {
			slog.Warn("recovery: sudo reboot failed, trying systemctl", "error", err)
			cmd2 := exec.Command("systemctl", "reboot")
			if err := cmd2.Run(); err != nil {
				slog.Error("recovery: all reboot methods failed", "error", err)
			}
		}
	}
	// Block forever if reboot doesn't kill us immediately.
	select {}
}
