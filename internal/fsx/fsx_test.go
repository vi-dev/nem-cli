package fsx

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "f.txt") // parent dir must be created

	if err := WriteAtomic(path, []byte("one"), 0o644); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "one" {
		t.Fatalf("read back: %q, %v", got, err)
	}

	// overwrite existing
	if err := WriteAtomic(path, []byte("two"), 0o644); err != nil {
		t.Fatalf("WriteAtomic overwrite: %v", err)
	}
	got, _ = os.ReadFile(path)
	if string(got) != "two" {
		t.Fatalf("after overwrite: %q", got)
	}

	// no temp litter
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("leftover temp files: %v", entries)
	}
}
