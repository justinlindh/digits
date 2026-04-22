package autodeploy

import (
	"context"
	"strings"
	"testing"
)

func TestExecRunner(t *testing.T) {
	r := NewExecRunner()
	out, err := r.Run(context.Background(), RunSpec{
		Name: "echo",
		Args: []string{"hello"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out.Stdout), "hello") {
		t.Errorf("stdout=%q", string(out.Stdout))
	}
}

func TestExecRunnerNonZeroExit(t *testing.T) {
	r := NewExecRunner()
	_, err := r.Run(context.Background(), RunSpec{Name: "false"})
	if err == nil {
		t.Fatal("expected error")
	}
}
