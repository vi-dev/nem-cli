package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const infoPkgYAML = `
schema: 2
name: tool
description: a fine tool
homepage: https://example.com/tool
license: MIT
platforms: [darwin/arm64, linux/amd64]
bins: [bin, sbin]
artifact:
  oci: ":{{.Version}}"
install:
  - extract: {}
versions:
  - v2.1.0
  - v1.9.0
`

// writeInfoDirCatalog writes a dir catalog rooted at a temp dir holding one
// package per name/yaml pair.
func writeInfoDirCatalog(t *testing.T, pkgs map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, y := range pkgs {
		dir := filepath.Join(root, "pkgs", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "pkg.yaml"), []byte(y), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestInfoShowsPackageDetails(t *testing.T) {
	nemHomeDir := t.TempDir()
	catalogRoot := writeInfoDirCatalog(t, map[string]string{"tool": infoPkgYAML})
	if _, _, err := runNem(t, nemHomeDir, "catalog", "add", "dev", catalogRoot); err != nil {
		t.Fatal(err)
	}

	out, _, err := runNem(t, nemHomeDir, "info", "tool")
	if err != nil {
		t.Fatalf("info: %v", err)
	}

	for _, want := range []string{
		"name:", "tool",
		"catalog:", "dev",
		"description:", "a fine tool",
		"homepage:", "https://example.com/tool",
		"license:", "MIT",
		"platforms:", "darwin/arm64, linux/amd64",
		"bins:", "bin, sbin",
		"versions:", "v2.1.0, v1.9.0",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("info output missing %q:\n%s", want, out)
		}
	}
	if strings.Index(out, "v2.1.0") > strings.Index(out, "v1.9.0") {
		t.Fatalf("versions must list newest first:\n%s", out)
	}
}

func TestInfoResolvesCatalogQualifiedKey(t *testing.T) {
	nemHomeDir := t.TempDir()
	firstYAML := strings.Replace(infoPkgYAML, "a fine tool", "first-desc", 1)
	secondYAML := strings.Replace(infoPkgYAML, "a fine tool", "second-desc", 1)
	firstRoot := writeInfoDirCatalog(t, map[string]string{"tool": firstYAML})
	secondRoot := writeInfoDirCatalog(t, map[string]string{"tool": secondYAML})
	if _, _, err := runNem(t, nemHomeDir, "catalog", "add", "first", firstRoot); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runNem(t, nemHomeDir, "catalog", "add", "second", secondRoot); err != nil {
		t.Fatal(err)
	}

	out, _, err := runNem(t, nemHomeDir, "info", "second:tool")
	if err != nil {
		t.Fatalf("info: %v", err)
	}
	if !strings.Contains(out, "second-desc") || strings.Contains(out, "first-desc") {
		t.Fatalf("expected the qualified catalog's entry:\n%s", out)
	}
}

func TestInfoOmitsEmptyFields(t *testing.T) {
	minimalYAML := `
schema: 2
name: bare
description: minimal package
artifact:
  oci: ":{{.Version}}"
install:
  - extract: {}
versions:
  - v1.0.0
`
	nemHomeDir := t.TempDir()
	catalogRoot := writeInfoDirCatalog(t, map[string]string{"bare": minimalYAML})
	if _, _, err := runNem(t, nemHomeDir, "catalog", "add", "dev", catalogRoot); err != nil {
		t.Fatal(err)
	}

	out, _, err := runNem(t, nemHomeDir, "info", "bare")
	if err != nil {
		t.Fatalf("info: %v", err)
	}
	for _, absent := range []string{"homepage:", "license:"} {
		if strings.Contains(out, absent) {
			t.Fatalf("info must omit empty field %q:\n%s", absent, out)
		}
	}
	if !strings.Contains(out, "platforms:") || !strings.Contains(out, "all") {
		t.Fatalf("unconstrained platforms must render as all:\n%s", out)
	}
}

func TestInfoUnknownPackageFails(t *testing.T) {
	nemHomeDir := t.TempDir()
	catalogRoot := writeInfoDirCatalog(t, map[string]string{"tool": infoPkgYAML})
	if _, _, err := runNem(t, nemHomeDir, "catalog", "add", "dev", catalogRoot); err != nil {
		t.Fatal(err)
	}

	_, _, err := runNem(t, nemHomeDir, "info", "nosuch")
	if err == nil {
		t.Fatal("want error for unknown package")
	}
}
