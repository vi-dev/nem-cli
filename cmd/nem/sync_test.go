package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"oras.land/oras-go/v2/content/oci"

	"github.com/vi-dev/nem-cli/internal/home"
	"github.com/vi-dev/nem-cli/internal/install"
	"github.com/vi-dev/nem-cli/internal/ocix"
	"github.com/vi-dev/nem-cli/internal/project"
	"github.com/vi-dev/nem-cli/internal/spec"
)

const syncToolYAML = `
schema: 2
name: tool
description: a test tool
artifact:
  oci: ":{{.Version}}"
install:
  - extract: {}
versions:
  - v1.0.0
`

func TestSyncInstallsMissingEntry(t *testing.T) {
	nemHomeDir := t.TempDir()
	catalogRoot := downloadableDirCatalog(t)
	projDir := t.TempDir()
	chdir(t, projDir)

	if _, errb, err := runNem(t, nemHomeDir, "catalog", "add", "demo", catalogRoot); err != nil {
		t.Fatalf("catalog add: %v\n%s", err, errb)
	}
	if _, errb, err := runNem(t, nemHomeDir, "use", "demo:tool"); err != nil {
		t.Fatalf("use: %v\n%s", err, errb)
	}

	h := testNemHome(nemHomeDir)
	installDir, err := h.PackageDir("tool", "v1.0.0")
	if err != nil {
		t.Fatalf("PackageDir: %v", err)
	}
	if err := os.RemoveAll(installDir); err != nil {
		t.Fatalf("remove install dir: %v", err)
	}
	if install.IsInstalled(h, "tool", "v1.0.0") {
		t.Fatal("setup broken: tool still reports installed after removal")
	}

	out, errb, err := runNem(t, nemHomeDir, "sync")
	if err != nil {
		t.Fatalf("sync: %v\nstdout: %s\nstderr: %s", err, out, errb)
	}
	if !strings.Contains(errb, "Installed tool v1.0.0") {
		t.Fatalf("narration missing install success line: %q", errb)
	}
	if !install.IsInstalled(h, "tool", "v1.0.0") {
		t.Fatal("sync did not reinstall tool v1.0.0")
	}

	out2, errb2, err := runNem(t, nemHomeDir, "sync")
	if err != nil {
		t.Fatalf("second sync: %v\nstdout: %s\nstderr: %s", err, out2, errb2)
	}
	if out2 != "" || errb2 != "" {
		t.Fatalf("second sync must print nothing: stdout=%q stderr=%q", out2, errb2)
	}
}

func TestSyncNoManifestFails(t *testing.T) {
	nemHomeDir := t.TempDir()
	projDir := t.TempDir()
	chdir(t, projDir)

	_, _, err := runNem(t, nemHomeDir, "sync")
	if err == nil {
		t.Fatal("want error when no nem.toml anywhere")
	}
	if !errors.Is(err, project.ErrNoManifest) {
		t.Fatalf("want project.ErrNoManifest, got %v", err)
	}
}

// syncedOCICatalogStore builds a fake OCI catalog holding the "tool"
// package and syncs it straight into name's local mirror under nemHomeDir,
// bypassing any real registry.
func syncedOCICatalogStore(t *testing.T, h home.Home, name string) {
	t.Helper()
	storePath, err := h.CatalogStore(name)
	if err != nil {
		t.Fatalf("CatalogStore: %v", err)
	}
	src, err := oci.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ocix.PushFakeCatalogForTest(t, src, []ocix.FakeEntry{{
		Name: "tool", Description: "a test tool", Latest: "v1.0.0",
		YAML: []byte(syncToolYAML),
	}}, "2")
	if _, err := ocix.SyncFrom(context.Background(), src, "v2", storePath); err != nil {
		t.Fatalf("SyncFrom: %v", err)
	}
}

func TestSyncDigestMismatchErrors(t *testing.T) {
	nemHomeDir := t.TempDir()
	h := testNemHome(nemHomeDir)
	projDir := t.TempDir()
	chdir(t, projDir)

	if _, errb, err := runNem(t, nemHomeDir, "catalog", "add", "demo", "ghcr.io/x/y:v2"); err != nil {
		t.Fatalf("catalog add: %v\n%s", err, errb)
	}
	syncedOCICatalogStore(t, h, "demo")

	if err := os.WriteFile(filepath.Join(projDir, "nem.toml"), []byte("[tools]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	current := spec.Current().String()
	lockBody := "# machine-written by nem — do not edit\nversion = 1\n\n" +
		"[[package]]\nname = \"tool\"\nversion = \"v1.0.0\"\ncatalog = \"demo\"\n" +
		"direct = true\nplatforms = [\"" + current + "\"]\ndigest = \"sha256:bogus\"\n"
	if err := os.WriteFile(filepath.Join(projDir, "nem.lock"), []byte(lockBody), 0o644); err != nil {
		t.Fatal(err)
	}

	_, errb, err := runNem(t, nemHomeDir, "sync")
	if err == nil {
		t.Fatal("want digest mismatch error")
	}
	if !strings.Contains(err.Error(), "changed") {
		t.Fatalf("error should mention the content changed: %v", err)
	}
	if !strings.Contains(errb, "Re-lock") {
		t.Fatalf("stderr should carry the digest-mismatch hint: %q", errb)
	}
}
