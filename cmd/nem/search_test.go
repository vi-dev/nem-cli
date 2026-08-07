package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const searchPkgYAML = `
schema: 2
name: %NAME%
description: %DESC%
artifact:
  oci: ":{{.Version}}"
install:
  - extract: {}
versions:
  - v1.0.0
`

// writeSearchDirCatalog writes a dir catalog rooted at a temp dir holding one
// package per name/description pair.
func writeSearchDirCatalog(t *testing.T, pkgs map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, desc := range pkgs {
		dir := filepath.Join(root, "pkgs", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		y := strings.NewReplacer("%NAME%", name, "%DESC%", desc).Replace(searchPkgYAML)
		if err := os.WriteFile(filepath.Join(dir, "pkg.yaml"), []byte(y), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestSearchMatchesByNameAndDescription(t *testing.T) {
	nemHomeDir := t.TempDir()
	catalogRoot := writeSearchDirCatalog(t, map[string]string{
		"foo": "unrelated",
		"bar": "contains fookw somewhere",
		"baz": "nothing here",
	})
	if _, _, err := runNem(t, nemHomeDir, "catalog", "add", "dev", catalogRoot); err != nil {
		t.Fatal(err)
	}

	out, _, err := runNem(t, nemHomeDir, "search", "foo")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if !strings.Contains(out, "foo") {
		t.Fatalf("name match missing:\n%s", out)
	}
	if !strings.Contains(out, "bar") {
		t.Fatalf("description match missing:\n%s", out)
	}
	if strings.Contains(out, "baz") {
		t.Fatalf("unrelated package must not match:\n%s", out)
	}
	for _, want := range []string{"NAME", "VERSION", "CATALOG", "DESCRIPTION", "v1.0.0", "dev"} {
		if !strings.Contains(out, want) {
			t.Fatalf("table missing %q:\n%s", want, out)
		}
	}
}

func TestSearchRanksExactPrefixSubstringDescription(t *testing.T) {
	nemHomeDir := t.TempDir()
	catalogRoot := writeSearchDirCatalog(t, map[string]string{
		"go":           "toolchain",
		"golang-tools": "extra utilities",
		"mango":        "a fruit",
		"kubectl":      "manages go clusters",
	})
	if _, _, err := runNem(t, nemHomeDir, "catalog", "add", "dev", catalogRoot); err != nil {
		t.Fatal(err)
	}

	out, _, err := runNem(t, nemHomeDir, "search", "go")
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	var names []string
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] == "NAME" {
			continue
		}
		names = append(names, fields[0])
	}
	want := []string{"go", "golang-tools", "mango", "kubectl"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("rank order = %v, want %v\n%s", names, want, out)
	}
}

func TestSearchDedupesByNameKeepingFirstCatalog(t *testing.T) {
	nemHomeDir := t.TempDir()
	firstRoot := writeSearchDirCatalog(t, map[string]string{"shared": "first-desc"})
	secondRoot := writeSearchDirCatalog(t, map[string]string{"shared": "second-desc"})

	if _, _, err := runNem(t, nemHomeDir, "catalog", "add", "first", firstRoot); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runNem(t, nemHomeDir, "catalog", "add", "second", secondRoot); err != nil {
		t.Fatal(err)
	}

	out, _, err := runNem(t, nemHomeDir, "search", "shared")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if strings.Count(out, "shared") != 1 {
		t.Fatalf("expected exactly one row for shared:\n%s", out)
	}
	if !strings.Contains(out, "first-desc") || strings.Contains(out, "second-desc") {
		t.Fatalf("expected first catalog's entry to win:\n%s", out)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	var row string
	for _, l := range lines {
		if strings.HasPrefix(l, "shared ") {
			row = l
		}
	}
	if row == "" || !strings.Contains(row, "first") {
		t.Fatalf("expected shared row to list catalog first: %q", row)
	}
}

func TestSearchSkipsUnsyncedCatalogWithWarning(t *testing.T) {
	nemHomeDir := t.TempDir()
	catalogRoot := writeSearchDirCatalog(t, map[string]string{"tool": "a fine tool"})
	// A fresh nemHome auto-creates the "official" oci catalog on first
	// OpenConfig, which is never synced in this test — search must skip it
	// with a warning rather than failing.
	if _, _, err := runNem(t, nemHomeDir, "catalog", "add", "dev", catalogRoot); err != nil {
		t.Fatal(err)
	}

	out, errb, err := runNem(t, nemHomeDir, "search", "tool")
	if err != nil {
		t.Fatalf("search: %v\nstderr: %s", err, errb)
	}
	if !strings.Contains(errb, "official") || !strings.Contains(errb, "not synced") {
		t.Fatalf("expected unsynced-catalog warning:\n%s", errb)
	}
	if !strings.Contains(out, "tool") {
		t.Fatalf("dir catalog results missing despite unsynced oci catalog:\n%s", out)
	}
}

func TestSearchNoMatchesExitsZero(t *testing.T) {
	nemHomeDir := t.TempDir()
	catalogRoot := writeSearchDirCatalog(t, map[string]string{"tool": "a fine tool"})
	if _, _, err := runNem(t, nemHomeDir, "catalog", "add", "dev", catalogRoot); err != nil {
		t.Fatal(err)
	}

	out, _, err := runNem(t, nemHomeDir, "search", "zzznotfound")
	if err != nil {
		t.Fatalf("search must exit 0 on no matches: %v", err)
	}
	if strings.TrimSpace(out) != "" {
		t.Fatalf("no-match search must print nothing on stdout:\n%s", out)
	}
}

func TestSearchAbortsOnNonNotSyncedCatalogError(t *testing.T) {
	nemHomeDir := t.TempDir()
	root := t.TempDir()
	badDir := filepath.Join(root, "pkgs", "bad")
	if err := os.MkdirAll(badDir, 0o755); err != nil {
		t.Fatal(err)
	}
	badYAML := "schema: 9\nname: bad\ndescription: broken\n"
	if err := os.WriteFile(filepath.Join(badDir, "pkg.yaml"), []byte(badYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runNem(t, nemHomeDir, "catalog", "add", "dev", root); err != nil {
		t.Fatal(err)
	}

	_, _, err := runNem(t, nemHomeDir, "search", "bad")
	if err == nil {
		t.Fatal("want error for a broken catalog entry")
	}
}
