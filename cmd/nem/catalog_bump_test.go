package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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

// stubBumpListFunc replaces discovery with fn for the test's duration.
func stubBumpListFunc(t *testing.T, fn func(context.Context, *spec.Package) ([]string, error)) {
	t.Helper()
	restore := bumpList
	bumpList = fn
	t.Cleanup(func() { bumpList = restore })
}

// stubBumpList makes discovery return exactly versions, in that order.
func stubBumpList(t *testing.T, versions ...string) {
	t.Helper()
	stubBumpListFunc(t, func(context.Context, *spec.Package) ([]string, error) { return versions, nil })
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

func TestCatalogBumpDeadSourceURLNamesItWithoutRetryHint(t *testing.T) {
	nemHome := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(http.NotFound))
	t.Cleanup(srv.Close)
	dir := writeLintFixture(t, map[string]string{"openssl": sourceBackfillFixture(srv.URL)})
	path := filepath.Join(dir, "pkgs", "openssl", "pkg.yaml")
	before := mustRead(t, path)

	_, errOut, err := runNem(t, nemHome, "catalog", "bump", "--version", "3.4.3", path)
	if err == nil {
		t.Fatal("want error for a dead source URL")
	}
	if !strings.Contains(errOut, srv.URL) {
		t.Fatalf("stderr = %q, want the failing source URL named", errOut)
	}
	if strings.Contains(errOut, "retry later") {
		t.Fatalf("stderr = %q, must not suggest retrying a dead source URL", errOut)
	}
	if string(mustRead(t, path)) != string(before) {
		t.Fatal("failed bump must not modify the manifest")
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

// sweepFixture is a manifest for multi-package sweep tests; discovery
// toggles the versionDiscovery block.
func sweepFixture(name, serverURL, current string, discovery bool) string {
	disc := ""
	if discovery {
		disc = fmt.Sprintf("versionDiscovery:\n  github:\n    repo: example/%s\n", name)
	}
	return fmt.Sprintf(`schema: 2
name: %s
artifact:
  url: "%s/{{.Version}}/{{.OS}}-{{.Arch}}"
install:
  - copy: {src: "{{.Artifact}}", dst: "bin/%s", mode: 0o755}
%sversions:
  - version: %s
    sha256:
      darwin/arm64: "aaa"
      darwin/amd64: "bbb"
      linux/arm64: "ccc"
      linux/amd64: "ddd"
`, name, serverURL, name, disc, current)
}

func namedBumpFixture(name, serverURL, current string) string {
	return sweepFixture(name, serverURL, current, true)
}

// stubBumpListByName routes discovery per package name so a sweep can
// return different versions for each package; a name missing from the
// map fails discovery for that package.
func stubBumpListByName(t *testing.T, byName map[string][]string) {
	t.Helper()
	stubBumpListFunc(t, func(_ context.Context, pkg *spec.Package) ([]string, error) {
		vs, ok := byName[pkg.Name]
		if !ok {
			return nil, fmt.Errorf("discovery unavailable for %s", pkg.Name)
		}
		return vs, nil
	})
}

func TestCatalogBumpDirMixedOutcomesExitsZero(t *testing.T) {
	nemHome := t.TempDir()
	srv, _ := bumpMultiArtifactServer(t, "1.1.0")
	dir := writeLintFixture(t, map[string]string{
		"aa": namedBumpFixture("aa", srv.URL, "1.0.0"),
		"bb": namedBumpFixture("bb", srv.URL, "2.0.0"),
		"cc": namedBumpFixture("cc", srv.URL, "3.0.0"),
		"dd": sweepFixture("dd", srv.URL, "1.0.0", false),
	})
	stubBumpListByName(t, map[string][]string{
		"aa": {"1.1.0", "1.0.0"},
		"bb": {"2.0.0"},
	}) // cc absent → its discovery fails
	bbPath := filepath.Join(dir, "pkgs", "bb", "pkg.yaml")
	bbBefore := mustRead(t, bbPath)

	_, errOut, err := runNem(t, nemHome, "catalog", "bump", dir)
	if err != nil {
		t.Fatalf("sweep must exit zero on per-package failures: %v", err)
	}
	if !strings.Contains(errOut, "discovery unavailable for cc") {
		t.Errorf("stderr = %q, want cc failure warning", errOut)
	}
	if !strings.Contains(errOut, "Checked 4 packages: 1 bumped, 1 up to date, 1 failed, 1 without discovery") {
		t.Fatalf("stderr = %q, want mixed-outcome summary", errOut)
	}
	if string(mustRead(t, bbPath)) != string(bbBefore) {
		t.Fatal("up-to-date package must not be rewritten")
	}
}

func TestCatalogBumpDirSweepsAllPackages(t *testing.T) {
	nemHome := t.TempDir()
	srv, _ := bumpMultiArtifactServer(t, "1.1.0", "1.8.3")
	dir := writeLintFixture(t, map[string]string{
		"aa": namedBumpFixture("aa", srv.URL, "1.0.0"),
		"jq": namedBumpFixture("jq", srv.URL, "1.8.2"),
	})
	stubBumpListByName(t, map[string][]string{
		"aa": {"1.1.0", "1.0.0"},
		"jq": {"1.8.3", "1.8.2"},
	})

	_, errOut, err := runNem(t, nemHome, "catalog", "bump", dir)
	if err != nil {
		t.Fatalf("bump: %v", err)
	}
	for name, want := range map[string]string{"aa": "1.1.0", "jq": "1.8.3"} {
		pkg, err := spec.Parse(mustRead(t, filepath.Join(dir, "pkgs", name, "pkg.yaml")))
		if err != nil {
			t.Fatal(err)
		}
		if pkg.Versions[0].Version != want {
			t.Fatalf("%s head = %q, want %q", name, pkg.Versions[0].Version, want)
		}
	}
	for _, line := range []string{"Bumped aa 1.0.0 → 1.1.0", "Bumped jq 1.8.2 → 1.8.3"} {
		if !strings.Contains(errOut, line) {
			t.Errorf("stderr = %q, want %q", errOut, line)
		}
	}
	if !strings.Contains(errOut, "Checked 2 packages: 2 bumped, 0 up to date, 0 failed, 0 without discovery") {
		t.Fatalf("stderr = %q, want sweep summary", errOut)
	}
}

func TestCatalogBumpVersionRejectsDirTarget(t *testing.T) {
	nemHome := t.TempDir()
	srv, _ := bumpMultiArtifactServer(t, "1.1.0")
	dir := writeLintFixture(t, map[string]string{"aa": namedBumpFixture("aa", srv.URL, "1.0.0")})
	stubBumpListByName(t, map[string][]string{"aa": {"1.1.0", "1.0.0"}})
	path := filepath.Join(dir, "pkgs", "aa", "pkg.yaml")
	before := mustRead(t, path)

	_, _, err := runNem(t, nemHome, "catalog", "bump", "--version", "1.1.0", dir)
	if err == nil || !strings.Contains(err.Error(), "single pkg.yaml") {
		t.Fatalf("err = %v, want single-pkg.yaml rejection", err)
	}
	if string(mustRead(t, path)) != string(before) {
		t.Fatal("rejected bump must not modify any manifest")
	}
}

func TestCatalogBumpDefaultsToCurrentDir(t *testing.T) {
	nemHome := t.TempDir()
	srv, _ := bumpMultiArtifactServer(t, "1.1.0")
	dir := writeLintFixture(t, map[string]string{"aa": namedBumpFixture("aa", srv.URL, "1.0.0")})
	stubBumpListByName(t, map[string][]string{"aa": {"1.1.0", "1.0.0"}})
	t.Chdir(dir)

	_, errOut, err := runNem(t, nemHome, "catalog", "bump")
	if err != nil {
		t.Fatalf("bump: %v", err)
	}
	if !strings.Contains(errOut, "Checked 1 packages: 1 bumped, 0 up to date, 0 failed, 0 without discovery") {
		t.Fatalf("stderr = %q, want sweep summary for the current directory", errOut)
	}
}

// bumpJSONRow mirrors the --json contract rows for assertions.
type bumpJSONRow struct {
	Name    string   `json:"name"`
	Path    string   `json:"path"`
	Current string   `json:"current"`
	Head    string   `json:"head"`
	Added   []string `json:"added"`
	Error   string   `json:"error"`
}

func decodeBumpRows(t *testing.T, out string) []bumpJSONRow {
	t.Helper()
	var rows []bumpJSONRow
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, out)
	}
	return rows
}

