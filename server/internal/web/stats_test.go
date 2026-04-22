package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestInternalStatsRequiresSecret(t *testing.T) {
	h := &Handler{cfg: HandlerConfig{AdminSecret: "test-secret-123"}}

	req := httptest.NewRequest(http.MethodGet, "/internal/stats", nil)
	w := httptest.NewRecorder()

	h.handleInternalStats(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}

	// Also test wrong secret
	req = httptest.NewRequest(http.MethodGet, "/internal/stats", nil)
	req.Header.Set("X-Admin-Secret", "wrong-secret")
	w = httptest.NewRecorder()

	h.handleInternalStats(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 with wrong secret, got %d", w.Code)
	}
}

func TestInternalStatsWithSecret(t *testing.T) {
	h := &Handler{cfg: HandlerConfig{AdminSecret: "test-secret-123"}}

	req := httptest.NewRequest(http.MethodGet, "/internal/stats", nil)
	req.Header.Set("X-Admin-Secret", "test-secret-123")
	w := httptest.NewRecorder()

	h.handleInternalStats(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var result map[string]any
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	expectedKeys := []string{
		"total_users",
		"total_households",
		"total_lines",
		"online_lines",
		"active_calls",
		"total_links",
	}
	for _, key := range expectedKeys {
		if _, ok := result[key]; !ok {
			t.Errorf("missing key %q in response", key)
		}
	}
}

func TestInternalStatsEmptySecretRejects(t *testing.T) {
	h := &Handler{cfg: HandlerConfig{AdminSecret: ""}}

	req := httptest.NewRequest(http.MethodGet, "/internal/stats", nil)
	w := httptest.NewRecorder()

	h.handleInternalStats(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 when AdminSecret is empty, got %d", w.Code)
	}
}
