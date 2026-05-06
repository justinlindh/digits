package autodeploy

import (
	"context"
	"strings"
	"testing"
)

func TestExecRunner(t *testing.T) {
	r := NewExecRunner()
	if err := r.Run(context.Background(), RunSpec{
		Name: "true",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestExecRunnerNonZeroExit(t *testing.T) {
	r := NewExecRunner()
	err := r.Run(context.Background(), RunSpec{Name: "sh", Args: []string{"-c", "echo nope >&2; exit 1"}})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "nope") {
		t.Errorf("expected stderr in error, got %q", err.Error())
	}
}
