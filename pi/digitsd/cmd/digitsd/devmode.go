package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/justinlindh/digits/pi/digitsd/internal/devmode"
)

// devModeHelperPath is the privileged helper that performs the root-level work
// of toggling developer mode (SSH host keys, the dev user, ssh.service). It is
// invoked via the single sudoers allowlist entry in /etc/sudoers.d/digits-devmode.
const devModeHelperPath = "/usr/local/bin/digits-devmode"

// runDevModeHelper shells out to the privileged helper to enable or disable
// developer mode. When enabling, the new SSH login password is piped on stdin
// so it never appears in the process list.
func runDevModeHelper(enable bool, password string) error {
	sub := "disable"
	if enable {
		sub = "enable"
	}
	cmd := exec.Command("sudo", devModeHelperPath, sub)
	if enable {
		cmd.Stdin = strings.NewReader(password + "\n")
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("digits-devmode %s: %w: %s", sub, err, bytes.TrimSpace(out))
	}
	slog.Info("devmode: helper applied", "action", sub, "output", strings.TrimSpace(string(out)))
	return nil
}

// devModeManager owns the lifecycle of the on-device dev web UI listener and
// the privileged enable/disable transitions. It lets the daemon flip dev mode
// at runtime (in response to a server command) without a restart.
//
// apply performs the privileged work; it is a field so tests can inject a stub
// in place of the real sudo invocation.
type devModeManager struct {
	cfg   *devModeConfig
	apply func(enable bool, password string) error
	start func(*devModeConfig) (net.Listener, error)

	mu sync.Mutex
	ln net.Listener
}

func newDevModeManager(cfg *devModeConfig) *devModeManager {
	return &devModeManager{cfg: cfg, apply: runDevModeHelper, start: startDevModeServer}
}

func (m *devModeManager) startListenerLocked() error {
	if m.ln != nil {
		return nil
	}
	ln, err := m.start(m.cfg)
	if err != nil {
		return err
	}
	m.ln = ln
	return nil
}

func (m *devModeManager) stopListenerLocked() {
	if m.ln != nil {
		_ = m.ln.Close()
		m.ln = nil
	}
}

// EnsureListener starts the dev web UI if it is not already running. Used at
// boot when the dev-mode flag is already present.
func (m *devModeManager) EnsureListener() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.startListenerLocked()
}

// Enable runs the privileged helper to turn dev mode on (creating the dev user,
// setting its SSH password, starting ssh.service) and then starts the dev web
// UI listener. The lock is held across apply so concurrent dev_mode commands
// can't run two helper processes that race on the rootfs remount.
func (m *devModeManager) Enable(password string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.apply(true, password); err != nil {
		return err
	}
	return m.startListenerLocked()
}

// Disable runs the privileged helper to turn dev mode off (stopping
// ssh.service, locking the dev account, clearing the flag) and then stops the
// dev web UI listener. The lock is held across apply for the same reason as
// Enable: serialize the privileged transition.
func (m *devModeManager) Disable() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.apply(false, ""); err != nil {
		return err
	}
	m.stopListenerLocked()
	return nil
}

// Close shuts down the listener without changing privileged state. Used on
// daemon shutdown.
func (m *devModeManager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopListenerLocked()
}

//go:embed devmode_static
var devmodeStaticFS embed.FS

// devModeStatus holds the snapshot returned by /api/status. Not serialized
// directly -- the handler merges it with dynamic flag state into a map.
type devModeStatus struct {
	DigitsdVersion   string
	FirmwareVersion  string
	FirmwareCommit   string
	Phase            string
	Online           bool
	PhoneNumber      string
	ConfigAutoUpdate bool
}

// devModeConfig holds paths and callbacks the dev-mode HTTP server needs.
type devModeConfig struct {
	FlagPath           string
	SkipFWReflashPath  string
	SkipAutoUpdatePath string

	// StatusFunc returns the current device status snapshot.
	StatusFunc func() devModeStatus

	// FlashFunc flashes a firmware ELF at the given path. Nil disables
	// the flash endpoint.
	FlashFunc func(elfPath string) error

	// UARTLogPath is the UART log file path (same as the serial logger).
	UARTLogPath string

	// CaptureDevice is the ALSA device name for mic capture (e.g.
	// "digits_capture" on V2 or "plughw:CARD=Zero,DEV=0" on V1).
	CaptureDevice string
}

