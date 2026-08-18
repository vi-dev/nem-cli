package main

import (
	"encoding/json"
	"strings"
	"testing"

	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/oci"

	"github.com/vi-dev/nem-cli/internal/ocix"
)

const diffFixtureV1 = `schema: 2
name: atool
description: Test tool
artifact:
  url: "https://example.com/atool-{{.Version}}.tar.gz"
install:
  - extract: {strip: 1}
bins: ["bin"]
versions:
  - version: 1.0.0
    sha256:
      darwin/arm64: "aaa"
      darwin/amd64: "aaa"
      linux/arm64: "aaa"
      linux/amd64: "aaa"
`

const diffFixtureV2 = `schema: 2
name: atool
description: Test tool
artifact:
  url: "https://example.com/atool-{{.Version}}.tar.gz"
install:
  - extract: {strip: 1}
bins: ["bin"]
versions:
  - version: 1.1.0
    sha256:
      darwin/arm64: "bbb"
      darwin/amd64: "bbb"
      linux/arm64: "bbb"
      linux/amd64: "bbb"
  - version: 1.0.0
    sha256:
      darwin/arm64: "aaa"
      darwin/amd64: "aaa"
      linux/arm64: "aaa"
      linux/amd64: "aaa"
`

func withFakeCatalog(t *testing.T, store oras.ReadOnlyTarget) {
	t.Helper()
	restore := openCatalog
	openCatalog = func(ref string) (oras.ReadOnlyTarget, string, error) { return store, "v2", nil }
	t.Cleanup(func() { openCatalog = restore })
}

func newCatalogStore(t *testing.T, entries []ocix.FakeEntry) oras.Target {
	t.Helper()
	store, err := oci.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ocix.PushFakeCatalogForTest(t, store, entries, ocix.SchemaVersion)
	return store
}

func decodeDiffRows(t *testing.T, out string) []diffRow {
	t.Helper()
	var rows []diffRow
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("unmarshal diff JSON: %v\noutput: %s", err, out)
	}
	return rows
}

func TestCatalogDiffJSONUnchangedAndUpdated(t *testing.T) {
	nemHome := t.TempDir()
	dir := writeLintFixture(t, map[string]string{"atool": diffFixtureV2})
	store := newCatalogStore(t, []ocix.FakeEntry{
		{Name: "atool", Description: "Test tool", Latest: "1.0.0", YAML: []byte(diffFixtureV1)},
	})
	withFakeCatalog(t, store)

	out, errOut, err := runNem(t, nemHome, "catalog", "diff", "ghcr.io/org/cat:v2", dir, "--json")
	if err != nil {
		t.Fatalf("diff: %v\nstderr: %s", err, errOut)
	}
	rows := decodeDiffRows(t, out)
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	r := rows[0]
	if r.Name != "atool" || r.Status != "updated" {
		t.Fatalf("row = %+v, want atool updated", r)
	}
	if r.Published != "1.0.0" || r.Local != "1.1.0" {
		t.Fatalf("published/local = %q/%q, want 1.0.0/1.1.0", r.Published, r.Local)
	}
	if len(r.VersionsAdded) != 1 || r.VersionsAdded[0] != "1.1.0" {
		t.Fatalf("versionsAdded = %v, want [1.1.0]", r.VersionsAdded)
	}
	if r.VersionsRemoved == nil || len(r.VersionsRemoved) != 0 {
		t.Fatalf("versionsRemoved = %v, want []", r.VersionsRemoved)
	}
	if r.Source {
		t.Fatal("source = true, want false for a prebuilt package")
	}
}

func TestCatalogDiffJSONIncludesUnchangedRows(t *testing.T) {
	nemHome := t.TempDir()
	dir := writeLintFixture(t, map[string]string{"atool": diffFixtureV1})
	store := newCatalogStore(t, []ocix.FakeEntry{
		{Name: "atool", Description: "Test tool", Latest: "1.0.0", YAML: []byte(diffFixtureV1)},
	})
	withFakeCatalog(t, store)

	out, errOut, err := runNem(t, nemHome, "catalog", "diff", "ghcr.io/org/cat:v2", dir, "--json")
	if err != nil {
		t.Fatalf("diff: %v\nstderr: %s", err, errOut)
	}
	rows := decodeDiffRows(t, out)
	if len(rows) != 1 || rows[0].Status != "unchanged" {
		t.Fatalf("rows = %+v, want one unchanged row", rows)
	}
	if rows[0].Published != "1.0.0" || rows[0].Local != "1.0.0" {
		t.Fatalf("published/local = %q/%q, want 1.0.0/1.0.0", rows[0].Published, rows[0].Local)
	}
}

