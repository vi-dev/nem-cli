package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/vi-dev/nem-cli/internal/spec"
)

// bumpArtifactServer serves fake per-platform artifacts at
// /<version>/<os>-<arch> and returns the expected sha256 per platform.
func bumpArtifactServer(t *testing.T, version string) (*httptest.Server, map[string]string) {
	t.Helper()
	sums := map[string]string{}
	for _, p := range spec.Supported {
		body := "artifact " + version + " " + p.String()
		s := sha256.Sum256([]byte(body))
		sums[p.String()] = hex.EncodeToString(s[:])
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Path shape: /<version>/<os>-<arch>
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
		if len(parts) != 2 || parts[0] != version {
			http.NotFound(w, r)
			return
		}
		osArch := strings.SplitN(parts[1], "-", 2)
		if len(osArch) != 2 {
			http.NotFound(w, r)
			return
		}
		fmt.Fprintf(w, "artifact %s %s/%s", parts[0], osArch[0], osArch[1])
	}))
	t.Cleanup(srv.Close)
	return srv, sums
}

func bumpFixture(serverURL string) string {
	return fmt.Sprintf(`schema: 2
name: jq
artifact:
  url: "%s/{{.Version}}/{{.OS}}-{{.Arch}}"
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
`, serverURL)
}