// startDevModeServer starts the dev-mode web UI on :8080.
// Returns the listener for shutdown; the server runs in a goroutine.
func startDevModeServer(cfg *devModeConfig) (net.Listener, error) {
	mux := http.NewServeMux()

	staticFS, err := fs.Sub(devmodeStaticFS, "devmode_static")
	if err != nil {
		return nil, fmt.Errorf("devmode: embed sub: %w", err)
	}
	mux.Handle("/", http.FileServer(http.FS(staticFS)))

	mux.HandleFunc("/api/status", devModeStatusHandler(cfg))
	mux.HandleFunc("/api/toggle", devModeToggleHandler(cfg))
	mux.HandleFunc("/api/flash", devModeFlashHandler(cfg))
	mux.HandleFunc("/api/mic-test", devModeMicTestHandler(cfg))
	mux.HandleFunc("/api/mic-test/download", devModeMicTestDownloadHandler())
	mux.HandleFunc("/api/log/serial", devModeSerialLogHandler(cfg))
	mux.HandleFunc("/api/log/journal", devModeJournalLogHandler())

	ln, err := net.Listen("tcp", ":8080")
	if err != nil {
		return nil, fmt.Errorf("devmode: listen :8080: %w", err)
	}

	go func() {
		slog.Info("devmode: web UI listening on :8080")
		if serveErr := http.Serve(ln, mux); serveErr != nil && !errors.Is(serveErr, net.ErrClosed) {
			slog.Error("devmode: serve failed", "error", serveErr)
		}
	}()

	return ln, nil
}

func devModeStatusHandler(cfg *devModeConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var status devModeStatus
		if cfg.StatusFunc != nil {
			status = cfg.StatusFunc()
		}
		resp := map[string]any{
			"digitsd_version":    status.DigitsdVersion,
			"firmware_version":   status.FirmwareVersion,
			"firmware_commit":    status.FirmwareCommit,
			"phase":              status.Phase,
			"online":             status.Online,
			"phone_number":       status.PhoneNumber,
			"config_auto_update": status.ConfigAutoUpdate,
			"dev_mode":           devmode.Enabled(cfg.FlagPath),
			"skip_fw_reflash":    devmode.SkipFWReflash(cfg.SkipFWReflashPath),
			"skip_auto_update":   devmode.SkipAutoUpdate(cfg.SkipAutoUpdatePath),
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp) //nolint:errcheck
	}
}

