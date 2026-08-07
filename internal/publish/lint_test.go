package publish

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validGoPkg = `
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

func pkgWithEnv(name, value string) string {
	return validGoPkg + "env:\n  - name: " + name + "\n    value: \"" + value + "\"\n"
}

var pkgMissingOnePlatformSha = strings.Replace(validGoPkg, "      linux/amd64: \"ddd\"\n", "", 1)

var pkgBadArtifactTemplate = strings.Replace(validGoPkg,
	`url: "https://go.dev/dl/go{{.Version}}.{{.OS}}-{{.Arch}}.tar.gz"`,
	`url: "https://go.dev/dl/go{{.Version}}.{{.Bogus}}.tar.gz"`, 1)

func pkgNamed(name string) string {
	return strings.Replace(validGoPkg, "name: go\n", "name: "+name+"\n", 1)
}

var pkgEmptyName = strings.Replace(validGoPkg, "name: go\n", "name: \"\"\n", 1)

func writeCatalog(t *testing.T, pkgs map[string]string) string {
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

func anyContains(findings []Finding, sub string) bool {
	for _, f := range findings {
		if strings.Contains(f.String(), sub) {
			return true
		}
	}
	return false
}

func TestLintCleanCatalog(t *testing.T) {
	dir := writeCatalog(t, map[string]string{"go": validGoPkg})
	found, err := Lint(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 0 {
		t.Fatalf("clean catalog should lint clean, got %v", found)
	}
}

func TestLintFindings(t *testing.T) {
	cases := []struct {
		name    string
		pkgs    map[string]string
		wantSub string // substring expected in at least one finding
	}{
		{"reserved-env", map[string]string{"go": pkgWithEnv("PATH", "x")}, "reserved"},
		{"incomplete-sha256", map[string]string{"go": pkgMissingOnePlatformSha}, "sha256"},
		{"bad-template", map[string]string{"go": pkgBadArtifactTemplate}, "template"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeCatalog(t, tc.pkgs)
			found, err := Lint(dir)
			if err != nil {
				t.Fatal(err)
			}
			if !anyContains(found, tc.wantSub) {
				t.Fatalf("want a finding containing %q, got %v", tc.wantSub, found)
			}
		})
	}
}

func TestLintNameMismatchLabeledByDirectory(t *testing.T) {
	dir := writeCatalog(t, map[string]string{"go": pkgNamed("golang")})
	found, err := Lint(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := Finding{Pkg: "go", Msg: `name "golang" does not match its directory "go"`}
	if len(found) != 1 || found[0] != want {
		t.Fatalf("want exactly %v, got %v", want, found)
	}
}

func TestLintEmptyNameYieldsOneFinding(t *testing.T) {
	dir := writeCatalog(t, map[string]string{"go": pkgEmptyName})
	found, err := Lint(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := Finding{Pkg: "go", Msg: `invalid name ""`}
	if len(found) != 1 || found[0] != want {
		t.Fatalf("want exactly one invalid-name finding %v, got %v", want, found)
	}
}

func TestLintSingleFile(t *testing.T) {
	dir := writeCatalog(t, map[string]string{"go": pkgWithEnv("PATH", "x")})
	found, err := Lint(filepath.Join(dir, "pkgs", "go", "pkg.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !anyContains(found, "reserved") {
		t.Fatalf("single-file lint missed the finding: %v", found)
	}
}

func TestLintEmptyPkgsDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "pkgs"), 0o755); err != nil {
		t.Fatal(err)
	}
	found, err := Lint(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].Pkg != "" {
		t.Fatalf("empty pkgs dir should yield one catalog-level finding, got %v", found)
	}
}

func TestLintMissingPkgsDir(t *testing.T) {
	dir := t.TempDir()
	found, err := Lint(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].Pkg != "" {
		t.Fatalf("missing pkgs dir should yield one catalog-level finding, got %v", found)
	}
}

func TestLintParseErrorSkipsFurtherChecks(t *testing.T) {
	dir := writeCatalog(t, map[string]string{"go": "schema: 2\nname: go\nbogus: true\n"})
	found, err := Lint(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 {
		t.Fatalf("parse error should yield exactly one finding, got %v", found)
	}
}

func TestLintDeterministicOrder(t *testing.T) {
	dir := writeCatalog(t, map[string]string{
		"go":      pkgWithEnv("PATH", "x"),
		"kubectl": strings.Replace(pkgWithEnv("IFS", "x"), "name: go\n", "name: kubectl\n", 1),
	})
	first, err := Lint(dir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Lint(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != len(second) {
		t.Fatalf("nondeterministic finding count: %v vs %v", first, second)
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("nondeterministic order at %d: %v vs %v", i, first, second)
		}
	}
	if len(first) != 2 || first[0].Pkg != "go" || first[1].Pkg != "kubectl" {
		t.Fatalf("want findings sorted by package name, got %v", first)
	}
}

func TestLintStringFormat(t *testing.T) {
	f := Finding{Pkg: "go", Msg: "broken"}
	if f.String() != "go: broken" {
		t.Fatalf("String: got %q", f.String())
	}
	f = Finding{Msg: "catalog broken"}
	if f.String() != "catalog broken" {
		t.Fatalf("String with empty Pkg: got %q", f.String())
	}
}
