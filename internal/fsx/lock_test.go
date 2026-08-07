package fsx

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLockCreatesAndReleases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")

	release, err := Lock(path)
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("lock file not created: %v", err)
	}
	release()

	// reacquirable after release
	release2, err := Lock(path)
	if err != nil {
		t.Fatalf("re-Lock: %v", err)
	}
	release2()

	// lock file is never deleted
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("lock file deleted: %v", err)
	}
}

func TestLockExcludesConcurrentAcquirer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	release, err := Lock(path)
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}

	acquired := make(chan struct{})
	go func() {
		r2, err := Lock(path) // must block until release()
		if err != nil {
			t.Errorf("second Lock: %v", err)
			close(acquired)
			return
		}
		close(acquired)
		r2()
	}()

	select {
	case <-acquired:
		t.Fatal("second Lock acquired while first still held")
	case <-time.After(100 * time.Millisecond):
		// still blocked: correct
	}
	release()
	select {
	case <-acquired:
		// acquired after release: correct
	case <-time.After(2 * time.Second):
		t.Fatal("second Lock never acquired after release")
	}
}
