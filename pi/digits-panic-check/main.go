// digits-panic-check reads the keypad matrix from the Pico over UART and
// enters recovery mode if the * key is held during early boot.
//
// Exit codes:
//   0: no key held, or Pico not responding (normal boot continues)
//   1: * key detected, recovery flag written, reboot initiated
package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultSerialDev  = "/dev/serial0"
	defaultBaud       = 115200
	recoveryFlagPath  = "/data/digits/recovery-mode"
	keydumpTimeout    = 2 * time.Second
	starKeyRow        = 3
	starKeyCol        = 0
	keydumpTotalLines = 6 // 1 ROWS + 1 COLS + 4 SCAN
)

// serialOpener abstracts serial port construction for testing.
type serialOpener func(device string, baud int) (io.ReadWriteCloser, error)

// rebootFunc abstracts the reboot call for testing.
type rebootFunc func() error

// config holds runtime settings, injectable for testing.
type config struct {
	serialDev        string
	baud             int
	recoveryFlagPath string
	openSerial       serialOpener
	reboot           rebootFunc
}

func defaultConfig() config {
	return config{
		serialDev:        defaultSerialDev,
		baud:             defaultBaud,
		recoveryFlagPath: recoveryFlagPath,
		openSerial:       openSerial,
		reboot: func() error {
			return exec.Command("systemctl", "reboot").Run()
		},
	}
}

// run executes the panic check. Returns true if recovery was triggered.
func run(cfg config) (bool, error) {
	port, err := cfg.openSerial(cfg.serialDev, cfg.baud)
	if err != nil {
		// Serial port not available: don't block boot.
		log.Printf("cannot open serial port: %v (continuing normal boot)", err)
		return false, nil
	}
	defer port.Close()

	// Send KEYDUMP command.
	if _, err := port.Write([]byte("KEYDUMP\n")); err != nil {
		log.Printf("cannot write KEYDUMP: %v (continuing normal boot)", err)
		return false, nil
	}

	// Read response lines with a timeout.
	lines, err := readLines(port, keydumpTotalLines, keydumpTimeout)
	if err != nil {
		log.Printf("KEYDUMP read failed: %v (continuing normal boot)", err)
		return false, nil
	}

	// Parse scan lines and check if * is held.
	held, err := parseStarHeld(lines)
	if err != nil {
		log.Printf("KEYDUMP parse failed: %v (continuing normal boot)", err)
		return false, nil
	}

	if !held {
		log.Println("no panic key held, continuing normal boot")
		return false, nil
	}

	log.Println("* key held: entering recovery mode")

	// Write recovery flag.
	flagDir := filepath.Dir(cfg.recoveryFlagPath)
	if err := os.MkdirAll(flagDir, 0755); err != nil {
		return false, fmt.Errorf("mkdir %s: %w", flagDir, err)
	}
	if err := os.WriteFile(cfg.recoveryFlagPath, []byte("panic-button\n"), 0644); err != nil {
		return false, fmt.Errorf("write recovery flag: %w", err)
	}

	// Close serial port before reboot so digitsd does not get EBUSY.
	port.Close()

	if err := cfg.reboot(); err != nil {
		return true, fmt.Errorf("reboot: %w", err)
	}
	return true, nil
}

// readLines reads up to count newline-delimited lines from r within the
// given timeout. Returns whatever lines were collected if the timeout fires
// before all lines arrive.
func readLines(r io.Reader, count int, timeout time.Duration) ([]string, error) {
	type result struct {
		line string
		err  error
	}

	ch := make(chan result, count)
	go func() {
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			ch <- result{line: scanner.Text()}
		}
		if err := scanner.Err(); err != nil {
			ch <- result{err: err}
		}
		close(ch)
	}()

	var lines []string
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for len(lines) < count {
		select {
		case res, ok := <-ch:
			if !ok {
				return lines, fmt.Errorf("serial closed after %d lines (expected %d)", len(lines), count)
			}
			if res.err != nil {
				return lines, res.err
			}
			lines = append(lines, res.line)
		case <-timer.C:
			return lines, fmt.Errorf("timeout after %d lines (expected %d)", len(lines), count)
		}
	}
	return lines, nil
}

// parseStarHeld checks the KEYDUMP output for the * key (row 3, col 0).
// A column value of 0 means the key is pressed.
//
// Expected format (6 lines):
//
//	ROWS: R0/GP27=1 R1/GP26=1 ...
//	COLS: C0/GP24=1 C1/GP23=1 ...
//	SCAN R0/GP27=LOW: C0=1 C1=1 C2=1
//	SCAN R1/GP26=LOW: C0=1 C1=1 C2=1
//	SCAN R2/GP21=LOW: C0=1 C1=1 C2=1
//	SCAN R3/GP25=LOW: C0=0 C1=1 C2=1   <-- C0=0 means * is pressed
func parseStarHeld(lines []string) (bool, error) {
	// Find the SCAN line for row 3.
	scanPrefix := fmt.Sprintf("SCAN R%d/", starKeyRow)
	for _, line := range lines {
		if !strings.HasPrefix(line, scanPrefix) {
			continue
		}
		return parseScanLine(line, starKeyCol)
	}
	return false, fmt.Errorf("no SCAN line found for row %d", starKeyRow)
}

// parseScanLine checks whether the given column reads 0 (pressed) in a
// SCAN line like "SCAN R3/GP25=LOW: C0=0 C1=1 C2=1".
func parseScanLine(line string, col int) (bool, error) {
	target := fmt.Sprintf("C%d=", col)
	idx := strings.Index(line, target)
	if idx < 0 {
		return false, fmt.Errorf("column C%d not found in %q", col, line)
	}
	valPos := idx + len(target)
	if valPos >= len(line) {
		return false, fmt.Errorf("no value after C%d= in %q", col, line)
	}
	return line[valPos] == '0', nil
}

func main() {
	log.SetFlags(0)
	log.SetPrefix("digits-panic-check: ")

	cfg := defaultConfig()
	triggered, err := run(cfg)
	if err != nil {
		log.Fatalf("fatal: %v", err)
	}
	if triggered {
		os.Exit(1)
	}
}