func TestCatalogDiffJSONNewPackage(t *testing.T) {
	nemHome := t.TempDir()
	dir := writeLintFixture(t, map[string]string{
		"atool": diffFixtureV1,
		"ztool": missingFixtureSourcePkg,
	})
	store := newCatalogStore(t, []ocix.FakeEntry{
		{Name: "atool", Description: "Test tool", Latest: "1.0.0", YAML: []byte(diffFixtureV1)},
	})
	withFakeCatalog(t, store)

	out, errOut, err := runNem(t, nemHome, "catalog", "diff", "ghcr.io/org/cat:v2", dir, "--json")
	if err != nil {
		t.Fatalf("diff: %v\nstderr: %s", err, errOut)
	}
	rows := decodeDiffRows(t, out)
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2 (sorted: atool, ztool)", len(rows))
	}
	if rows[0].Name != "atool" || rows[1].Name != "ztool" {
		t.Fatalf("order = %s, %s; want atool, ztool", rows[0].Name, rows[1].Name)
	}
	z := rows[1]
	if z.Status != "new" || !z.Source {
		t.Fatalf("row = %+v, want new source row", z)
	}
	if z.Published != "" {
		t.Fatalf("published = %q, want empty for new", z.Published)
	}
	if len(z.VersionsAdded) != 2 || z.VersionsAdded[0] != "1.1.0" || z.VersionsAdded[1] != "1.0.0" {
		t.Fatalf("versionsAdded = %v, want all declared versions", z.VersionsAdded)
	}
}

func TestCatalogDiffRespelledVersionIsNotADelta(t *testing.T) {
	nemHome := t.TempDir()
	published := strings.Replace(diffFixtureV1, "version: 1.0.0", "version: v1.0.0", 1)
	dir := writeLintFixture(t, map[string]string{"atool": diffFixtureV1})
	store := newCatalogStore(t, []ocix.FakeEntry{
		{Name: "atool", Description: "Test tool", Latest: "v1.0.0", YAML: []byte(published)},
	})
	withFakeCatalog(t, store)

	out, errOut, err := runNem(t, nemHome, "catalog", "diff", "ghcr.io/org/cat:v2", dir, "--json")
	if err != nil {
		t.Fatalf("diff: %v\nstderr: %s", err, errOut)
	}
	rows := decodeDiffRows(t, out)
	if len(rows) != 1 || rows[0].Status != "updated" {
		t.Fatalf("rows = %+v, want one updated row (digest differs)", rows)
	}
	if len(rows[0].VersionsAdded) != 0 || len(rows[0].VersionsRemoved) != 0 {
		t.Fatalf("deltas = %v/%v, want both empty for a respelled version",
			rows[0].VersionsAdded, rows[0].VersionsRemoved)
	}
}

func TestCatalogDiffReportsRemovedPackages(t *testing.T) {
	nemHome := t.TempDir()
	dir := writeLintFixture(t, map[string]string{"atool": diffFixtureV1})
	store := newCatalogStore(t, []ocix.FakeEntry{
		{Name: "atool", Description: "Test tool", Latest: "1.0.0", YAML: []byte(diffFixtureV1)},
		{Name: "ztool", Description: "Gone", Latest: "1.1.0", YAML: []byte(missingFixtureSourcePkg)},
	})
	withFakeCatalog(t, store)

	out, errOut, err := runNem(t, nemHome, "catalog", "diff", "ghcr.io/org/cat:v2", dir, "--json")
	if err != nil {
		t.Fatalf("diff: %v\nstderr: %s", err, errOut)
	}
	rows := decodeDiffRows(t, out)
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	z := rows[1]
	if z.Name != "ztool" || z.Status != "removed" {
		t.Fatalf("row = %+v, want ztool removed", z)
	}
	if z.Local != "" || z.Path != "" {
		t.Fatalf("local/path = %q/%q, want empty for removed", z.Local, z.Path)
	}
	if z.Published != "1.1.0" {
		t.Fatalf("published = %q, want 1.1.0", z.Published)
	}
	if !z.Source {
		t.Fatal("source = false, want true (published manifest declares build)")
	}
	if len(z.VersionsRemoved) != 2 {
		t.Fatalf("versionsRemoved = %v, want both published versions", z.VersionsRemoved)
	}
	if len(z.VersionsAdded) != 0 {
		t.Fatalf("versionsAdded = %v, want []", z.VersionsAdded)
	}
}