func TestCatalogBumpDirJSON(t *testing.T) {
	nemHome := t.TempDir()
	srv, _ := bumpMultiArtifactServer(t, "1.1.0")
	dir := writeLintFixture(t, map[string]string{
		"aa": namedBumpFixture("aa", srv.URL, "1.0.0"),
		"bb": namedBumpFixture("bb", srv.URL, "2.0.0"),
		"cc": namedBumpFixture("cc", srv.URL, "3.0.0"),
		"dd": sweepFixture("dd", srv.URL, "1.0.0", false),
	})
	stubBumpListByName(t, map[string][]string{
		"aa": {"1.1.0", "1.0.0"},
		"bb": {"2.0.0"},
	}) // cc absent → its discovery fails

	out, _, err := runNem(t, nemHome, "catalog", "bump", "--json", dir)
	if err != nil {
		t.Fatalf("bump: %v", err)
	}
	rows := decodeBumpRows(t, out)
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3 (packages without discovery are not rows): %s", len(rows), out)
	}
	byName := map[string]bumpJSONRow{}
	for _, r := range rows {
		byName[r.Name] = r
	}
	aa := byName["aa"]
	if aa.Current != "1.0.0" || aa.Head != "1.1.0" || len(aa.Added) != 1 || aa.Added[0] != "1.1.0" || aa.Error != "" {
		t.Errorf("aa row = %+v, want current 1.0.0, head 1.1.0, added [1.1.0]", aa)
	}
	if want := filepath.Join(dir, "pkgs", "aa", "pkg.yaml"); aa.Path != want {
		t.Errorf("aa path = %q, want %q", aa.Path, want)
	}
	bb := byName["bb"]
	if bb.Current != "2.0.0" || bb.Head != "2.0.0" || len(bb.Added) != 0 || bb.Error != "" {
		t.Errorf("bb row = %+v, want unchanged head 2.0.0 and no added versions", bb)
	}
	cc := byName["cc"]
	if !strings.Contains(cc.Error, "discovery unavailable for cc") || cc.Head != "" || len(cc.Added) != 0 {
		t.Errorf("cc row = %+v, want error and no head", cc)
	}
}