func TestCatalogBumpPrebuilt(t *testing.T) {
	nemHome := t.TempDir()
	srv, sums := bumpArtifactServer(t, "1.8.3")
	dir := writeLintFixture(t, map[string]string{"jq": bumpFixture(srv.URL)})
	path := filepath.Join(dir, "pkgs", "jq", "pkg.yaml")
	// Canonicalize so the bump diff is surgical.
	if _, _, err := runNem(t, nemHome, "catalog", "fmt", dir); err != nil {
		t.Fatal(err)
	}

	if _, _, err := runNem(t, nemHome, "catalog", "bump", "--version", "1.8.3", path); err != nil {
		t.Fatalf("bump: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := spec.Parse(data)
	if err != nil {
		t.Fatalf("bumped manifest no longer parses: %v", err)
	}
	if pkg.Versions[0].Version != "1.8.3" {
		t.Fatalf("versions[0] = %q, want 1.8.3", pkg.Versions[0].Version)
	}
	for plat, want := range sums {
		if got := pkg.Versions[0].Sha256[plat]; got != want {
			t.Errorf("sha256[%s] = %q, want %q", plat, got, want)
		}
	}
	if pkg.Versions[1].Version != "1.8.2" {
		t.Fatal("existing entry lost")
	}
}

func TestCatalogBumpUsesDiscoveryByDefault(t *testing.T) {
	nemHome := t.TempDir()
	srv, _ := bumpArtifactServer(t, "1.8.3")
	dir := writeLintFixture(t, map[string]string{"jq": bumpFixture(srv.URL)})
	path := filepath.Join(dir, "pkgs", "jq", "pkg.yaml")
	stubBumpList(t, "1.8.2", "1.8.3")

	if _, _, err := runNem(t, nemHome, "catalog", "bump", path); err != nil {
		t.Fatalf("bump: %v", err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "1.8.3") {
		t.Fatal("discovered version not inserted")
	}
}

// stubBumpList makes discovery return exactly versions, in that order.
func stubBumpList(t *testing.T, versions ...string) {
	t.Helper()
	restore := bumpList
	bumpList = func(_ context.Context, _ *spec.Package) ([]string, error) { return versions, nil }
	t.Cleanup(func() { bumpList = restore })
}

// bumpMultiArtifactServer serves fake artifacts for every listed version
// at /<version>/<os>-<arch>; sums is keyed "<version> <os/arch>".
func bumpMultiArtifactServer(t *testing.T, versions ...string) (*httptest.Server, map[string]string) {
	t.Helper()
	serve := map[string]bool{}
	sums := map[string]string{}
	for _, v := range versions {
		serve[v] = true
		for _, p := range spec.Supported {
			body := "artifact " + v + " " + p.String()
			s := sha256.Sum256([]byte(body))
			sums[v+" "+p.String()] = hex.EncodeToString(s[:])
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
		if len(parts) != 2 || !serve[parts[0]] {
			http.NotFound(w, r)
			return
		}
		osArch := strings.SplitN(parts[1], "-", 2)
		if len(osArch) != 2 {
			http.NotFound(w, r)
			return
		}
		fmt.Fprintf(w, "artifact %s %s/%s", parts[0], osArch[0], osArch[1])
	}))
	t.Cleanup(srv.Close)
	return srv, sums
}

func TestCatalogBumpAddsAllNewerVersions(t *testing.T) {
	nemHome := t.TempDir()
	srv, sums := bumpMultiArtifactServer(t, "1.8.3", "1.8.4")
	dir := writeLintFixture(t, map[string]string{"jq": bumpFixture(srv.URL)})
	path := filepath.Join(dir, "pkgs", "jq", "pkg.yaml")
	stubBumpList(t, "1.8.4", "1.7.1", "1.8.3", "1.8.2") // unordered; includes older and current

	_, errOut, err := runNem(t, nemHome, "catalog", "bump", path)
	if err != nil {
		t.Fatalf("bump: %v", err)
	}
	// Per-platform completion lines must name the version they hashed —
	// a multi-version run is unreadable otherwise.
	for _, line := range []string{"Hashed jq 1.8.3 linux/amd64", "Hashed jq 1.8.4 darwin/arm64"} {
		if !strings.Contains(errOut, line) {
			t.Errorf("stderr = %q, want %q", errOut, line)
		}
	}
	pkg, err := spec.Parse(mustRead(t, path))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"1.8.4", "1.8.3", "1.8.2"}
	if len(pkg.Versions) != len(want) {
		t.Fatalf("got %d versions, want %d", len(pkg.Versions), len(want))
	}
	for i, v := range want {
		if pkg.Versions[i].Version != v {
			t.Fatalf("versions[%d] = %q, want %q (newest-first)", i, pkg.Versions[i].Version, v)
		}
	}
	if got := pkg.Versions[0].Sha256["linux/amd64"]; got != sums["1.8.4 linux/amd64"] {
		t.Errorf("1.8.4 sha256 = %q, want %q", got, sums["1.8.4 linux/amd64"])
	}
	if got := pkg.Versions[1].Sha256["linux/amd64"]; got != sums["1.8.3 linux/amd64"] {
		t.Errorf("1.8.3 sha256 = %q, want %q", got, sums["1.8.3 linux/amd64"])
	}
}

func TestCatalogBumpBackfillCoverage(t *testing.T) {
	nemHome := t.TempDir()
	srv, sums := bumpMultiArtifactServer(t, "1.8.4", "1.8.3", "1.8.1")
	dir := writeLintFixture(t, map[string]string{"jq": bumpFixture(srv.URL)})
	path := filepath.Join(dir, "pkgs", "jq", "pkg.yaml")
	stubBumpList(t, "1.8.4", "1.8.3", "1.8.2", "1.8.1", "1.8.0", "1.7.1")

	_, errOut, err := runNem(t, nemHome, "catalog", "bump", "--backfill", "4", path)
	if err != nil {
		t.Fatalf("bump: %v", err)
	}
	pkg, err := spec.Parse(mustRead(t, path))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"1.8.4", "1.8.3", "1.8.2", "1.8.1"}
	if len(pkg.Versions) != len(want) {
		t.Fatalf("got %d versions, want %d: %+v", len(pkg.Versions), len(want), pkg.Versions)
	}
	for i, v := range want {
		if pkg.Versions[i].Version != v {
			t.Fatalf("versions[%d] = %q, want %q (newest-first)", i, pkg.Versions[i].Version, v)
		}
	}
	if got := pkg.Versions[3].Sha256["linux/amd64"]; got != sums["1.8.1 linux/amd64"] {
		t.Errorf("backfilled 1.8.1 sha256 = %q, want %q", got, sums["1.8.1 linux/amd64"])
	}
	if !strings.Contains(errOut, "Bumped jq 1.8.2 → 1.8.4 (3 versions)") {
		t.Fatalf("stderr = %q, want bumped summary", errOut)
	}
}

