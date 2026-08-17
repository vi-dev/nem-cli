package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vi-dev/nem-cli/internal/spec"
)

// fmtFixture reuses lintFixtureGoPkg, which is not canonical: it starts
// with a leading blank line that spec.Format strips.
const fmtFixture = lintFixtureGoPkg

func TestCatalogFmtRewritesToCanonical(t *testing.T) {
	nemHome := t.TempDir()
	dir := writeLintFixture(t, map[string]string{"go": fmtFixture})
	path := filepath.Join(dir, "pkgs", "go", "pkg.yaml")

	if _, _, err := runNem(t, nemHome, "catalog", "fmt", dir); err != nil {
		t.Fatalf("fmt: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	canon, err := spec.Format(data)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(canon) {
		t.Fatal("file is not canonical after fmt")
	}
	// Second run is a no-op: file content unchanged.
	before, _ := os.ReadFile(path)
	if _, _, err := runNem(t, nemHome, "catalog", "fmt", dir); err != nil {
		t.Fatalf("second fmt: %v", err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("second fmt changed the file")
	}
}

func TestCatalogFmtCheck(t *testing.T) {
	nemHome := t.TempDir()
	dir := writeLintFixture(t, map[string]string{"go": fmtFixture})
	path := filepath.Join(dir, "pkgs", "go", "pkg.yaml")

	// Canonicalize first, then dirty the file with an extra blank line.
	if _, _, err := runNem(t, nemHome, "catalog", "fmt", dir); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	os.WriteFile(path, append([]byte("\n"), data...), 0o644)

	out, _, err := runNem(t, nemHome, "catalog", "fmt", "--check", dir)
	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("fmt --check error = %v, want *ExitError", err)
	}
	if exitErr.Code != 1 {
		t.Fatalf("exit code = %d, want 1", exitErr.Code)
	}
	if !strings.Contains(out, path) {
		t.Fatalf("stdout = %q, want the dirty path listed", out)
	}
	after, _ := os.ReadFile(path)
	if string(after) != "\n"+string(data) {
		t.Fatal("--check must not rewrite files")
	}
}
