package main

import (
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

	"github.com/justinlindh/digits/pi/digitsd/internal/devmode"
)

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

		file, header, err := r.FormFile("firmware")
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "no firmware file in request"}) //nolint:errcheck
			return
		}
		defer file.Close() //nolint:errcheck

		// Write to staging area.
		stagingDir := "/data/digits/staging"
		if err := os.MkdirAll(stagingDir, 0755); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "create staging dir: " + err.Error()}) //nolint:errcheck
			return
		}
		destPath := filepath.Join(stagingDir, "dev-upload.elf")
		out, err := os.Create(destPath)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "create staging file: " + err.Error()}) //nolint:errcheck
			return
		}
		if _, err := io.Copy(out, file); err != nil {
			_ = out.Close()
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "write file: " + err.Error()}) //nolint:errcheck
			return
		}
		_ = out.Close()

		slog.Info("devmode: firmware upload received", "filename", header.Filename, "size", header.Size, "staged", destPath)

		// Guard against concurrent flash operations (shares the same
		// atomic with the OTA updater).
		if !updateInProgress.CompareAndSwap(false, true) {
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]string{"error": "another flash/update is already in progress"}) //nolint:errcheck
			return
		}

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
		cmd := exec.Command("journalctl", "-u", "digitsd", "-n", "200", "--no-pager")
		out, err := cmd.Output()
		if err != nil {
			fmt.Fprintf(w, "(journalctl failed: %v)", err) //nolint:errcheck
			return
		}
		_, _ = w.Write(out)
	}
}
