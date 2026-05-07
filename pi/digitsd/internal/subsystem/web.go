package subsystem

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
)

type WebModule struct {
	mux     *http.ServeMux
	mgr     *Manager
	ln      net.Listener
	logPath string
	status  ModuleStatus
}

func NewWebModule() *WebModule {
	return &WebModule{
		mux:     http.NewServeMux(),
		logPath: "/tmp/subsystem.log",
		status:  ModuleStatus{State: StatePending},
	}
}

func (w *WebModule) SetLogPath(path string) { w.logPath = path }

func (w *WebModule) Name() string { return "web" }

func (w *WebModule) SetManager(mgr *Manager) { w.mgr = mgr }

func (w *WebModule) Init(ctx context.Context) error {
	w.status.State = StateInitializing

	w.mux.HandleFunc("/status", w.handleStatus)
	w.mux.HandleFunc("/log/raw", w.handleLogRaw)

	ln, err := net.Listen("tcp", ":80")
	if err != nil {
		w.status = ModuleStatus{State: StateFailed, Message: err.Error()}
		return fmt.Errorf("listen :80: %w", err)
	}
	w.ln = ln

	go func() {
		if err := http.Serve(ln, w.mux); err != nil && !errors.Is(err, net.ErrClosed) {
			slog.Error("subsystem web: serve failed", "error", err)
		}
	}()

	w.status.State = StateReady
	slog.Info("subsystem web: listening on :80")
	return nil
}

func (w *WebModule) Mux() *http.ServeMux { return w.mux }
func (w *WebModule) IsReady() bool       { return w.status.State == StateReady }
func (w *WebModule) Status() ModuleStatus { return w.status }

func (w *WebModule) Shutdown(ctx context.Context) error {
	if w.ln != nil {
		return w.ln.Close()
	}
	return nil
}

func (w *WebModule) handleStatus(rw http.ResponseWriter, r *http.Request) {
	rw.Header().Set("Content-Type", "application/json")
	if w.mgr == nil {
		_ = json.NewEncoder(rw).Encode(map[string]string{"error": "no manager"})
		return
	}
	_ = json.NewEncoder(rw).Encode(w.mgr.Status())
}

var logLineRe = regexp.MustCompile(`^time=\S+\s+level=(\w+)\s+msg="?([^"]*)"?\s*(.*)$`)

func (w *WebModule) handleLogRaw(rw http.ResponseWriter, r *http.Request) {
	rw.Header().Set("Content-Type", "text/plain; charset=utf-8")
	data, err := os.ReadFile(w.logPath)
	if err != nil {
		_, _ = fmt.Fprintf(rw, "no log available")
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		m := logLineRe.FindStringSubmatch(line)
		if m == nil {
			_, _ = fmt.Fprintln(rw, line)
			continue
		}
		level, msg, extra := m[1], m[2], strings.TrimSpace(m[3])
		if extra != "" {
			_, _ = fmt.Fprintf(rw, "%-5s %s  %s\n", level, msg, extra)
		} else {
			_, _ = fmt.Fprintf(rw, "%-5s %s\n", level, msg)
		}
	}
}
