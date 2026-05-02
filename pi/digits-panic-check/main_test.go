package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// mockSerial implements io.ReadWriteCloser with canned responses.
type mockSerial struct {
	rx       *bytes.Buffer // data the "Pico" sends back
	tx       bytes.Buffer  // data written by the binary
	closed   bool
	readErr  error // injected read error
	writeErr error // injected write error
}

func (m *mockSerial) Read(p []byte) (int, error) {
	if m.readErr != nil {
		return 0, m.readErr
	}
	return m.rx.Read(p)
}

func (m *mockSerial) Write(p []byte) (int, error) {
	if m.writeErr != nil {
		return 0, m.writeErr
	}
	return m.tx.Write(p)
}

func (m *mockSerial) Close() error {
	m.closed = true
	return nil
}

// keydumpResponse builds a standard KEYDUMP response with the given column
// values for each row. Each row is a slice of 3 column values (0 or 1).
func keydumpResponse(rows [4][3]int) string {
	var b strings.Builder
	b.WriteString("ROWS: R0/GP27=1 R1/GP26=1 R2/GP21=1 R3/GP25=1\n")
	b.WriteString("COLS: C0/GP24=1 C1/GP23=1 C2/GP22=1\n")
	gpPins := [4]int{27, 26, 21, 25}
	for r := 0; r < 4; r++ {
		b.WriteString("SCAN R")
		b.WriteByte('0' + byte(r))
		b.WriteString("/GP")
		b.WriteString(itoa(gpPins[r]))
		b.WriteString("=LOW:")
		for c := 0; c < 3; c++ {
			b.WriteString(" C")
			b.WriteByte('0' + byte(c))
			b.WriteByte('=')
			b.WriteByte('0' + byte(rows[r][c]))
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}

func TestParseStarHeld_Pressed(t *testing.T) {
	// * is at row 3, col 0. C0=0 means pressed.
	lines := strings.Split(strings.TrimSuffix(keydumpResponse([4][3]int{
		{1, 1, 1},
		{1, 1, 1},
		{1, 1, 1},
		{0, 1, 1}, // * pressed
	}), "\n"), "\n")

	held, err := parseStarHeld(lines)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !held {
		t.Fatal("expected * to be held")
	}
}

func TestParseStarHeld_NotPressed(t *testing.T) {
	lines := strings.Split(strings.TrimSuffix(keydumpResponse([4][3]int{
		{1, 1, 1},
		{1, 1, 1},
		{1, 1, 1},
		{1, 1, 1}, // nothing pressed
	}), "\n"), "\n")

	held, err := parseStarHeld(lines)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if held {
		t.Fatal("expected * to NOT be held")
	}
}

func TestParseStarHeld_OtherKeyPressed(t *testing.T) {
	// Row 3, col 1 is '0' key. Should not trigger.
	lines := strings.Split(strings.TrimSuffix(keydumpResponse([4][3]int{
		{1, 1, 1},
		{1, 1, 1},
		{1, 1, 1},
		{1, 0, 1}, // 0 key pressed, not *
	}), "\n"), "\n")

	held, err := parseStarHeld(lines)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if held {
		t.Fatal("expected * to NOT be held when 0 key is pressed")
	}
}

func TestParseStarHeld_NoScanLines(t *testing.T) {
	lines := []string{
		"ROWS: R0/GP27=1",
		"COLS: C0/GP24=1",
	}
	_, err := parseStarHeld(lines)
	if err == nil {
		t.Fatal("expected error for missing scan lines")
	}
}

func TestParseStarHeld_MalformedScanLine(t *testing.T) {
	lines := []string{
		"ROWS: R0/GP27=1",
		"COLS: C0/GP24=1",
		"SCAN R0/GP27=LOW: garbage",
		"SCAN R1/GP26=LOW: garbage",
		"SCAN R2/GP21=LOW: garbage",
		"SCAN R3/GP25=LOW: garbage",
	}
	_, err := parseStarHeld(lines)
	if err == nil {
		t.Fatal("expected error for malformed scan line")
	}
}

func TestParseScanLine(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		col     int
		want    bool
		wantErr bool
	}{
		{
			name: "col0 pressed",
			line: "SCAN R3/GP25=LOW: C0=0 C1=1 C2=1",
			col:  0,
			want: true,
		},
		{
			name: "col0 not pressed",
			line: "SCAN R3/GP25=LOW: C0=1 C1=1 C2=1",
			col:  0,
			want: false,
		},
		{
			name: "col1 pressed",
			line: "SCAN R3/GP25=LOW: C0=1 C1=0 C2=1",
			col:  1,
			want: true,
		},
		{
			name: "v1 four columns",
			line: "SCAN R3/GP5=LOW: C0=0 C1=1 C2=1 C3=1",
			col:  0,
			want: true,
		},
		{
			name:    "missing column",
			line:    "SCAN R3/GP25=LOW: C1=1 C2=1",
			col:     0,
			wantErr: true,
		},
		{
			name:    "truncated value",
			line:    "SCAN R3/GP25=LOW: C0=",
			col:     0,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseScanLine(tt.line, tt.col)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRun_StarHeld(t *testing.T) {
	tmpDir := t.TempDir()
	flagPath := filepath.Join(tmpDir, "recovery-mode")

	response := keydumpResponse([4][3]int{
		{1, 1, 1},
		{1, 1, 1},
		{1, 1, 1},
		{0, 1, 1},
	})

	mock := &mockSerial{rx: bytes.NewBufferString(response)}
	rebooted := false

	cfg := config{
		serialDev:        "/dev/serial0",
		baud:             115200,
		recoveryFlagPath: flagPath,
		openSerial: func(string, int) (io.ReadWriteCloser, error) {
			return mock, nil
		},
		reboot: func() error {
			rebooted = true
			return nil
		},
	}

	triggered, err := run(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !triggered {
		t.Fatal("expected recovery to be triggered")
	}
	if !rebooted {
		t.Fatal("expected reboot to be called")
	}
	if !mock.closed {
		t.Fatal("expected serial port to be closed")
	}

	data, err := os.ReadFile(flagPath)
	if err != nil {
		t.Fatalf("recovery flag not written: %v", err)
	}
	if !strings.Contains(string(data), "panic-button") {
		t.Fatalf("unexpected flag content: %q", string(data))
	}
}

func TestRun_NoKeyHeld(t *testing.T) {
	response := keydumpResponse([4][3]int{
		{1, 1, 1},
		{1, 1, 1},
		{1, 1, 1},
		{1, 1, 1},
	})

	mock := &mockSerial{rx: bytes.NewBufferString(response)}

	cfg := config{
		serialDev:        "/dev/serial0",
		baud:             115200,
		recoveryFlagPath: "/nonexistent/recovery-mode",
		openSerial: func(string, int) (io.ReadWriteCloser, error) {
			return mock, nil
		},
		reboot: func() error {
			t.Fatal("reboot should not be called")
			return nil
		},
	}

	triggered, err := run(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if triggered {
		t.Fatal("expected no recovery trigger")
	}
}

func TestRun_SerialOpenFails(t *testing.T) {
	cfg := config{
		serialDev: "/dev/serial0",
		baud:      115200,
		openSerial: func(string, int) (io.ReadWriteCloser, error) {
			return nil, errors.New("device not found")
		},
		reboot: func() error {
			t.Fatal("reboot should not be called")
			return nil
		},
	}

	triggered, err := run(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if triggered {
		t.Fatal("expected no recovery trigger on serial failure")
	}
}

func TestRun_PicoNotResponding(t *testing.T) {
	// Mock that never sends any data (simulates unresponsive Pico).
	mock := &mockSerial{rx: bytes.NewBuffer(nil)}

	cfg := config{
		serialDev: "/dev/serial0",
		baud:      115200,
		openSerial: func(string, int) (io.ReadWriteCloser, error) {
			return mock, nil
		},
		reboot: func() error {
			t.Fatal("reboot should not be called")
			return nil
		},
	}

	triggered, err := run(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if triggered {
		t.Fatal("expected no recovery trigger on timeout")
	}
}

func TestRun_WriteFails(t *testing.T) {
	mock := &mockSerial{
		rx:       bytes.NewBuffer(nil),
		writeErr: errors.New("write error"),
	}

	cfg := config{
		serialDev: "/dev/serial0",
		baud:      115200,
		openSerial: func(string, int) (io.ReadWriteCloser, error) {
			return mock, nil
		},
		reboot: func() error {
			t.Fatal("reboot should not be called")
			return nil
		},
	}

	triggered, err := run(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if triggered {
		t.Fatal("expected no recovery trigger on write failure")
	}
}

func TestRun_MalformedResponse(t *testing.T) {
	mock := &mockSerial{
		rx: bytes.NewBufferString("garbage line 1\ngarbage line 2\ngarbage 3\ngarbage 4\ngarbage 5\ngarbage 6\n"),
	}

	cfg := config{
		serialDev: "/dev/serial0",
		baud:      115200,
		openSerial: func(string, int) (io.ReadWriteCloser, error) {
			return mock, nil
		},
		reboot: func() error {
			t.Fatal("reboot should not be called")
			return nil
		},
	}

	triggered, err := run(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if triggered {
		t.Fatal("expected no recovery trigger on malformed response")
	}
}

func TestRun_PartialResponse(t *testing.T) {
	// Only 3 lines instead of 6.
	mock := &mockSerial{
		rx: bytes.NewBufferString("ROWS: R0/GP27=1\nCOLS: C0/GP24=1\nSCAN R0/GP27=LOW: C0=1\n"),
	}

	cfg := config{
		serialDev: "/dev/serial0",
		baud:      115200,
		openSerial: func(string, int) (io.ReadWriteCloser, error) {
			return mock, nil
		},
		reboot: func() error {
			t.Fatal("reboot should not be called")
			return nil
		},
	}

	triggered, err := run(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if triggered {
		t.Fatal("expected no recovery trigger on partial response")
	}
}

func TestRun_V1FourColumns(t *testing.T) {
	// V1 board has 4 columns. * is still row 3, col 0.
	tmpDir := t.TempDir()
	flagPath := filepath.Join(tmpDir, "recovery-mode")

	var b strings.Builder
	b.WriteString("ROWS: R0/GP2=1 R1/GP3=1 R2/GP4=1 R3/GP5=1\n")
	b.WriteString("COLS: C0/GP6=1 C1/GP7=1 C2/GP8=1 C3/GP9=1\n")
	b.WriteString("SCAN R0/GP2=LOW: C0=1 C1=1 C2=1 C3=1\n")
	b.WriteString("SCAN R1/GP3=LOW: C0=1 C1=1 C2=1 C3=1\n")
	b.WriteString("SCAN R2/GP4=LOW: C0=1 C1=1 C2=1 C3=1\n")
	b.WriteString("SCAN R3/GP5=LOW: C0=0 C1=1 C2=1 C3=1\n")

	mock := &mockSerial{rx: bytes.NewBufferString(b.String())}
	rebooted := false

	cfg := config{
		serialDev:        "/dev/serial0",
		baud:             115200,
		recoveryFlagPath: flagPath,
		openSerial: func(string, int) (io.ReadWriteCloser, error) {
			return mock, nil
		},
		reboot: func() error {
			rebooted = true
			return nil
		},
	}

	triggered, err := run(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !triggered {
		t.Fatal("expected recovery to be triggered for V1 board")
	}
	if !rebooted {
		t.Fatal("expected reboot")
	}
}

func TestReadLines_Timeout(t *testing.T) {
	// Reader that blocks forever.
	r, _ := io.Pipe()
	defer r.Close()

	lines, err := readLines(r, 6, 100*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if len(lines) != 0 {
		t.Fatalf("expected 0 lines, got %d", len(lines))
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("expected timeout in error, got: %v", err)
	}
}
