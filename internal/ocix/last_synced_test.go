package ocix

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLastSynced(t *testing.T) {
	store := t.TempDir()
	if _, err := LastSynced(store); err == nil {
		t.Fatal("want error for a never-synced store")
	}

	path := filepath.Join(store, "index.json")
	if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}

	ts, err := LastSynced(store)
	if err != nil {
		t.Fatalf("LastSynced: %v", err)
	}
	if d := ts.Sub(old); d < -time.Second || d > time.Second {
		t.Fatalf("LastSynced = %v, want ~%v", ts, old)
	}
}
