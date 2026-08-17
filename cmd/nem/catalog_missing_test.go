package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/oci"

	"github.com/vi-dev/nem-cli/internal/ocix"
)

const missingFixtureSourcePkg = `
schema: 2
name: ztool
description: Test tool built from source
platforms: [darwin/arm64, linux/arm64, linux/amd64]
artifact:
  oci: ":{{.Version}}"
install:
  - extract: {strip: 0}
versions:
  - version: 1.1.0
    sourceSha256: "aaa"
  - version: 1.0.0
    sourceSha256: "bbb"
build:
  source:
    url: "https://example.com/src-{{.Version}}.tar.gz"
  output: out
  steps:
    - run: make
`

func withFakeArchives(t *testing.T, f func(catalogRef, name string) (oras.ReadOnlyTarget, error)) {
	t.Helper()
	restore := openArchives
	openArchives = f
	t.Cleanup(func() { openArchives = restore })
}

func newArchiveStore(t *testing.T) oras.Target {
	t.Helper()
	store, err := oci.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestCatalogMissingReportsIncompleteVersions(t *testing.T) {
	nemHome := t.TempDir()
	dir := writeLintFixture(t, map[string]string{"ztool": missingFixtureSourcePkg})

	store := newArchiveStore(t)
	ocix.PushFakeArchive(t, store, "1.1.0", map[string][]byte{
		"darwin/arm64": []byte("a"), "linux/arm64": []byte("b"), "linux/amd64": []byte("c"),
	})
	ocix.PushFakeArchive(t, store, "1.0.0", map[string][]byte{
		"linux/amd64": []byte("c"),
	})
	withFakeArchives(t, func(catalogRef, name string) (oras.ReadOnlyTarget, error) { return store, nil })

	out, errOut, err := runNem(t, nemHome, "catalog", "missing", "ghcr.io/org/cat", dir)
	if err != nil {
		t.Fatalf("missing: %v\nstderr: %s", err, errOut)
	}
	if !strings.Contains(out, "ztool") || !strings.Contains(out, "1.0.0") {
		t.Fatalf("stdout = %q, want the incomplete version row", out)
	}
	if !strings.Contains(out, "darwin/arm64") || !strings.Contains(out, "linux/arm64") {
		t.Fatalf("stdout = %q, want the absent platforms listed", out)
	}
	if strings.Contains(out, "1.1.0") {
		t.Fatalf("stdout = %q, complete version must not be listed", out)
	}
}

func TestCatalogMissingJSONGroupsByPlatform(t *testing.T) {
	nemHome := t.TempDir()
	dir := writeLintFixture(t, map[string]string{"ztool": missingFixtureSourcePkg})

	// 1.1.0 lacks darwin/arm64; 1.0.0 has no archive index at all.
	store := newArchiveStore(t)
	ocix.PushFakeArchive(t, store, "1.1.0", map[string][]byte{
		"linux/arm64": []byte("b"), "linux/amd64": []byte("c"),
	})
	withFakeArchives(t, func(catalogRef, name string) (oras.ReadOnlyTarget, error) { return store, nil })

	out, errOut, err := runNem(t, nemHome, "catalog", "missing", "ghcr.io/org/cat", dir, "--json")
	if err != nil {
		t.Fatalf("missing --json: %v\nstderr: %s", err, errOut)
	}
	var grouped map[string][]struct {
		Package string `json:"package"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal([]byte(out), &grouped); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, out)
	}
	for _, plat := range []string{"darwin/arm64", "darwin/amd64", "linux/arm64", "linux/amd64"} {
		if _, ok := grouped[plat]; !ok {
			t.Errorf("key %q absent; every supported platform must be present", plat)
		}
	}
	if len(grouped) != 4 {
		t.Fatalf("grouped has %d keys, want exactly the supported platforms: %v", len(grouped), grouped)
	}
	if !strings.Contains(errOut, "Checked 1 oci packages") {
		t.Fatalf("stderr = %q, want the summary in json mode too", errOut)
	}
	if got := grouped["darwin/arm64"]; len(got) != 2 {
		t.Fatalf("darwin/arm64 = %+v, want both versions", got)
	}
	if got := grouped["linux/arm64"]; len(got) != 1 || got[0].Version != "1.0.0" {
		t.Fatalf("linux/arm64 = %+v, want only the index-less 1.0.0", got)
	}
	// darwin/amd64 is not in the package's platforms and must stay empty.
	if got := grouped["darwin/amd64"]; len(got) != 0 {
		t.Fatalf("darwin/amd64 = %+v, want empty for an undeclared platform", got)
	}
}

func TestCatalogMissingExpandsOSOnlyPlatformConstraints(t *testing.T) {
	nemHome := t.TempDir()
	fixture := strings.Replace(missingFixtureSourcePkg,
		"platforms: [darwin/arm64, linux/arm64, linux/amd64]", "platforms: [linux]", 1)
	dir := writeLintFixture(t, map[string]string{"ztool": fixture})

	store := newArchiveStore(t)
	for _, v := range []string{"1.1.0", "1.0.0"} {
		ocix.PushFakeArchive(t, store, v, map[string][]byte{
			"linux/arm64": []byte("b"), "linux/amd64": []byte("c"),
		})
	}
	withFakeArchives(t, func(catalogRef, name string) (oras.ReadOnlyTarget, error) { return store, nil })

	out, errOut, err := runNem(t, nemHome, "catalog", "missing", "ghcr.io/org/cat", dir)
	if err != nil {
		t.Fatalf("missing: %v\nstderr: %s", err, errOut)
	}
	if strings.TrimSpace(out) != "" {
		t.Fatalf("stdout = %q, want empty: both linux archives exist", out)
	}
}

func TestCatalogMissingSkipsPrebuiltPackages(t *testing.T) {
	nemHome := t.TempDir()
	dir := writeLintFixture(t, map[string]string{"go": lintFixtureGoPkg})

	var queried []string
	withFakeArchives(t, func(catalogRef, name string) (oras.ReadOnlyTarget, error) {
		queried = append(queried, name)
		return newArchiveStore(t), nil
	})

	out, errOut, err := runNem(t, nemHome, "catalog", "missing", "ghcr.io/org/cat", dir)
	if err != nil {
		t.Fatalf("missing: %v\nstderr: %s", err, errOut)
	}
	if len(queried) != 0 {
		t.Fatalf("archives queried for %v; prebuilt packages must be skipped", queried)
	}
	if strings.TrimSpace(out) != "" {
		t.Fatalf("stdout = %q, want empty when nothing is missing", out)
	}
	if !strings.Contains(errOut, "1 prebuilt skipped") {
		t.Fatalf("stderr = %q, want the skipped count", errOut)
	}
}

// erroringTarget fails every read with its fixed error, standing in for an
// unreachable or auth-rejecting registry.
type erroringTarget struct{ err error }

func (e erroringTarget) Fetch(context.Context, ocispec.Descriptor) (io.ReadCloser, error) {
	return nil, e.err
}
func (e erroringTarget) Exists(context.Context, ocispec.Descriptor) (bool, error) {
	return false, e.err
}
func (e erroringTarget) Resolve(context.Context, string) (ocispec.Descriptor, error) {
	return ocispec.Descriptor{}, e.err
}

func TestCatalogMissingAbortsOnRegistryFailure(t *testing.T) {
	nemHome := t.TempDir()
	dir := writeLintFixture(t, map[string]string{"ztool": missingFixtureSourcePkg})
	withFakeArchives(t, func(catalogRef, name string) (oras.ReadOnlyTarget, error) {
		return erroringTarget{err: errors.New("registry unreachable")}, nil
	})

	out, _, err := runNem(t, nemHome, "catalog", "missing", "ghcr.io/org/cat", dir)
	if err == nil {
		t.Fatal("a non-not-found registry failure must abort the command")
	}
	if strings.Contains(out, "ztool") {
		t.Fatalf("stdout = %q, a failure must not be reported as missing", out)
	}
}

func TestCatalogMissingRejectsTaggedRef(t *testing.T) {
	nemHome := t.TempDir()
	dir := writeLintFixture(t, map[string]string{"ztool": missingFixtureSourcePkg})

	if _, _, err := runNem(t, nemHome, "catalog", "missing", "ghcr.io/org/cat:v2", dir); err == nil {
		t.Fatal("a tagged registry ref must be rejected")
	}
}
