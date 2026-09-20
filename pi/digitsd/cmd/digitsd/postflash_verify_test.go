package main

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/justinlindh/digits/pi/digitsd/internal/updater"
)

type fakePicoVerifier struct {
	pings    []error
	versions []struct {
		version string
		commit  string
		err     error
	}
	pingCalls    int
	versionCalls int
}

func (f *fakePicoVerifier) Ping() error {
	i := f.pingCalls
	f.pingCalls++
	if i >= len(f.pings) {
		return errors.New("no PONG")
	}
	return f.pings[i]
}

func (f *fakePicoVerifier) QueryVersion() (string, string, error) {
	i := f.versionCalls
	f.versionCalls++
	if i >= len(f.versions) {
		return "", "", errors.New("no VERSION response")
	}
	r := f.versions[i]
	return r.version, r.commit, r.err
}

func TestAwaitPicoFirmwareRejectsSilentAndOldVersion(t *testing.T) {
	fake := &fakePicoVerifier{
		pings: []error{errors.New("timeout"), nil, nil},
		versions: []struct {
			version string
			commit  string
			err     error
		}{{version: "1.8.0"}, {version: "1.8.0"}},
	}

	_, _, err := awaitPicoFirmware(fake, "1.9.0", 3, 0)
	if err == nil || !strings.Contains(err.Error(), `expected "1.9.0"`) {
		t.Fatalf("awaitPicoFirmware() error = %v, want target-version mismatch", err)
	}
}

func TestAwaitPicoFirmwareAcceptsTargetAfterRetries(t *testing.T) {
	fake := &fakePicoVerifier{
		pings: []error{errors.New("booting"), nil, nil},
		versions: []struct {
			version string
			commit  string
			err     error
		}{{version: "1.8.0"}, {version: "1.9.0", commit: "abc1234"}},
	}

	version, commit, err := awaitPicoFirmware(fake, "1.9.0", 3, 0)
	if err != nil {
		t.Fatalf("awaitPicoFirmware() error: %v", err)
	}
	if version != "1.9.0" || commit != "abc1234" {
		t.Fatalf("got %q/%q, want 1.9.0/abc1234", version, commit)
	}
	if fake.pingCalls != 3 || fake.versionCalls != 2 {
		t.Fatalf("calls = ping %d, version %d; want 3, 2", fake.pingCalls, fake.versionCalls)
	}
}

type fakeUpdateRunner struct{}

func (fakeUpdateRunner) CheckVersion(string, string) (*updater.CheckResult, error) {
	return &updater.CheckResult{FWAvailable: true, FWVersion: "1.9.0", FWURL: "fake", FWSHA256: strings.Repeat("a", 64)}, nil
}
func (fakeUpdateRunner) Download(string, string, string) (string, error) { return "/tmp/fake.elf", nil }
func (fakeUpdateRunner) ApplyFirmwareUpdate(string) error                { return nil }
func (fakeUpdateRunner) ApplyPiUpdate(string, string) error              { return nil }

func TestFirmwareUpdateReportsFailureWithoutVerifiedTarget(t *testing.T) {
	var statuses []string
	err := runTargetedUpdateWithRunner(fakeUpdateRunner{}, "", "1.9.0", true,
		func(status, _ string) { statuses = append(statuses, status) },
		func(string) error { return errors.New("Pico still reports 1.8.0") })
	if err == nil {
		t.Fatal("runTargetedUpdateWithRunner() returned nil after verification failure")
	}
	if reflect.DeepEqual(statuses, []string{"downloading", "applying", "failed"}) == false {
		t.Fatalf("statuses = %v, want downloading/applying/failed", statuses)
	}
}

func TestFirmwareUpdateReportsSuccessOnlyAfterVerification(t *testing.T) {
	var events []string
	err := runTargetedUpdateWithRunner(fakeUpdateRunner{}, "", "1.9.0", true,
		func(status, _ string) { events = append(events, status) },
		func(expected string) error {
			events = append(events, "verified:"+expected)
			return nil
		})
	if err != nil {
		t.Fatalf("runTargetedUpdateWithRunner() error: %v", err)
	}
	want := []string{"downloading", "applying", "verified:1.9.0", "success"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestAwaitPicoFirmwareDoesNotLeakOnRetryDelay(t *testing.T) {
	fake := &fakePicoVerifier{pings: []error{nil}, versions: []struct {
		version string
		commit  string
		err     error
	}{{version: "2.0.0"}}}
	start := time.Now()
	_, _, err := awaitPicoFirmware(fake, "2.0.0", 2, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) >= time.Second {
		t.Fatal("successful verification slept after completion")
	}
}
