package subsystem

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
)

type WebModule struct {
	mux    *http.ServeMux
	mgr    *Manager
	ln     net.Listener
	status ModuleStatus
}

func NewWebModule() *WebModule {
	return &WebModule{
		mux:    http.NewServeMux(),
		status: ModuleStatus{State: StatePending},
	}
}

func (w *WebModule) Name() string { return "web" }

func (w *WebModule) SetManager(mgr *Manager) { w.mgr = mgr }

func (w *WebModule) Init(ctx context.Context) error {
	w.status.State = StateInitializing

	w.mux.HandleFunc("/status", w.handleStatus)
	w.mux.HandleFunc("/log/raw", handleLogRaw)

	ln, err := net.Listen("tcp", ":80")
	if err != nil {
		w.status = ModuleStatus{State: StateFailed, Message: err.Error()}
		return fmt.Errorf("listen :80: %w", err)
	}
	w.ln = ln

	go func() {
		if err := http.Serve(ln, w.mux); err != nil && !strings.Contains(err.Error(), "closed") {
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
		json.NewEncoder(rw).Encode(map[string]string{"error": "no manager"})
		return
	}
	json.NewEncoder(rw).Encode(w.mgr.Status())
}

var logLineRe = regexp.MustCompile(`^time=\S+\s+level=(\w+)\s+msg="?([^"]*)"?\s*(.*)$`)

func handleLogRaw(rw http.ResponseWriter, r *http.Request) {
	rw.Header().Set("Content-Type", "text/plain; charset=utf-8")
	data, err := os.ReadFile("/tmp/recovery.log")
	if err != nil {
		fmt.Fprintf(rw, "no log available")
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		m := logLineRe.FindStringSubmatch(line)
		if m == nil {
			fmt.Fprintln(rw, line)
			continue
		}
		level, msg, extra := m[1], m[2], strings.TrimSpace(m[3])
		if extra != "" {
			fmt.Fprintf(rw, "%-5s %s  %s\n", level, msg, extra)
		} else {
			fmt.Fprintf(rw, "%-5s %s\n", level, msg)
		}
	}
}
