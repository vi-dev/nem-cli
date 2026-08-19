package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const lintFixtureGoPkg = `
schema: 2
name: go
description: The Go programming language
homepage: https://go.dev
license: BSD-3-Clause
artifact:
  url: "https://go.dev/dl/go{{.Version}}.{{.OS}}-{{.Arch}}.tar.gz"
install:
  - extract: {strip: 1}
versions:
  - version: v1.26.5
    sha256:
      darwin/arm64: "aaa"
      darwin/amd64: "bbb"
      linux/arm64: "ccc"
      linux/amd64: "ddd"
`

const lintFixtureGoPkgReservedEnv = lintFixtureGoPkg + `env:
  - name: PATH
    value: "x"
`

func writeLintFixture(t *testing.T, pkgs map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, y := range pkgs {
		pkgDir := filepath.Join(dir, "pkgs", name)
		if err := os.MkdirAll(pkgDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(pkgDir, "pkg.yaml"), []byte(y), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestCatalogLintCleanCatalog(t *testing.T) {
	nemHome := t.TempDir()
	catDir := writeLintFixture(t, map[string]string{"go": lintFixtureGoPkg})

	out, errb, err := runNem(t, nemHome, "catalog", "lint", catDir)
	if err != nil {
		t.Fatalf("lint: %v", err)
	}
	if !strings.Contains(errb, "OK Catalog is clean: no findings") {
		t.Fatalf("stderr = %q, want the clean-summary success line", errb)
	}
	if out != "" {
		t.Fatalf("stdout = %q, want empty", out)
	}
}

func TestCatalogLintCleanSummarySuppressedByQuiet(t *testing.T) {
	nemHome := t.TempDir()
	catDir := writeLintFixture(t, map[string]string{"go": lintFixtureGoPkg})

	out, errb, err := runNem(t, nemHome, "catalog", "lint", catDir, "--quiet")
	if err != nil {
		t.Fatalf("lint: %v", err)
	}
	if strings.Contains(errb, "clean") {
		t.Fatalf("stderr = %q, want no clean summary under --quiet", errb)
	}
	if out != "" {
		t.Fatalf("stdout = %q, want empty", out)
	}
}

func TestCatalogLintDefectiveCatalog(t *testing.T) {
	nemHome := t.TempDir()
	catDir := writeLintFixture(t, map[string]string{"go": lintFixtureGoPkgReservedEnv})

	out, errb, err := runNem(t, nemHome, "catalog", "lint", catDir)
	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("lint error = %v, want *ExitError", err)
	}
	if exitErr.Code != 1 {
		t.Fatalf("exit code = %d, want 1", exitErr.Code)
	}
	if !strings.Contains(errb, "WARN ") || !strings.Contains(errb, "reserved") {
		t.Fatalf("stderr = %q, want the reserved-env finding as a WARN line", errb)
	}
	if out != "" {
		t.Fatalf("stdout = %q, want empty", out)
	}
}

func TestCatalogLintFindingsSurviveQuiet(t *testing.T) {
	nemHome := t.TempDir()
	catDir := writeLintFixture(t, map[string]string{"go": lintFixtureGoPkgReservedEnv})

	_, errb, err := runNem(t, nemHome, "catalog", "lint", catDir, "--quiet")
	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("lint error = %v, want *ExitError", err)
	}
	if exitErr.Code != 1 {
		t.Fatalf("exit code = %d, want 1", exitErr.Code)
	}
	if !strings.Contains(errb, "reserved") {
		t.Fatalf("stderr = %q, want findings despite --quiet", errb)
	}
}

func TestCatalogLintDefaultsToCurrentDir(t *testing.T) {
	nemHome := t.TempDir()
	catDir := writeLintFixture(t, map[string]string{"go": lintFixtureGoPkg})
	chdir(t, catDir)

	_, errb, err := runNem(t, nemHome, "catalog", "lint")
	if err != nil {
		t.Fatalf("lint: %v", err)
	}
	if !strings.Contains(errb, "OK Catalog is clean: no findings") {
		t.Fatalf("stderr = %q, want the clean-summary success line", errb)
	}
}
