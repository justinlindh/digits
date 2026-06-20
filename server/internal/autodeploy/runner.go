// Package autodeploy implements the automatic update loop for the autodeploy
// command. It polls GitHub Releases for new tags and, on a new release, drives
// a Docker Compose deploy (login, pull, up, health-check verify) of the signald
// stack, reverting to the previous version if the new release fails its health
// check.
package autodeploy

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
)

// RunSpec describes a child process invocation for Runner.Run.
type RunSpec struct {
	Name  string
	Args  []string
	Dir   string
	Env   []string
	Stdin []byte
}

// Runner executes shell commands on behalf of the Deployer. The interface
// allows tests to inject a fake without spawning real processes.
type Runner interface {
	Run(ctx context.Context, spec RunSpec) error
}

// ExecRunner is the production Runner that delegates to os/exec.
type ExecRunner struct{}

// NewExecRunner returns an ExecRunner ready for use.
func NewExecRunner() *ExecRunner { return &ExecRunner{} }

func (ExecRunner) Run(ctx context.Context, spec RunSpec) error {
	cmd := exec.CommandContext(ctx, spec.Name, spec.Args...)
	cmd.Dir = spec.Dir
	if len(spec.Env) > 0 {
		cmd.Env = append(os.Environ(), spec.Env...)
	}
	if len(spec.Stdin) > 0 {
		cmd.Stdin = bytes.NewReader(spec.Stdin)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %v: %w (stderr=%q)", spec.Name, spec.Args, err, stderr.String())
	}
	return nil
}
