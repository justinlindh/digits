package watchdog

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWatchdog_PetsFile(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "watchdog")
	f, err := os.Create(tmp)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	w, err := Open(tmp)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer w.Close()

	if err := w.Pet(); err != nil {
		t.Fatalf("Pet: %v", err)
	}

	data, _ := os.ReadFile(tmp)
	if len(data) == 0 {
		t.Error("expected watchdog file to have data after pet")
	}
}

func TestWatchdog_StartStop(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "watchdog")
	f, _ := os.Create(tmp)
	f.Close()

	w, err := Open(tmp)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	w.Start(50 * time.Millisecond)
	time.Sleep(150 * time.Millisecond)
	w.Close()

	info, _ := os.Stat(tmp)
	if info.Size() == 0 {
		t.Error("expected watchdog file to have data after start/stop cycle")
	}
}

func TestWatchdog_OpenNonexistent(t *testing.T) {
	_, err := Open("/nonexistent/watchdog")
	if err == nil {
		t.Error("expected error opening nonexistent path")
	}
}
