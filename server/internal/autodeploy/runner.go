package autodeploy

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
)

type RunSpec struct {
	Name  string
	Args  []string
	Dir   string
	Env   []string
	Stdin []byte
}

type RunOutput struct {
	Stdout []byte
	Stderr []byte
}

type Runner interface {
	Run(ctx context.Context, spec RunSpec) (RunOutput, error)
}

type ExecRunner struct{}

func NewExecRunner() *ExecRunner { return &ExecRunner{} }

func (ExecRunner) Run(ctx context.Context, spec RunSpec) (RunOutput, error) {
	cmd := exec.CommandContext(ctx, spec.Name, spec.Args...)
	cmd.Dir = spec.Dir
	if len(spec.Env) > 0 {
		cmd.Env = append(os.Environ(), spec.Env...)
	}
	if len(spec.Stdin) > 0 {
		cmd.Stdin = bytes.NewReader(spec.Stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	out := RunOutput{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	if err != nil {
		return out, fmt.Errorf("%s %v: %w (stderr=%q)", spec.Name, spec.Args, err, stderr.String())
	}
	return out, nil
}
