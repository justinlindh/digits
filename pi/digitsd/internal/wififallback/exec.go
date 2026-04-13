package wififallback

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// runCmd executes name with args, enforcing a timeout. On success it returns
// stdout. On failure it returns an error that wraps the underlying exec error
// and includes the trimmed stderr for diagnostics.
func runCmd(timeout time.Duration, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s %v failed: %w: %s", name, args, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}
