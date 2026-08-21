package usage

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vi-dev/nem-cli/internal/home"
)

func testHome(t *testing.T) home.Home {
	t.Helper()
	root := t.TempDir()
	return home.Resolve(func(k string) string {
		if k == "NEM_HOME" {
			return root
		}
		return ""
	})
}

func TestLoadMissingFileIsEmpty(t *testing.T) {
	idx := Load(testHome(t))
	if len(idx) != 0 {
		t.Fatalf("absent index should be empty, got %v", idx)
	}
}

func TestLoadCorruptFileIsEmpty(t *testing.T) {
	h := testHome(t)
	if err := os.MkdirAll(h.Root(), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(h.Usage(), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if idx := Load(h); len(idx) != 0 {
		t.Fatalf("corrupt index should be empty, got %v", idx)
	}
}

func TestStampThenLoad(t *testing.T) {
	h := testHome(t)
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	Stamp(h, now, []string{Key("go", "1.26.6")})

	got, ok := Load(h).LastUsed("go", "1.26.6")
	if !ok {
		t.Fatal("go@1.26.6 should be stamped")
	}
	if !got.Equal(now) {
		t.Fatalf("stamp = %v, want %v", got, now)
	}
}

func TestStampDebouncesWithinTheHour(t *testing.T) {
	h := testHome(t)
	first := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	Stamp(h, first, []string{Key("go", "1.26.6")})

	// Set mtime to a known past time to detect if Stamp rewrites the file.
	knownTime := first.Add(-time.Hour)
	if err := os.Chtimes(h.Usage(), knownTime, knownTime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	// 59 minutes later the stamp is still fresh, so nothing is rewritten.
	Stamp(h, first.Add(59*time.Minute), []string{Key("go", "1.26.6")})
	got, _ := Load(h).LastUsed("go", "1.26.6")
	if !got.Equal(first) {
		t.Fatalf("debounced stamp changed to %v, want %v", got, first)
	}
	if info2, _ := os.Stat(h.Usage()); !info2.ModTime().Equal(knownTime) {
		t.Fatal("debounced stamp rewrote the file")
	}
}

func TestStampRewritesAfterTheHour(t *testing.T) {
	h := testHome(t)
	first := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	later := first.Add(61 * time.Minute)
	Stamp(h, first, []string{Key("go", "1.26.6")})
	Stamp(h, later, []string{Key("go", "1.26.6")})

	got, _ := Load(h).LastUsed("go", "1.26.6")
	if !got.Equal(later) {
		t.Fatalf("stamp = %v, want %v", got, later)
	}
}

func TestStampNeverErrorsOnUnwritableHome(t *testing.T) {
	// A file where the root directory should be makes every write fail.
	root := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(root, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	h := home.Resolve(func(k string) string {
		if k == "NEM_HOME" {
			return root
		}
		return ""
	})
	Stamp(h, time.Now(), []string{Key("go", "1.26.6")}) // must not panic
	if len(Load(h)) != 0 {
		t.Fatal("unwritable home should load empty")
	}
}

func TestLastUsedUnknownVersion(t *testing.T) {
	if _, ok := (Index{}).LastUsed("go", "1.26.6"); ok {
		t.Fatal("unknown version must report ok=false")
	}
}

func TestByKey(t *testing.T) {
	now := time.Now()
	idx := Index{Key("go", "1.26.6"): now}
	got, ok := idx.ByKey(Key("go", "1.26.6"))
	if !ok || !got.Equal(now) {
		t.Fatalf("ByKey = %v, %v; want %v, true", got, ok, now)
	}
	if _, ok := idx.ByKey("absent@1"); ok {
		t.Error("unknown key must report ok=false")
	}
}

func TestPruneDropsVanishedVersions(t *testing.T) {
	now := time.Now()
	idx := Index{
		Key("go", "1.26.6"): now,
		Key("go", "1.26.5"): now,
	}
	got := idx.Prune([]string{Key("go", "1.26.6")})
	if len(got) != 1 {
		t.Fatalf("Prune kept %d entries, want 1", len(got))
	}
	if _, ok := got.LastUsed("go", "1.26.6"); !ok {
		t.Fatal("Prune dropped a surviving version")
	}
}
