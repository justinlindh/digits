package subsystem

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/justinlindh/digits/pi/digitsd/internal/phone"
)

type SerialConfig struct {
	Device string
	Baud   int
	// FlashOnFail, when set, is called once if the Pico cannot be reached on
	// the initial probe. It flashes the bundled firmware so a board whose
	// RP2040 was never programmed (e.g. a fresh device that boots straight
	// into setup/AP mode, which never runs the normal-mode reflash path) can
	// still bring up the serial link. nil disables the fallback.
	FlashOnFail func(reason string) error
}

type SerialModule struct {
	cfg    SerialConfig
	port   *phone.SerialPort
	status ModuleStatus
}

func NewSerialModule(cfg SerialConfig) *SerialModule {
	return &SerialModule{cfg: cfg, status: ModuleStatus{State: StatePending}}
}

func (s *SerialModule) Name() string { return "serial" }

func (s *SerialModule) Init(ctx context.Context) error {
	s.status.State = StateInitializing

	// probe opens the port and PINGs the Pico, stashing the port on success.
	probe := func() bool {
		sp, err := phone.OpenSerial(s.cfg.Device, s.cfg.Baud, slog.Default())
		if err != nil {
			slog.Warn("subsystem serial: open failed", "error", err)
			return false
		}
		if err = sp.Ping(); err != nil {
			slog.Warn("subsystem serial: ping failed", "error", err)
			_ = sp.Close()
			return false
		}
		s.port = sp
		return true
	}

	// Without a flash fallback (recovery mode) keep the original 10-attempt
	// budget. With one (setup mode) probe only briefly before flashing: a
	// healthy Pico answers on the first attempt, so a long probe just delays
	// the flash a virgin board actually needs.
	firstAttempts := 10
	if s.cfg.FlashOnFail != nil {
		firstAttempts = 3
	}

	if attemptSerialBringup(probe, s.cfg.FlashOnFail, firstAttempts, 10, func() {
		time.Sleep(500 * time.Millisecond)
	}) {
		s.status.State = StateReady
		return nil
	}

	err := fmt.Errorf("failed to open and ping after retries")
	s.status = ModuleStatus{State: StateFailed, Message: err.Error()}
	return err
}

// attemptSerialBringup probes for a reachable Pico, flashing the firmware once
// as a fallback if the initial probe fails and flash is non-nil. It returns
// true as soon as a probe succeeds. The probe/flash/sleep funcs are injected so
// the orchestration can be unit-tested without real hardware.
func attemptSerialBringup(probe func() bool, flash func(reason string) error, firstAttempts, afterFlashAttempts int, sleep func()) bool {
	for i := 0; i < firstAttempts; i++ {
		if probe() {
			return true
		}
		if i < firstAttempts-1 {
			sleep()
		}
	}

	if flash == nil {
		return false
	}

	slog.Warn("subsystem serial: Pico unreachable, flashing firmware")
	if err := flash("serial-init"); err != nil {
		slog.Error("subsystem serial: flash attempt failed", "error", err)
		return false
	}

	for i := 0; i < afterFlashAttempts; i++ {
		if probe() {
			slog.Info("subsystem serial: Pico reachable after flash")
			return true
		}
		if i < afterFlashAttempts-1 {
			sleep()
		}
	}
	return false
}

func (s *SerialModule) Port() *phone.SerialPort { return s.port }
func (s *SerialModule) Status() ModuleStatus    { return s.status }

func (s *SerialModule) Shutdown(ctx context.Context) error {
	if s.port != nil {
		return s.port.Close()
	}
	return nil
}