func TestCatalogBumpBackfillOnlyOldVersions(t *testing.T) {
	nemHome := t.TempDir()
	srv, _ := bumpMultiArtifactServer(t, "1.8.1")
	dir := writeLintFixture(t, map[string]string{"jq": bumpFixture(srv.URL)})
	path := filepath.Join(dir, "pkgs", "jq", "pkg.yaml")
	stubBumpList(t, "1.8.2", "1.8.1")

	_, errOut, err := runNem(t, nemHome, "catalog", "bump", "--backfill", "2", path)
	if err != nil {
		t.Fatalf("bump: %v", err)
	}
	pkg, err := spec.Parse(mustRead(t, path))
	if err != nil {
		t.Fatal(err)
	}
	if len(pkg.Versions) != 2 || pkg.Versions[0].Version != "1.8.2" || pkg.Versions[1].Version != "1.8.1" {
		t.Fatalf("versions = %+v, want [1.8.2 1.8.1]", pkg.Versions)
	}
	if !strings.Contains(errOut, "Backfilled jq (1 version)") {
		t.Fatalf("stderr = %q, want backfilled summary", errOut)
	}
}

func TestCatalogBumpBackfillIsIdempotent(t *testing.T) {
	nemHome := t.TempDir()
	srv, _ := bumpMultiArtifactServer(t, "1.8.1")
	dir := writeLintFixture(t, map[string]string{"jq": bumpFixture(srv.URL)})
	path := filepath.Join(dir, "pkgs", "jq", "pkg.yaml")
	stubBumpList(t, "1.8.2", "1.8.1")

	if _, _, err := runNem(t, nemHome, "catalog", "bump", "--backfill", "2", path); err != nil {
		t.Fatalf("first bump: %v", err)
	}
	before := mustRead(t, path)
	_, errOut, err := runNem(t, nemHome, "catalog", "bump", "--backfill", "2", path)
	if err != nil {
		t.Fatalf("second bump: %v", err)
	}
	if !strings.Contains(errOut, "up to date") {
		t.Fatalf("stderr = %q, want up-to-date notice", errOut)
	}
	if string(mustRead(t, path)) != string(before) {
		t.Fatal("idempotent backfill must not rewrite the file")
	}
}

func TestCatalogBumpBackfillSkipsMissingOldRelease(t *testing.T) {
	nemHome := t.TempDir()
	srv, _ := bumpMultiArtifactServer(t, "1.8.1") // 1.8.0 has no assets
	dir := writeLintFixture(t, map[string]string{"jq": bumpFixture(srv.URL)})
	path := filepath.Join(dir, "pkgs", "jq", "pkg.yaml")
	stubBumpList(t, "1.8.2", "1.8.1", "1.8.0")

	_, errOut, err := runNem(t, nemHome, "catalog", "bump", "--backfill", "3", path)
	if err != nil {
		t.Fatalf("bump must succeed when another candidate lands: %v", err)
	}
	pkg, err := spec.Parse(mustRead(t, path))
	if err != nil {
		t.Fatal(err)
	}
	if len(pkg.Versions) != 2 || pkg.Versions[1].Version != "1.8.1" {
		t.Fatalf("versions = %+v, want [1.8.2 1.8.1]", pkg.Versions)
	}
	if !strings.Contains(errOut, "1.8.0") {
		t.Fatalf("stderr = %q, want a warning naming the skipped 1.8.0", errOut)
	}
}

func sourceBackfillFixture(serverURL string) string {
	return fmt.Sprintf(`schema: 2
name: openssl
platforms: [darwin/arm64, linux/arm64, linux/amd64]
artifact:
  oci: ":{{.Version}}"
install:
  - extract: {strip: 0}
versions:
  - version: 3.4.2
    sourceSha256: "0000000000000000000000000000000000000000000000000000000000000000"
build:
  source:
    url: '%s/openssl-{{.Version}}.tar.gz'
  output: out
  steps:
    - run: make
`, serverURL)
}

func TestCatalogBumpBackfillSourceBuiltWarns(t *testing.T) {
	nemHome := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/openssl-3.4.1.tar.gz" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, "source tarball 3.4.1")
	}))
	t.Cleanup(srv.Close)
	dir := writeLintFixture(t, map[string]string{"openssl": sourceBackfillFixture(srv.URL)})
	path := filepath.Join(dir, "pkgs", "openssl", "pkg.yaml")
	stubBumpList(t, "3.4.2", "3.4.1")

	_, errOut, err := runNem(t, nemHome, "catalog", "bump", "--backfill", "2", path)
	if err != nil {
		t.Fatalf("bump: %v", err)
	}
	pkg, err := spec.Parse(mustRead(t, path))
	if err != nil {
		t.Fatal(err)
	}
	if len(pkg.Versions) != 2 || pkg.Versions[1].Version != "3.4.1" || pkg.Versions[1].SourceSha256 == "" {
		t.Fatalf("versions = %+v, want backfilled 3.4.1 with sourceSha256", pkg.Versions)
	}
	if !strings.Contains(errOut, "source-built") || !strings.Contains(errOut, "build --version") {
		t.Fatalf("stderr = %q, want source-built warning and build hint", errOut)
	}
}

