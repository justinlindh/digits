package web

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/justinlindh/digits/server/internal/calls"
)

func f32(v float32) *float32 { return &v }
func i64(v int64) *int64     { return &v }

func TestSamplesToWindow_Empty(t *testing.T) {
	window, latest := samplesToWindow(nil)
	// The JSON contract is a non-nil empty array plus a nil latest; a nil
	// window would serialize as `null` and change the wire shape.
	if window == nil {
		t.Error("window must be non-nil for empty input")
	}
	if len(window) != 0 {
		t.Errorf("window len = %d, want 0", len(window))
	}
	if latest != nil {
		t.Errorf("latest = %+v, want nil", latest)
	}
}

func TestSamplesToWindow_MapsFieldsAndLatest(t *testing.T) {
	t0 := time.UnixMilli(1_000)
	t1 := time.UnixMilli(2_000)
	samples := []calls.Sample{
		{TS: t0, LossPct: f32(1.5), JitterMs: f32(10), RttMs: f32(40), ConnType: "relay", BytesIn: i64(100), BytesOut: i64(200)},
		{TS: t1, LossPct: f32(0), JitterMs: f32(5), RttMs: f32(20), ConnType: "host", BytesIn: i64(300), BytesOut: i64(400)},
	}
	window, latest := samplesToWindow(samples)

	if len(window) != 2 {
		t.Fatalf("window len = %d, want 2", len(window))
	}
	if window[0].TS != t0.UnixMilli() {
		t.Errorf("window[0].TS = %d, want %d", window[0].TS, t0.UnixMilli())
	}
	if latest == nil {
		t.Fatal("latest must be non-nil for non-empty input")
	}
	// latest must be the last element, not the first.
	if latest.TS != t1.UnixMilli() {
		t.Errorf("latest.TS = %d, want %d (the last sample)", latest.TS, t1.UnixMilli())
	}
	if latest.ConnType != "host" || latest.BytesIn == nil || *latest.BytesIn != 300 {
		t.Errorf("latest fields not mapped from the last sample: %+v", latest)
	}
}

func TestToAPISample_UnixMilliAndPointers(t *testing.T) {
	ts := time.UnixMilli(1_700_000_000_000)
	got := toAPISample(calls.Sample{TS: ts, RttMs: f32(42), ConnType: "srflx"})
	if got.TS != ts.UnixMilli() {
		t.Errorf("TS = %d, want %d", got.TS, ts.UnixMilli())
	}
	if got.RttMs == nil || *got.RttMs != 42 {
		t.Errorf("RttMs = %v, want 42", got.RttMs)
	}
	// Unset pointer fields pass through as nil (so they omit from JSON).
	if got.LossPct != nil || got.BytesIn != nil {
		t.Errorf("unset fields should stay nil, got LossPct=%v BytesIn=%v", got.LossPct, got.BytesIn)
	}
}

func TestParseClampedInt(t *testing.T) {
	const field = "ring_timeout"
	cases := []struct {
		name    string
		raw     string
		wantVal int
		wantOK  bool
	}{
		{"in range", "20", 20, true},
		{"low bound inclusive", "10", 10, true},
		{"high bound inclusive", "60", 60, true},
		{"trims whitespace", "  30 ", 30, true},
		{"below range", "9", 0, false},
		{"above range", "61", 0, false},
		{"empty", "", 0, false},
		{"non-integer", "abc", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			form := url.Values{field: {tc.raw}}
			r := httptest.NewRequest("POST", "/", strings.NewReader(form.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()

			got, ok := parseClampedInt(w, r, field, 10, 60)
			if ok != tc.wantOK {
				t.Fatalf("parseClampedInt(%q) ok = %v, want %v", tc.raw, ok, tc.wantOK)
			}
			if got != tc.wantVal {
				t.Errorf("parseClampedInt(%q) = %d, want %d", tc.raw, got, tc.wantVal)
			}
			if !tc.wantOK && w.Code != 400 {
				t.Errorf("expected 400 on failure, got %d", w.Code)
			}
		})
	}
}