func devModeToggleHandler(cfg *devModeConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Name    string `json:"name"`
			Enabled bool   `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		var err error
		switch req.Name {
		case "devmode":
			if req.Enabled {
				err = devmode.Enable(cfg.FlagPath)
			} else {
				err = devmode.Disable(cfg.FlagPath)
			}
		case "fw_reflash":
			err = devmode.SetSkipFWReflash(cfg.SkipFWReflashPath, req.Enabled)
		case "auto_update":
			err = devmode.SetSkipAutoUpdate(cfg.SkipAutoUpdatePath, req.Enabled)
		default:
			http.Error(w, "unknown toggle: "+req.Name, http.StatusBadRequest)
			return
		}

		if err != nil {
			slog.Error("devmode: toggle failed", "name", req.Name, "error", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		slog.Info("devmode: toggle", "name", req.Name, "enabled", req.Enabled)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"}) //nolint:errcheck
	}
}

func devModeFlashHandler(cfg *devModeConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		if cfg.FlashFunc == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]string{"error": "flash not available (no SWD capability)"}) //nolint:errcheck
			return
		}

		if !updateInProgress.CompareAndSwap(false, true) {
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]string{"error": "another flash/update is already in progress"}) //nolint:errcheck
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, 4<<20) // 4 MB max
		file, header, err := r.FormFile("firmware")
		if err != nil {
			updateInProgress.Store(false)
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "no firmware file in request"}) //nolint:errcheck
			return
		}
		defer file.Close() //nolint:errcheck

		stagingDir := "/data/digits/staging"
		if err := os.MkdirAll(stagingDir, 0755); err != nil {
			updateInProgress.Store(false)
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "create staging dir: " + err.Error()}) //nolint:errcheck
			return
		}
		destPath := filepath.Join(stagingDir, "dev-upload.elf")
		out, err := os.Create(destPath)
		if err != nil {
			updateInProgress.Store(false)
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "create staging file: " + err.Error()}) //nolint:errcheck
			return
		}
		if _, err := io.Copy(out, file); err != nil {
			_ = out.Close()
			updateInProgress.Store(false)
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "write file: " + err.Error()}) //nolint:errcheck
			return
		}
		_ = out.Close()

		slog.Info("devmode: firmware upload received", "filename", header.Filename, "size", header.Size, "staged", destPath)

		go func() {
			defer updateInProgress.Store(false)
			if flashErr := cfg.FlashFunc(destPath); flashErr != nil {
				slog.Error("devmode: flash failed", "error", flashErr)
			} else {
				slog.Info("devmode: flash succeeded")
			}
		}()

		json.NewEncoder(w).Encode(map[string]string{"message": "Flashing " + header.Filename + " -- check serial log for progress"}) //nolint:errcheck
	}
}

func devModeSerialLogHandler(cfg *devModeConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if cfg.UARTLogPath == "" {
			fmt.Fprint(w, "(no UART log path configured)") //nolint:errcheck
			return
		}
		data, err := os.ReadFile(cfg.UARTLogPath)
		if err != nil {
			fmt.Fprintf(w, "(cannot read log: %v)", err) //nolint:errcheck
			return
		}
		// Tail: return last 64KB max.
		const maxBytes = 64 * 1024
		if len(data) > maxBytes {
			data = data[len(data)-maxBytes:]
		}
		_, _ = w.Write(data)
	}
}

func devModeJournalLogHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		cmd := exec.Command("sudo", "journalctl", "-u", "digitsd", "-n", "200", "--no-pager")
		out, err := cmd.CombinedOutput()
		if err != nil {
			fmt.Fprintf(w, "(journalctl failed: %v)\n%s", err, out) //nolint:errcheck
			return
		}
		_, _ = w.Write(out)
	}
}

const micTestPath = "/tmp/mic-test.wav"

var (
	micRecording atomic.Bool
	micLastErr   atomic.Value // stores string; empty means no error
)

func devModeMicTestHandler(cfg *devModeConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.Method {
		case http.MethodPost:
			if !micRecording.CompareAndSwap(false, true) {
				w.WriteHeader(http.StatusConflict)
				json.NewEncoder(w).Encode(map[string]string{"error": "recording already in progress"}) //nolint:errcheck
				return
			}
			micLastErr.Store("")

			device := cfg.CaptureDevice
			if device == "" {
				device = "default"
			}

			go func() {
				defer micRecording.Store(false)
				cmd := exec.Command("arecord",
					"-D", device,
					"-f", "S16_LE",
					"-r", "48000",
					"-c", "2",
					"-d", "5",
					micTestPath)
				out, err := cmd.CombinedOutput()
				if err != nil {
					msg := fmt.Sprintf("arecord: %v: %s", err, out)
					slog.Error("devmode: mic test failed", "error", msg)
					micLastErr.Store(msg)
				} else {
					slog.Info("devmode: mic test recording completed")
				}
			}()

			json.NewEncoder(w).Encode(map[string]any{"status": "recording", "duration": 5}) //nolint:errcheck

		case http.MethodGet:
			if micRecording.Load() {
				json.NewEncoder(w).Encode(map[string]string{"status": "recording"}) //nolint:errcheck
				return
			}
			if v := micLastErr.Load(); v != nil {
				if errStr, _ := v.(string); errStr != "" {
					json.NewEncoder(w).Encode(map[string]string{"status": "error", "error": errStr}) //nolint:errcheck
					return
				}
			}
			if _, err := os.Stat(micTestPath); err != nil {
				json.NewEncoder(w).Encode(map[string]string{"status": "idle"}) //nolint:errcheck
				return
			}
			json.NewEncoder(w).Encode(map[string]string{"status": "ready"}) //nolint:errcheck

		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

func devModeMicTestDownloadHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if micRecording.Load() {
			http.Error(w, "recording in progress", http.StatusConflict)
			return
		}
		http.ServeFile(w, r, micTestPath)
	}
}