func TestCatalogBumpVersionAndBackfillConflict(t *testing.T) {
	nemHome := t.TempDir()
	srv, _ := bumpMultiArtifactServer(t)
	dir := writeLintFixture(t, map[string]string{"jq": bumpFixture(srv.URL)})
	path := filepath.Join(dir, "pkgs", "jq", "pkg.yaml")
	before := mustRead(t, path)

	_, _, err := runNem(t, nemHome, "catalog", "bump", "--version", "1.8.3", "--backfill", "2", path)
	if err == nil || !strings.Contains(err.Error(), "combined") {
		t.Fatalf("err = %v, want cannot-be-combined error", err)
	}
	if string(mustRead(t, path)) != string(before) {
		t.Fatal("conflicting flags must not modify the manifest")
	}
}

func TestCatalogBumpVersionBackfillsInPlace(t *testing.T) {
	nemHome := t.TempDir()
	srv, _ := bumpMultiArtifactServer(t, "1.8.1")
	dir := writeLintFixture(t, map[string]string{"jq": bumpFixture(srv.URL)})
	path := filepath.Join(dir, "pkgs", "jq", "pkg.yaml")

	_, errOut, err := runNem(t, nemHome, "catalog", "bump", "--version", "1.8.1", path)
	if err != nil {
		t.Fatalf("bump: %v", err)
	}
	pkg, err := spec.Parse(mustRead(t, path))
	if err != nil {
		t.Fatal(err)
	}
	if len(pkg.Versions) != 2 || pkg.Versions[0].Version != "1.8.2" || pkg.Versions[1].Version != "1.8.1" {
		t.Fatalf("versions = %+v, want [1.8.2 1.8.1] (older --version must not become head)", pkg.Versions)
	}
	if !strings.Contains(errOut, "Backfilled jq (1 version)") {
		t.Fatalf("stderr = %q, want backfilled summary", errOut)
	}
}

func legacyTailFixture(serverURL string) string {
	return fmt.Sprintf(`schema: 2
name: jq
artifact:
  url: "%s/{{.Version}}/{{.OS}}-{{.Arch}}"
install:
  - copy: {src: "{{.Artifact}}", dst: "bin/jq", mode: 0o755}
versionDiscovery:
  github:
    repo: jqlang/jq
    prefix: "jq-"
versions:
  - version: 2.0.0
    sha256:
      darwin/arm64: "aaa"
      darwin/amd64: "bbb"
      linux/arm64: "ccc"
      linux/amd64: "ddd"
  - version: v1.9.0
    sha256:
      darwin/arm64: "eee"
      darwin/amd64: "fff"
      linux/arm64: "ggg"
      linux/amd64: "hhh"
`, serverURL)
}

func TestCatalogBumpToleratesLegacyTail(t *testing.T) {
	nemHome := t.TempDir()
	srv, _ := bumpMultiArtifactServer(t, "2.1.0")
	dir := writeLintFixture(t, map[string]string{"jq": legacyTailFixture(srv.URL)})
	path := filepath.Join(dir, "pkgs", "jq", "pkg.yaml")
	stubBumpList(t, "2.1.0", "2.0.0")

	if _, _, err := runNem(t, nemHome, "catalog", "bump", path); err != nil {
		t.Fatalf("bump must tolerate a legacy tail below a migrated head: %v", err)
	}
	pkg, err := spec.Parse(mustRead(t, path))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"2.1.0", "2.0.0", "v1.9.0"}
	for i, v := range want {
		if pkg.Versions[i].Version != v {
			t.Fatalf("versions[%d] = %q, want %q", i, pkg.Versions[i].Version, v)
		}
	}
}

func unorderedHistoryFixture(serverURL string) string {
	return fmt.Sprintf(`schema: 2
name: jq
artifact:
  url: "%s/{{.Version}}/{{.OS}}-{{.Arch}}"
install:
  - copy: {src: "{{.Artifact}}", dst: "bin/jq", mode: 0o755}
versionDiscovery:
  github:
    repo: jqlang/jq
    prefix: "jq-"
versions:
  - version: 2.0.0
    sha256:
      darwin/arm64: "aaa"
      darwin/amd64: "bbb"
      linux/arm64: "ccc"
      linux/amd64: "ddd"
  - version: 1.0.0
    sha256:
      darwin/arm64: "eee"
      darwin/amd64: "fff"
      linux/arm64: "ggg"
      linux/amd64: "hhh"
  - version: 1.5.0
    sha256:
      darwin/arm64: "iii"
      darwin/amd64: "jjj"
      linux/arm64: "kkk"
      linux/amd64: "lll"
`, serverURL)
}

