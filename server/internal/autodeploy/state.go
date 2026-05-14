package autodeploy

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// AttemptStatus records the outcome of the most recent deploy attempt.
type AttemptStatus string

const (
	StatusInProgress     AttemptStatus = "in_progress"
	StatusSuccess        AttemptStatus = "success"
	StatusFailed         AttemptStatus = "failed"
	StatusFailedReverted AttemptStatus = "failed_reverted"
	StatusCritical       AttemptStatus = "critical"
)

// State is the autodeploy daemon's persistent bookkeeping: what was last
// deployed, the outcome of the latest attempt, and rate-limit state for
// operator email alerts.
type State struct {
	LastDeployedTag       string        `json:"last_deployed_tag,omitempty"`
	LastDeployedCommitSHA string        `json:"last_deployed_commit_sha,omitempty"`
	LastDeployedAt        time.Time     `json:"last_deployed_at,omitempty"`
	LastAttemptTag        string        `json:"last_attempt_tag,omitempty"`
	LastAttemptStatus     AttemptStatus `json:"last_attempt_status,omitempty"`
	LastAttemptError      string        `json:"last_attempt_error,omitempty"`
	LastAttemptAt         time.Time     `json:"last_attempt_at,omitempty"`
	LastEmailAt           time.Time     `json:"last_email_at,omitempty"`
	LastEmailErrorClass   string        `json:"last_email_error_class,omitempty"`
	GitHubETag            string        `json:"github_etag,omitempty"`
}

// Store persists and retrieves autodeploy State between daemon restarts.
type Store interface {
	Read() (State, error)
	Write(State) error
}

// FileStore is a file-backed Store that serializes State to JSON with an
// advisory file lock to prevent concurrent daemon instances from racing.
type FileStore struct {
	path string
}

// NewFileStore returns a FileStore that reads and writes state at path.
func NewFileStore(path string) *FileStore {
	return &FileStore{path: path}
}

func (fs *FileStore) Read() (State, error) {
	f, err := os.Open(fs.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return State{}, nil
		}
		return State{}, fmt.Errorf("open state: %w", err)
	}
	defer func() { _ = f.Close() }()

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH); err != nil {
		return State{}, fmt.Errorf("flock(sh): %w", err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN) //nolint:errcheck

	var s State
	if err := json.NewDecoder(f).Decode(&s); err != nil {
		return State{}, fmt.Errorf("decode state: %w", err)
	}
	return s, nil
}

func (fs *FileStore) Write(s State) error {
	dir := filepath.Dir(fs.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir state dir: %w", err)
	}

	lockFile, err := os.OpenFile(fs.path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open lock: %w", err)
	}
	defer func() { _ = lockFile.Close() }()
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("flock(ex): %w", err)
	}
	defer syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN) //nolint:errcheck

	tmp, err := os.CreateTemp(dir, ".state-*.tmp")
	if err != nil {
		return fmt.Errorf("create tmp: %w", err)
	}
	tmpPath := tmp.Name()
	removeTmp := true
	defer func() {
		if removeTmp {
			_ = os.Remove(tmpPath)
		}
	}()

	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(s); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("encode: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close tmp: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		return fmt.Errorf("chmod tmp: %w", err)
	}
	if err := os.Rename(tmpPath, fs.path); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	removeTmp = false
	return nil
}