func TestCatalogDiffSingleFileSkipsRemovedSweep(t *testing.T) {
	nemHome := t.TempDir()
	dir := writeLintFixture(t, map[string]string{"atool": diffFixtureV2})
	store := newCatalogStore(t, []ocix.FakeEntry{
		{Name: "atool", Description: "Test tool", Latest: "1.0.0", YAML: []byte(diffFixtureV1)},
		{Name: "ztool", Description: "Other", Latest: "1.1.0", YAML: []byte(missingFixtureSourcePkg)},
	})
	withFakeCatalog(t, store)

	out, errOut, err := runNem(t, nemHome, "catalog", "diff", "ghcr.io/org/cat:v2",
		dir+"/pkgs/atool/pkg.yaml", "--json")
	if err != nil {
		t.Fatalf("diff: %v\nstderr: %s", err, errOut)
	}
	rows := decodeDiffRows(t, out)
	if len(rows) != 1 || rows[0].Name != "atool" || rows[0].Status != "updated" {
		t.Fatalf("rows = %+v, want only atool updated (no removed sweep)", rows)
	}
}

func TestCatalogDiffHumanTableListsOnlyChanged(t *testing.T) {
	nemHome := t.TempDir()
	dir := writeLintFixture(t, map[string]string{
		"atool": diffFixtureV2,
		"btool": strings.Replace(diffFixtureV1, "name: atool", "name: btool", 1),
	})
	store := newCatalogStore(t, []ocix.FakeEntry{
		{Name: "atool", Description: "Test tool", Latest: "1.0.0", YAML: []byte(diffFixtureV1)},
		{Name: "btool", Description: "Test tool", Latest: "1.0.0",
			YAML: []byte(strings.Replace(diffFixtureV1, "name: atool", "name: btool", 1))},
		{Name: "ztool", Description: "Gone", Latest: "1.1.0", YAML: []byte(missingFixtureSourcePkg)},
	})
	withFakeCatalog(t, store)

	out, errOut, err := runNem(t, nemHome, "catalog", "diff", "ghcr.io/org/cat:v2", dir)
	if err != nil {
		t.Fatalf("diff: %v\nstderr: %s", err, errOut)
	}
	if !strings.Contains(out, "atool") || !strings.Contains(out, "updated") {
		t.Fatalf("stdout = %q, want the updated atool row", out)
	}
	if !strings.Contains(out, "ztool") || !strings.Contains(out, "removed") {
		t.Fatalf("stdout = %q, want the removed ztool row", out)
	}
	if strings.Contains(out, "btool") {
		t.Fatalf("stdout = %q, unchanged btool must not be listed", out)
	}
	if !strings.Contains(out, "-") {
		t.Fatalf("stdout = %q, want '-' for the removed row's absent local side", out)
	}
}

func TestCatalogDiffFailsWholeCommandOnUnparseableManifest(t *testing.T) {
	nemHome := t.TempDir()
	dir := writeLintFixture(t, map[string]string{
		"atool":   diffFixtureV1,
		"badtool": "schema: [not valid yaml",
	})
	store := newCatalogStore(t, []ocix.FakeEntry{
		{Name: "atool", Description: "Test tool", Latest: "1.0.0", YAML: []byte(diffFixtureV1)},
	})
	withFakeCatalog(t, store)

	out, _, err := runNem(t, nemHome, "catalog", "diff", "ghcr.io/org/cat:v2", dir, "--json")
	if err == nil {
		t.Fatal("diff succeeded on an unparseable manifest, want whole-command failure")
	}
	if strings.Contains(out, "\"status\"") {
		t.Fatalf("stdout = %q, want no JSON rows on failure", out)
	}
}

func TestCatalogDiffJSONOmitsAbsentKeys(t *testing.T) {
	nemHome := t.TempDir()
	dir := writeLintFixture(t, map[string]string{"atool": diffFixtureV1})
	store := newCatalogStore(t, nil)
	withFakeCatalog(t, store)

	out, errOut, err := runNem(t, nemHome, "catalog", "diff", "ghcr.io/org/cat:v2", dir, "--json")
	if err != nil {
		t.Fatalf("diff: %v\nstderr: %s", err, errOut)
	}
	if strings.Contains(out, "\"published\"") {
		t.Fatalf("stdout = %q, want no published key for a new-only diff", out)
	}
	if !strings.Contains(out, "\"versionsAdded\": [") {
		t.Fatalf("stdout = %q, want versionsAdded array present", out)
	}
}