func TestCatalogBumpRejectsUnorderedHistory(t *testing.T) {
	nemHome := t.TempDir()
	srv, _ := bumpMultiArtifactServer(t, "2.1.0")
	dir := writeLintFixture(t, map[string]string{"jq": unorderedHistoryFixture(srv.URL)})
	path := filepath.Join(dir, "pkgs", "jq", "pkg.yaml")
	stubBumpList(t, "2.1.0")
	before := mustRead(t, path)

	_, _, err := runNem(t, nemHome, "catalog", "bump", path)
	if err == nil || !strings.Contains(err.Error(), "newest-first") {
		t.Fatalf("err = %v, want newest-first rejection (out-of-order history is a lint finding, not bumpable state)", err)
	}
	if string(mustRead(t, path)) != string(before) {
		t.Fatal("rejected bump must not modify the manifest")
	}
}

func legacySpellingFixture(serverURL string) string {
	return fmt.Sprintf(`schema: 2
name: jq
artifact:
  url: "%s/{{.Version}}/{{.OS}}-{{.Arch}}"
install:
  - copy: {src: "{{.Artifact}}", dst: "bin/jq", mode: 0o755}
versionDiscovery:
  github:
    repo: jqlang/jq
    prefix: "jq-"
versions:
  - version: v1.3.1
    sha256:
      darwin/arm64: "aaa"
      darwin/amd64: "bbb"
      linux/arm64: "ccc"
      linux/amd64: "ddd"
`, serverURL)
}

func TestCatalogBumpSkipsSpellingEqualCandidates(t *testing.T) {
	nemHome := t.TempDir()
	srv, _ := bumpMultiArtifactServer(t, "1.3.1")
	dir := writeLintFixture(t, map[string]string{"jq": legacySpellingFixture(srv.URL)})
	path := filepath.Join(dir, "pkgs", "jq", "pkg.yaml")
	stubBumpList(t, "1.3.1")
	before := mustRead(t, path)

	_, errOut, err := runNem(t, nemHome, "catalog", "bump", "--backfill", "1", path)
	if err != nil {
		t.Fatalf("bump: %v", err)
	}
	if !strings.Contains(errOut, "up to date") {
		t.Fatalf("stderr = %q, want up-to-date notice (1.3.1 equals present v1.3.1)", errOut)
	}
	if string(mustRead(t, path)) != string(before) {
		t.Fatal("spelling-equal candidate must not be added")
	}
}

func TestCatalogBumpVersionSpellingEqualIsNoop(t *testing.T) {
	nemHome := t.TempDir()
	srv, _ := bumpMultiArtifactServer(t, "1.3.1")
	dir := writeLintFixture(t, map[string]string{"jq": legacySpellingFixture(srv.URL)})
	path := filepath.Join(dir, "pkgs", "jq", "pkg.yaml")
	before := mustRead(t, path)

	_, errOut, err := runNem(t, nemHome, "catalog", "bump", "--version", "1.3.1", path)
	if err != nil {
		t.Fatalf("bump: %v", err)
	}
	if !strings.Contains(errOut, "already present") {
		t.Fatalf("stderr = %q, want already-present notice", errOut)
	}
	if string(mustRead(t, path)) != string(before) {
		t.Fatal("spelling-equal --version must not modify the manifest")
	}
}

func flowVersionsBumpFixture(serverURL string) string {
	return fmt.Sprintf(`schema: 2
name: jq
artifact:
  url: "%s/{{.Version}}/{{.OS}}-{{.Arch}}"
install:
  - copy: {src: "{{.Artifact}}", dst: "bin/jq", mode: 0o755}
versions: [{version: 1.8.2, sha256: {darwin/arm64: "aaa", darwin/amd64: "bbb", linux/arm64: "ccc", linux/amd64: "ddd"}}]
`, serverURL)
}

