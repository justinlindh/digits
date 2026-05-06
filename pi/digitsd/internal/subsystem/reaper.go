package subsystem

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// ReaperModule reaps zombie child processes when running as PID 1.
type ReaperModule struct {
	status ModuleStatus
	stop   chan struct{}
}

func NewReaperModule() *ReaperModule {
	return &ReaperModule{
		status: ModuleStatus{State: StatePending},
		stop:   make(chan struct{}),
	}
}

func (r *ReaperModule) Name() string { return "reaper" }

func (r *ReaperModule) Init(ctx context.Context) error {
	r.status.State = StateInitializing

	sigchld := make(chan os.Signal, 1)
	signal.Notify(sigchld, syscall.SIGCHLD)

	go func() {
		var ws syscall.WaitStatus
		for {
			select {
			case <-sigchld:
				for {
					pid, err := syscall.Wait4(-1, &ws, syscall.WNOHANG, nil)
					if pid <= 0 || err != nil {
						break
					}
				}
			case <-r.stop:
				signal.Stop(sigchld)
				return
			}
		}
	}()

	r.status.State = StateReady
	return nil
}

func (r *ReaperModule) IsReady() bool                      { return r.status.State == StateReady }
func (r *ReaperModule) Status() ModuleStatus               { return r.status }
func (r *ReaperModule) Shutdown(ctx context.Context) error {
	close(r.stop)
	return nil
}
