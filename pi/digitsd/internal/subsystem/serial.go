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
	logger := slog.Default()

	for attempt := 1; attempt <= 10; attempt++ {
		sp, err := phone.OpenSerial(s.cfg.Device, s.cfg.Baud, logger)
		if err != nil {
			slog.Warn("subsystem serial: open failed", "attempt", attempt, "error", err)
			time.Sleep(500 * time.Millisecond)
			continue
		}
		if err = sp.Ping(); err != nil {
			slog.Warn("subsystem serial: ping failed", "attempt", attempt, "error", err)
			_ = sp.Close()
			time.Sleep(500 * time.Millisecond)
			continue
		}
		s.port = sp
		s.status.State = StateReady
		return nil
	}

	err := fmt.Errorf("failed to open and ping after 10 attempts")
	s.status = ModuleStatus{State: StateFailed, Message: err.Error()}
	return err
}

func (s *SerialModule) Port() *phone.SerialPort { return s.port }
func (s *SerialModule) IsReady() bool           { return s.status.State == StateReady }
func (s *SerialModule) Status() ModuleStatus    { return s.status }

func (s *SerialModule) Shutdown(ctx context.Context) error {
	if s.port != nil {
		return s.port.Close()
	}
	return nil
}

func (s *SerialModule) HealthCheck() error {
	if s.port == nil {
		return fmt.Errorf("not initialized")
	}
	return s.port.Ping()
}