func TestCatalogBumpRejectsFlowVersionsBeforeDownloading(t *testing.T) {
	nemHome := t.TempDir()
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		fmt.Fprint(w, "ok")
	}))
	t.Cleanup(srv.Close)
	dir := writeLintFixture(t, map[string]string{"jq": flowVersionsBumpFixture(srv.URL)})
	path := filepath.Join(dir, "pkgs", "jq", "pkg.yaml")
	before := mustRead(t, path)

	_, _, err := runNem(t, nemHome, "catalog", "bump", "--version", "1.8.3", path)
	if err == nil || !strings.Contains(err.Error(), "block sequence") {
		t.Fatalf("err = %v, want block-sequence rejection", err)
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("server got %d requests, want 0 (must fail before downloading)", n)
	}
	if string(mustRead(t, path)) != string(before) {
		t.Fatal("rejected bump must not modify the manifest")
	}
}

func TestCatalogBumpSkipsFailedCandidate(t *testing.T) {
	nemHome := t.TempDir()
	srv, _ := bumpMultiArtifactServer(t, "1.8.4") // 1.8.3 has no assets
	dir := writeLintFixture(t, map[string]string{"jq": bumpFixture(srv.URL)})
	path := filepath.Join(dir, "pkgs", "jq", "pkg.yaml")
	stubBumpList(t, "1.8.3", "1.8.4")

	_, errOut, err := runNem(t, nemHome, "catalog", "bump", path)
	if err != nil {
		t.Fatalf("bump must succeed when another candidate lands: %v", err)
	}
	pkg, err := spec.Parse(mustRead(t, path))
	if err != nil {
		t.Fatal(err)
	}
	if len(pkg.Versions) != 2 || pkg.Versions[0].Version != "1.8.4" || pkg.Versions[1].Version != "1.8.2" {
		t.Fatalf("versions = %+v, want [1.8.4 1.8.2]", pkg.Versions)
	}
	if !strings.Contains(errOut, "1.8.3") {
		t.Fatalf("stderr = %q, want a warning naming the skipped 1.8.3", errOut)
	}
}

func TestCatalogBumpAllCandidatesFailWritesNothing(t *testing.T) {
	nemHome := t.TempDir()
	srv, _ := bumpMultiArtifactServer(t) // serves nothing
	dir := writeLintFixture(t, map[string]string{"jq": bumpFixture(srv.URL)})
	path := filepath.Join(dir, "pkgs", "jq", "pkg.yaml")
	stubBumpList(t, "1.8.3")
	before := mustRead(t, path)

	_, errOut, err := runNem(t, nemHome, "catalog", "bump", path)
	if err == nil {
		t.Fatal("want error when every candidate fails")
	}
	if !strings.Contains(errOut, "retry later") {
		t.Fatalf("stderr = %q, want not-uploaded-yet hint", errOut)
	}
	if string(mustRead(t, path)) != string(before) {
		t.Fatal("failed bump must not modify the manifest")
	}
}

func TestCatalogBumpUpToDateIsNoop(t *testing.T) {
	nemHome := t.TempDir()
	srv, _ := bumpMultiArtifactServer(t)
	dir := writeLintFixture(t, map[string]string{"jq": bumpFixture(srv.URL)})
	path := filepath.Join(dir, "pkgs", "jq", "pkg.yaml")
	stubBumpList(t, "1.8.2", "1.7.1")
	before := mustRead(t, path)

	_, errOut, err := runNem(t, nemHome, "catalog", "bump", path)
	if err != nil {
		t.Fatalf("bump: %v", err)
	}
	if !strings.Contains(errOut, "up to date") {
		t.Fatalf("stderr = %q, want up-to-date notice", errOut)
	}
	if string(mustRead(t, path)) != string(before) {
		t.Fatal("no-op bump must not rewrite the file")
	}
}

func TestCatalogBumpAlreadyPresentIsNoop(t *testing.T) {
	nemHome := t.TempDir()
	srv, _ := bumpArtifactServer(t, "1.8.2")
	dir := writeLintFixture(t, map[string]string{"jq": bumpFixture(srv.URL)})
	path := filepath.Join(dir, "pkgs", "jq", "pkg.yaml")
	before, _ := os.ReadFile(path)

	_, errOut, err := runNem(t, nemHome, "catalog", "bump", "--version", "1.8.2", path)
	if err != nil {
		t.Fatalf("bump: %v", err)
	}
	if !strings.Contains(errOut, "already present") {
		t.Fatalf("stderr = %q, want already-present notice", errOut)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("no-op bump must not rewrite the file")
	}
}