func TestCatalogBumpDirRunsPackagesConcurrently(t *testing.T) {
	nemHome := t.TempDir()
	srv, _ := bumpMultiArtifactServer(t)
	dir := writeLintFixture(t, map[string]string{
		"aa": namedBumpFixture("aa", srv.URL, "1.0.0"),
		"bb": namedBumpFixture("bb", srv.URL, "2.0.0"),
	})
	// Each discovery call parks until a second call is in flight; a
	// sequential sweep never has two and times out into failures. aa
	// then waits for bb to finish, so the row-order assertion below
	// proves rows follow manifest order, not completion order.
	gate := make(chan struct{})
	bbDone := make(chan struct{})
	stubBumpListFunc(t, func(_ context.Context, pkg *spec.Package) ([]string, error) {
		select {
		case gate <- struct{}{}:
		case <-gate:
		case <-time.After(2 * time.Second):
			return nil, fmt.Errorf("no concurrent discovery for %s within 2s", pkg.Name)
		}
		if pkg.Name == "aa" {
			<-bbDone
			return []string{"1.0.0"}, nil
		}
		defer close(bbDone)
		return []string{"2.0.0"}, nil
	})

	out, errOut, err := runNem(t, nemHome, "catalog", "bump", "--json", dir)
	if err != nil {
		t.Fatalf("bump: %v", err)
	}
	if !strings.Contains(errOut, "Checked 2 packages: 0 bumped, 2 up to date, 0 failed, 0 without discovery") {
		t.Fatalf("stderr = %q, want both packages up to date (failures mean the sweep ran sequentially)", errOut)
	}
	rows := decodeBumpRows(t, out)
	if len(rows) != 2 || rows[0].Name != "aa" || rows[1].Name != "bb" {
		t.Fatalf("rows = %+v, want manifest order [aa bb] regardless of completion order", rows)
	}
}

func TestCatalogBumpSingleFileJSON(t *testing.T) {
	nemHome := t.TempDir()
	srv, _ := bumpMultiArtifactServer(t, "1.8.3")
	dir := writeLintFixture(t, map[string]string{"jq": bumpFixture(srv.URL)})
	path := filepath.Join(dir, "pkgs", "jq", "pkg.yaml")
	stubBumpList(t, "1.8.3", "1.8.2")

	out, _, err := runNem(t, nemHome, "catalog", "bump", "--json", path)
	if err != nil {
		t.Fatalf("bump: %v", err)
	}
	rows := decodeBumpRows(t, out)
	if len(rows) != 1 || rows[0].Name != "jq" || rows[0].Current != "1.8.2" || rows[0].Head != "1.8.3" {
		t.Fatalf("rows = %+v, want one jq row 1.8.2 → 1.8.3", rows)
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
