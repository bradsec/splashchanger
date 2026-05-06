package lockfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAcquireAndRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.lock")
	lock, err := AcquirePath(path)
	if err != nil {
		t.Fatalf("failed to acquire lock: %v", err)
	}
	defer lock.Release()

	// Verify lock file exists and contains PID.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("could not read lock file: %v", err)
	}
	if len(data) == 0 {
		t.Error("lock file is empty, expected PID")
	}
}

func TestAcquireTwiceFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.lock")
	lock1, err := AcquirePath(path)
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	defer lock1.Release()

	_, err = AcquirePath(path)
	if err == nil {
		t.Error("expected error for second acquire, got nil")
	}
	if !strings.Contains(err.Error(), "already running") {
		t.Errorf("expected 'already running' error, got: %v", err)
	}
}

func TestReleaseThenReacquire(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.lock")
	lock1, err := AcquirePath(path)
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	lock1.Release()

	lock2, err := AcquirePath(path)
	if err != nil {
		t.Fatalf("reacquire after release failed: %v", err)
	}
	lock2.Release()
}

func TestReleaseNil(t *testing.T) {
	var l *Lock
	if err := l.Release(); err != nil {
		t.Errorf("Release on nil lock should not error: %v", err)
	}
}
