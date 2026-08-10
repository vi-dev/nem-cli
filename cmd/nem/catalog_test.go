package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/vi-dev/nem-cli/internal/catalog"
)

func runNem(t *testing.T, nemHomeDir string, args ...string) (string, string, error) {
	t.Helper()
	t.Setenv("NEM_HOME", nemHomeDir)
	root := newRoot()
	var out, errb bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errb)
	root.SetArgs(append(args, "--color", "never"))
	err := root.Execute()
	if err != nil && ranHook && console != nil {
		// Mirror main()'s own error rendering so tests can assert on the
		// hint wiring through stderr, the same as a real invocation.
		console.Error(err, hintFor(err))
	}
	return out.String(), errb.String(), err
}

func TestCatalogAddListRemove(t *testing.T) {
	nemHome := t.TempDir()
	catDir := t.TempDir()

	_, errb, err := runNem(t, nemHome, "catalog", "add", "dev", catDir)
	if err != nil {
		t.Fatalf("add: %v\n%s", err, errb)
	}
	if !strings.Contains(errb, "Added catalog dev") {
		t.Fatalf("narration: %q", errb)
	}

	out, _, err := runNem(t, nemHome, "catalog", "list")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"NAME", "TYPE", "SOURCE", "official", "oci", "dev", "dir", catDir} {
		if !strings.Contains(out, want) {
			t.Errorf("list missing %q:\n%s", want, out)
		}
	}

	// duplicate rejected
	if _, _, err := runNem(t, nemHome, "catalog", "add", "dev", catDir); err == nil {
		t.Fatal("duplicate add must fail")
	}

	// remove deletes entry and mirror dir
	os.MkdirAll(filepath.Join(nemHome, "catalogs", "dev", "store"), 0o755)
	_, _, err = runNem(t, nemHome, "catalog", "remove", "dev")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(nemHome, "catalogs", "dev")); !os.IsNotExist(err) {
		t.Fatal("mirror dir not deleted")
	}
	out, _, _ = runNem(t, nemHome, "catalog", "list")
	// Checking for "dev" alone would false-positive on the official catalog's
	// fixed ref (ghcr.io/vi-dev/nem-official-catalog:v2, which contains "dev" via
	// "vi-dev"); catDir is unique to the removed entry's source column.
	if strings.Contains(out, catDir) {
		t.Fatalf("dev still listed:\n%s", out)
	}

	if _, _, err := runNem(t, nemHome, "catalog", "remove", "ghost"); err == nil {
		t.Fatal("removing unknown catalog must fail")
	}
}

func TestCatalogAddTypeDetection(t *testing.T) {
	nemHome := t.TempDir()
	if _, _, err := runNem(t, nemHome, "catalog", "add", "team", "ghcr.io/org/cat:v2"); err != nil {
		t.Fatalf("oci add: %v", err)
	}
	out, _, _ := runNem(t, nemHome, "catalog", "list")
	if !strings.Contains(out, "ghcr.io/org/cat:v2") {
		t.Fatalf("oci ref not listed:\n%s", out)
	}
	// --type dir forces even for a non-existing path? No: dir paths must exist? Config only
	// requires absolute. Force-dir with an absolute path:
	forced := t.TempDir()
	if _, _, err := runNem(t, nemHome, "catalog", "add", "forced", forced, "--type", "dir"); err != nil {
		t.Fatalf("forced dir add: %v", err)
	}
}

func TestCatalogAddRejectsTaglessOCIRef(t *testing.T) {
	nemHome := t.TempDir()
	if _, _, err := runNem(t, nemHome, "catalog", "add", "o", "ghcr.io/x/y"); err == nil {
		t.Fatal("tagless oci ref must be rejected")
	}
	out, _, _ := runNem(t, nemHome, "catalog", "list")
	if strings.Contains(out, "ghcr.io/x/y") {
		t.Fatalf("rejected catalog must not be persisted:\n%s", out)
	}
}

func TestCatalogUpdateSyncsOCI(t *testing.T) {
	nemHome := t.TempDir()
	var synced []string
	orig := syncCatalog
	syncCatalog = func(ctx context.Context, ref, storePath string) (string, error) {
		synced = append(synced, ref+"|"+storePath)
		return "sha256:fake", nil
	}
	defer func() { syncCatalog = orig }()

	dir := t.TempDir()
	if _, _, err := runNem(t, nemHome, "catalog", "add", "dev", dir); err != nil {
		t.Fatal(err)
	}
	_, errb, err := runNem(t, nemHome, "catalog", "update")
	if err != nil {
		t.Fatalf("update: %v\n%s", err, errb)
	}
	if len(synced) != 1 || !strings.Contains(synced[0], catalog.OfficialRef) {
		t.Fatalf("synced: %v", synced)
	}
	if !strings.Contains(errb, "Synced catalog official") {
		t.Fatalf("narration: %q", errb)
	}

	// named dir catalog warns, exits 0
	_, errb, err = runNem(t, nemHome, "catalog", "update", "dev")
	if err != nil || !strings.Contains(errb, "dir catalog") {
		t.Fatalf("dir update: %v, %q", err, errb)
	}
	// unknown errors
	if _, _, err := runNem(t, nemHome, "catalog", "update", "ghost"); err == nil {
		t.Fatal("unknown catalog must fail")
	}
}

func TestCatalogUpdateSkipsDisabled(t *testing.T) {
	dir := t.TempDir()
	h := testNemHome(dir)
	if err := catalog.SaveConfig(h, &catalog.Config{Catalogs: []catalog.Entry{
		{Name: "on", Type: "oci", Ref: "ghcr.io/x/on:v2"},
		{Name: "off", Type: "oci", Ref: "ghcr.io/x/off:v2", Disabled: true},
	}}); err != nil {
		t.Fatal(err)
	}
	var synced []string
	orig := syncCatalog
	syncCatalog = func(ctx context.Context, ref, storePath string) (string, error) {
		synced = append(synced, ref)
		return "", nil
	}
	defer func() { syncCatalog = orig }()

	if _, _, err := runNem(t, dir, "catalog", "update"); err != nil {
		t.Fatalf("update: %v", err)
	}
	if !slices.Equal(synced, []string{"ghcr.io/x/on:v2"}) {
		t.Fatalf("update should sync only enabled catalogs, synced %v", synced)
	}
}

func TestCatalogReorder(t *testing.T) {
	nemHome := t.TempDir()
	dir := t.TempDir()
	runNem(t, nemHome, "catalog", "add", "dev", dir)

	if _, _, err := runNem(t, nemHome, "catalog", "reorder", "dev", "official"); err != nil {
		t.Fatal(err)
	}
	out, _, _ := runNem(t, nemHome, "catalog", "list")
	if strings.Index(out, "dev") > strings.Index(out, "official") {
		t.Fatalf("order not applied:\n%s", out)
	}
	if _, _, err := runNem(t, nemHome, "catalog", "reorder", "dev"); err == nil {
		t.Fatal("partial reorder must fail")
	}
}
