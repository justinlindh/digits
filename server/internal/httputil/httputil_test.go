package httputil

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthzReturnsVersionJSON(t *testing.T) {
	h := Healthz("1.9.1")
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type=%q, want application/json", ct)
	}
	var got struct {
		Status  string `json:"status"`
		Version string `json:"version"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Status != "ok" {
		t.Errorf("status=%q, want ok", got.Status)
	}
	if got.Version != "1.9.1" {
		t.Errorf("version=%q, want 1.9.1", got.Version)
	}
}

func TestHealthzEmptyVersion(t *testing.T) {
	h := Healthz("")
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var got struct {
		Status  string `json:"status"`
		Version string `json:"version"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&got)
	if got.Status != "ok" || got.Version != "" {
		t.Errorf("unexpected body: %+v", got)
	}
}
