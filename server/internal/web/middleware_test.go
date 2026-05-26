package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsGateExempt(t *testing.T) {
	cases := []struct {
		path  string
		extra []string
		want  bool
	}{
		{"/auth/login", nil, true},
		{"/auth/magic/abc", nil, true},
		{"/auth", nil, false}, // exact "/auth" has no trailing slash; not exempt
		{"/api/status", nil, true},
		{"/api/dashboard/stream", nil, true},
		{"/api", nil, false}, // exact "/api" has no trailing slash; not exempt
		{"/ws", nil, true},
		{"/ws/", nil, true},
		{"/ws/room/abc", nil, true},
		{"/", nil, false},
		{"/phones", nil, false},
		{"/settings", nil, false},
		{"/calls", nil, false},
		{"/welcome", []string{"/welcome"}, true},
		{"/onboard", []string{"/welcome", "/onboard"}, true},
		{"/welcome", []string{"/onboard"}, false},
		{"/phones/5551234", nil, false},
	}

	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			got := isGateExempt(tc.path, tc.extra...)
			if got != tc.want {
				t.Errorf("isGateExempt(%q, %v) = %v, want %v", tc.path, tc.extra, got, tc.want)
			}
		})
	}
}

func TestCSRFOriginCheck(t *testing.T) {
	const baseURL = "https://app.digits.family"

	pass := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := csrfOriginCheck(baseURL, pass)

	cases := []struct {
		name     string
		method   string
		origin   string
		wantCode int
	}{
		{"GET no origin", http.MethodGet, "", http.StatusOK},
		{"GET matching origin", http.MethodGet, baseURL, http.StatusOK},
		{"GET wrong origin", http.MethodGet, "https://evil.example", http.StatusOK},
		{"HEAD wrong origin", http.MethodHead, "https://evil.example", http.StatusOK},
		{"OPTIONS wrong origin", http.MethodOptions, "https://evil.example", http.StatusOK},
		{"POST matching origin", http.MethodPost, baseURL, http.StatusOK},
		{"POST no origin", http.MethodPost, "", http.StatusOK},
		{"POST wrong origin", http.MethodPost, "https://evil.example", http.StatusForbidden},
		{"PUT wrong origin", http.MethodPut, "https://evil.example", http.StatusForbidden},
		{"DELETE wrong origin", http.MethodDelete, "https://evil.example", http.StatusForbidden},
		{"PATCH wrong origin", http.MethodPatch, "https://evil.example", http.StatusForbidden},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, "/", nil)
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.wantCode {
				t.Errorf("code=%d, want %d", rec.Code, tc.wantCode)
			}
		})
	}
}

func TestCSRFOriginCheckDisabledWithEmptyBaseURL(t *testing.T) {
	pass := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := csrfOriginCheck("", pass)

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("empty baseURL: got %d, want 200 (check disabled)", rec.Code)
	}
}
