package httputil

import (
	"bufio"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStatusRecorderDefaultsTo200OnWrite(t *testing.T) {
	rec := httptest.NewRecorder()
	sr := &StatusRecorder{ResponseWriter: rec}

	if _, err := sr.Write([]byte("hello")); err != nil {
		t.Fatalf("write: %v", err)
	}

	if sr.Status != http.StatusOK {
		t.Errorf("status=%d, want 200", sr.Status)
	}
	if !sr.WroteHeader {
		t.Error("WroteHeader=false, want true after Write")
	}
	if got := rec.Body.String(); got != "hello" {
		t.Errorf("body=%q, want %q", got, "hello")
	}
}

func TestStatusRecorderCapturesExplicitStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	sr := &StatusRecorder{ResponseWriter: rec}

	sr.WriteHeader(http.StatusCreated)
	if _, err := sr.Write([]byte("x")); err != nil {
		t.Fatalf("write: %v", err)
	}

	if sr.Status != http.StatusCreated {
		t.Errorf("status=%d, want 201", sr.Status)
	}
	if rec.Code != http.StatusCreated {
		t.Errorf("underlying code=%d, want 201", rec.Code)
	}
}

func TestStatusRecorderWriteHeaderOnlyAppliesOnce(t *testing.T) {
	rec := httptest.NewRecorder()
	sr := &StatusRecorder{ResponseWriter: rec}

	sr.WriteHeader(http.StatusNotFound)
	sr.WriteHeader(http.StatusInternalServerError)

	if sr.Status != http.StatusNotFound {
		t.Errorf("status=%d, want 404 (first wins)", sr.Status)
	}
	if rec.Code != http.StatusNotFound {
		t.Errorf("underlying code=%d, want 404 (second call must not reach writer)", rec.Code)
	}
}

func TestStatusRecorderFlushDelegates(t *testing.T) {
	rec := httptest.NewRecorder()
	sr := &StatusRecorder{ResponseWriter: rec}

	sr.Flush()

	if !rec.Flushed {
		t.Error("underlying recorder not flushed")
	}
}

// nonFlusher wraps an http.ResponseWriter while hiding the Flusher
// interface, so Flush() must become a safe no-op.
type nonFlusher struct{ http.ResponseWriter }

func TestStatusRecorderFlushNoopWhenUnsupported(t *testing.T) {
	sr := &StatusRecorder{ResponseWriter: nonFlusher{httptest.NewRecorder()}}
	// Must not panic when the underlying writer is not an http.Flusher.
	sr.Flush()
}

func TestStatusRecorderHijackUnsupported(t *testing.T) {
	// httptest.ResponseRecorder does not implement http.Hijacker.
	sr := &StatusRecorder{ResponseWriter: httptest.NewRecorder()}

	conn, rw, err := sr.Hijack()
	if err != http.ErrNotSupported {
		t.Errorf("err=%v, want %v", err, http.ErrNotSupported)
	}
	if conn != nil || rw != nil {
		t.Error("expected nil conn and bufio on unsupported hijack")
	}
}

// hijackableWriter implements http.Hijacker so the pass-through path is exercised.
type hijackableWriter struct {
	http.ResponseWriter
	hijacked bool
}

func (h *hijackableWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h.hijacked = true
	return nil, nil, nil
}

func TestStatusRecorderHijackDelegates(t *testing.T) {
	hw := &hijackableWriter{ResponseWriter: httptest.NewRecorder()}
	sr := &StatusRecorder{ResponseWriter: hw}

	if _, _, err := sr.Hijack(); err != nil {
		t.Fatalf("hijack: %v", err)
	}
	if !hw.hijacked {
		t.Error("underlying Hijack not called")
	}
}
