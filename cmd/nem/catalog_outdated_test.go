package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/vi-dev/nem-cli/internal/spec"
)

const outdatedFixtureJq = `
schema: 2
name: jq
artifact:
  url: "https://example.com/jq-{{.Version}}-{{.OS}}-{{.Arch}}"
install:
  - copy: {src: "{{.Artifact}}", dst: "bin/jq", mode: 0o755}
versionDiscovery:
  github:
    repo: jqlang/jq
    prefix: "jq-"
versions:
  - version: 1.8.2
    sha256:
      darwin/arm64: "aaa"
      darwin/amd64: "bbb"
      linux/arm64: "ccc"
      linux/amd64: "ddd"
`

func withFakeLatest(t *testing.T, f func(context.Context, *spec.Package) (string, error)) {
	t.Helper()
	restore := discoverLatest
	discoverLatest = f
	t.Cleanup(func() { discoverLatest = restore })
}

func TestCatalogOutdatedTable(t *testing.T) {
	nemHome := t.TempDir()
	dir := writeLintFixture(t, map[string]string{
		"jq": outdatedFixtureJq,
		"go": lintFixtureGoPkg, // no versionDiscovery → skipped
	})
	withFakeLatest(t, func(_ context.Context, pkg *spec.Package) (string, error) {
		return "1.8.3", nil
	})

	out, errOut, err := runNem(t, nemHome, "catalog", "outdated", dir)
	if err != nil {
		t.Fatalf("outdated: %v", err)
	}
	if !strings.Contains(out, "jq") || !strings.Contains(out, "1.8.2") || !strings.Contains(out, "1.8.3") {
		t.Fatalf("stdout = %q, want jq row", out)
	}
	if !strings.Contains(errOut, "1 outdated") || !strings.Contains(errOut, "1 without discovery") {
		t.Fatalf("stderr = %q, want summary", errOut)
	}
}

func TestCatalogOutdatedUpToDateIsQuietOnStdout(t *testing.T) {
	nemHome := t.TempDir()
	dir := writeLintFixture(t, map[string]string{"jq": outdatedFixtureJq})
	withFakeLatest(t, func(_ context.Context, pkg *spec.Package) (string, error) {
		return "1.8.2", nil
	})

	out, _, err := runNem(t, nemHome, "catalog", "outdated", dir)
	if err != nil {
		t.Fatalf("outdated: %v", err)
	}
	if strings.TrimSpace(out) != "" {
		t.Fatalf("stdout = %q, want empty when everything is current", out)
	}
}

func TestCatalogOutdatedJSON(t *testing.T) {
	nemHome := t.TempDir()
	dir := writeLintFixture(t, map[string]string{"jq": outdatedFixtureJq})
	withFakeLatest(t, func(_ context.Context, pkg *spec.Package) (string, error) {
		return "1.8.3", nil
	})

	out, _, err := runNem(t, nemHome, "catalog", "outdated", "--json", dir)
	if err != nil {
		t.Fatal(err)
	}
	var rows []struct {
		Name, Current, Latest, Error string
		Outdated                     bool
	}
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, out)
	}
	if len(rows) != 1 || rows[0].Name != "jq" || !rows[0].Outdated || rows[0].Latest != "1.8.3" {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestCatalogOutdatedDiscoveryErrorWarnsButSucceeds(t *testing.T) {
	nemHome := t.TempDir()
	dir := writeLintFixture(t, map[string]string{"jq": outdatedFixtureJq})
	withFakeLatest(t, func(_ context.Context, pkg *spec.Package) (string, error) {
		return "", errors.New("upstream unreachable")
	})

	out, errOut, err := runNem(t, nemHome, "catalog", "outdated", "--json", dir)
	if err != nil {
		t.Fatalf("outdated must exit 0 on per-package errors, got %v", err)
	}
	if !strings.Contains(errOut, "upstream unreachable") {
		t.Fatalf("stderr = %q, want warning", errOut)
	}
	if !strings.Contains(out, "upstream unreachable") {
		t.Fatalf("json = %q, want error field", out)
	}
}
