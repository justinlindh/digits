package subsystem

import (
	"context"
	"log/slog"
	"os"
	"syscall"
)

type MountsModule struct {
	ready bool
}

func NewMountsModule() *MountsModule {
	return &MountsModule{}
}

func (m *MountsModule) Name() string { return "mounts" }

func (m *MountsModule) Init(ctx context.Context) error {
	_ = os.MkdirAll("/tmp", 0755)
	if err := syscall.Mount("tmpfs", "/tmp", "tmpfs", 0, "size=64M"); err != nil {
		slog.Warn("mounts: /tmp mount failed (may already be mounted)", "error", err)
	}

	_ = os.MkdirAll("/data", 0755)
	if err := syscall.Mount("/dev/mmcblk0p4", "/data", "ext4", 0, ""); err != nil {
		slog.Warn("mounts: /data mount failed (non-fatal)", "error", err)
	}

	m.ready = true
	return nil
}

func (m *MountsModule) IsReady() bool                      { return m.ready }
func (m *MountsModule) Shutdown(ctx context.Context) error { return nil }
