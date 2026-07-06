package autodeploy

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	store := NewFileStore(path)

	s, err := store.Read()
	if err != nil {
		t.Fatalf("Read on missing file: %v", err)
	}
	if s.LastDeployedTag != "" {
		t.Errorf("expected zero state, got %+v", s)
	}

	s.LastDeployedTag = "server/v1.9.0"
	s.LastDeployedCommitSHA = "abc123"
	s.LastDeployedAt = time.Now().UTC().Truncate(time.Second)
	s.GitHubETag = `W/"etag"`

	if err := store.Write(s); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := store.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.LastDeployedTag != s.LastDeployedTag {
		t.Errorf("LastDeployedTag=%q", got.LastDeployedTag)
	}
	if got.GitHubETag != s.GitHubETag {
		t.Errorf("GitHubETag=%q", got.GitHubETag)
	}
}

func TestStateAtomicWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	store := NewFileStore(path)

	s1 := State{LastDeployedTag: "server/v1.0.0"}
	if err := store.Write(s1); err != nil {
		t.Fatal(err)
	}

	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Fatalf("stale tmp file: %s", e.Name())
		}
	}
}

func TestStateCorruptJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := NewFileStore(path).Read()
	if err == nil {
		t.Fatal("expected error on corrupt JSON")
	}
}

func TestStateConcurrentWrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	store := NewFileStore(path)

	var wg sync.WaitGroup
	for i := range 10 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := State{LastDeployedTag: "server/v1.0.0"}
			s.LastAttemptError = time.Now().String()
			_ = store.Write(s)
		}(i)
	}
	wg.Wait()

	if _, err := store.Read(); err != nil {
		t.Fatalf("Read after concurrent writes: %v", err)
	}
}