func sourceBumpFixture(serverURL string) string {
	return fmt.Sprintf(`schema: 2
name: openssl
platforms: [darwin/arm64, linux/arm64, linux/amd64]
artifact:
  oci: ":{{.Version}}"
install:
  - extract: {strip: 0}
versions:
  - version: v3.4.1
    sourceSha256: "002a2d6b30b58bf4bea46c43bdd96365aaf8daa6c428782aa4feee06da197df3"
build:
  source:
    url: '%s/openssl-{{trimPrefix .Version "v"}}.tar.gz'
  output: out
  steps:
    - run: make
`, serverURL)
}

func TestCatalogBumpSourceBuilt(t *testing.T) {
	nemHome := t.TempDir()
	body := []byte("source tarball v3.4.2")
	wantSum := sha256.Sum256(body)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/openssl-3.4.2.tar.gz" {
			http.NotFound(w, r)
			return
		}
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	dir := writeLintFixture(t, map[string]string{"openssl": sourceBumpFixture(srv.URL)})
	path := filepath.Join(dir, "pkgs", "openssl", "pkg.yaml")

	_, errOut, err := runNem(t, nemHome, "catalog", "bump", "--version", "v3.4.2", path)
	if !strings.Contains(errOut, "Hashed openssl v3.4.2 source") {
		t.Errorf("stderr = %q, want source line naming the version", errOut)
	}
	if err != nil {
		t.Fatalf("bump: %v", err)
	}
	pkgData, _ := os.ReadFile(path)
	pkg, err := spec.Parse(pkgData)
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Versions[0].Version != "v3.4.2" ||
		pkg.Versions[0].SourceSha256 != hex.EncodeToString(wantSum[:]) {
		t.Fatalf("versions[0] = %+v", pkg.Versions[0])
	}
}

func TestCatalogBumpBareOCI(t *testing.T) {
	nemHome := t.TempDir()
	fixture := `schema: 2
name: tool
artifact:
  oci: ":{{.Version}}"
install:
  - extract: {strip: 0}
versions:
  - v1.0.0
`
	dir := writeLintFixture(t, map[string]string{"tool": fixture})
	path := filepath.Join(dir, "pkgs", "tool", "pkg.yaml")

	if _, _, err := runNem(t, nemHome, "catalog", "bump", "--version", "v1.1.0", path); err != nil {
		t.Fatalf("bump: %v", err)
	}
	pkg, err := spec.Parse(mustRead(t, path))
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Versions[0].Version != "v1.1.0" || pkg.Versions[0].Sha256 != nil || pkg.Versions[0].SourceSha256 != "" {
		t.Fatalf("versions[0] = %+v, want bare scalar", pkg.Versions[0])
	}
}

func TestCatalogBumpPartialFailureWritesNothing(t *testing.T) {
	nemHome := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "darwin-arm64") {
			http.NotFound(w, r) // one platform's asset missing
			return
		}
		fmt.Fprint(w, "ok")
	}))
	t.Cleanup(srv.Close)
	dir := writeLintFixture(t, map[string]string{"jq": bumpFixture(srv.URL)})
	path := filepath.Join(dir, "pkgs", "jq", "pkg.yaml")
	before := mustRead(t, path)

	_, errOut, err := runNem(t, nemHome, "catalog", "bump", "--version", "1.8.3", path)
	if err == nil {
		t.Fatal("want error when a platform artifact is missing")
	}
	if !strings.Contains(errOut, "retry later") {
		t.Fatalf("stderr = %q, want not-uploaded-yet hint", errOut)
	}
	if string(mustRead(t, path)) != string(before) {
		t.Fatal("failed bump must not modify the manifest")
	}
}

func TestCatalogBumpRejectsInvalidVersion(t *testing.T) {
	nemHome := t.TempDir()
	srv, _ := bumpArtifactServer(t, "25.0.4+7")
	dir := writeLintFixture(t, map[string]string{"jq": bumpFixture(srv.URL)})
	path := filepath.Join(dir, "pkgs", "jq", "pkg.yaml")
	before := mustRead(t, path)

	_, _, err := runNem(t, nemHome, "catalog", "bump", "--version", "25.0.4+7", path)
	if err == nil {
		t.Fatal("want error for a version outside the OCI tag grammar")
	}
	if string(mustRead(t, path)) != string(before) {
		t.Fatal("failed bump must not modify the manifest")
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
