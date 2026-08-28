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
	catalogRoot := downloadableDirCatalog(t, "")
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

func TestSyncWarnsOnManifestDrift(t *testing.T) {
	nemHomeDir := t.TempDir()
	catalogRoot := downloadableDirCatalog(t, "")
	projDir := t.TempDir()
	chdir(t, projDir)

	if _, errb, err := runNem(t, nemHomeDir, "catalog", "add", "demo", catalogRoot); err != nil {
		t.Fatalf("catalog add: %v\n%s", err, errb)
	}
	if _, errb, err := runNem(t, nemHomeDir, "use", "demo:tool"); err != nil {
		t.Fatalf("use: %v\n%s", err, errb)
	}

	manifestPath := filepath.Join(projDir, "nem.toml")
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(manifest), "[tools]\n", "[tools]\n\"demo:other\" = \"v2.0.0\"\n", 1)
	if edited == string(manifest) {
		t.Fatalf("could not find [tools] section to edit: %q", manifest)
	}
	if err := os.WriteFile(manifestPath, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	_, errb, err := runNem(t, nemHomeDir, "sync")
	if err != nil {
		t.Fatalf("sync must not fail on drift: %v\nstderr: %s", err, errb)
	}
	if !strings.Contains(errb, "other@v2.0.0") {
		t.Fatalf("stderr should name the drifted tool: %q", errb)
	}
	if !strings.Contains(errb, "nem lock") {
		t.Fatalf("stderr should suggest `nem lock`: %q", errb)
	}
}

func TestSyncWarnsOnCatalogSwitch(t *testing.T) {
	nemHomeDir := t.TempDir()
	catalogRoot := downloadableDirCatalog(t, "")
	projDir := t.TempDir()
	chdir(t, projDir)

	if _, errb, err := runNem(t, nemHomeDir, "catalog", "add", "demo", catalogRoot); err != nil {
		t.Fatalf("catalog add: %v\n%s", err, errb)
	}
	if _, errb, err := runNem(t, nemHomeDir, "use", "demo:tool"); err != nil {
		t.Fatalf("use: %v\n%s", err, errb)
	}

	manifestPath := filepath.Join(projDir, "nem.toml")
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(manifest), "'demo:tool'", "'other:tool'", 1)
	if edited == string(manifest) {
		t.Fatalf("could not find demo:tool entry to edit: %q", manifest)
	}
	if err := os.WriteFile(manifestPath, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	_, errb, err := runNem(t, nemHomeDir, "sync")
	if err != nil {
		t.Fatalf("sync must not fail on drift: %v\nstderr: %s", err, errb)
	}
	if !strings.Contains(errb, "other:tool@v1.0.0") {
		t.Fatalf("stderr should flag the catalog switch: %q", errb)
	}
	if !strings.Contains(errb, "nem lock") {
		t.Fatalf("stderr should suggest `nem lock`: %q", errb)
	}
}

func TestSyncWarnsWhenOnlyADependencyCovers(t *testing.T) {
	nemHomeDir := t.TempDir()
	projDir := t.TempDir()
	chdir(t, projDir)

	if err := os.WriteFile(filepath.Join(projDir, "nem.toml"), []byte("[tools]\ntool = \"v1.0.0\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lockBody := "# machine-written by nem — do not edit\nversion = 2\n\n" +
		"[[package]]\nname = \"tool\"\nversion = \"v1.0.0\"\ncatalog = \"demo\"\n" +
		"direct = false\nplatforms = []\non_path = true\n"
	if err := os.WriteFile(filepath.Join(projDir, "nem.lock"), []byte(lockBody), 0o644); err != nil {
		t.Fatal(err)
	}

	_, errb, err := runNem(t, nemHomeDir, "sync")
	if err != nil {
		t.Fatalf("sync must not fail on drift: %v\nstderr: %s", err, errb)
	}
	if !strings.Contains(errb, "tool@v1.0.0") {
		t.Fatalf("a dependency-only lock entry must not cover a direct declaration: %q", errb)
	}
}

func TestSyncWarnsOnUnpinnedManifestEntry(t *testing.T) {
	nemHomeDir := t.TempDir()
	projDir := t.TempDir()
	chdir(t, projDir)

	if err := os.WriteFile(filepath.Join(projDir, "nem.toml"), []byte("[tools]\ntool = \"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projDir, "nem.lock"), []byte("version = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, errb, err := runNem(t, nemHomeDir, "sync")
	if err != nil {
		t.Fatalf("sync must not fail on an unpinned entry: %v\nstderr: %s", err, errb)
	}
	if !strings.Contains(errb, "declares tool without a version") {
		t.Fatalf("stderr should explain the entry is unpinned: %q", errb)
	}
	if !strings.Contains(errb, "nem use tool@<version>") {
		t.Fatalf("stderr should suggest pinning, not `nem lock`: %q", errb)
	}
	if strings.Contains(errb, "tool@,") {
		t.Fatalf("stderr must not contain the malformed tool@ form: %q", errb)
	}
}

func TestSyncWarnsOnUnreadableManifest(t *testing.T) {
	nemHomeDir := t.TempDir()
	projDir := t.TempDir()
	chdir(t, projDir)

	if err := os.WriteFile(filepath.Join(projDir, "nem.toml"), []byte("not toml ["), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projDir, "nem.lock"), []byte("version = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, errb, err := runNem(t, nemHomeDir, "sync")
	if err != nil {
		t.Fatalf("sync must not fail on an unreadable manifest: %v\nstderr: %s", err, errb)
	}
	if !strings.Contains(errb, "drift") {
		t.Fatalf("stderr should warn the drift check was skipped: %q", errb)
	}
}

func TestSyncGlobalInstallsFromGlobalLock(t *testing.T) {
	nemHomeDir := t.TempDir()
	catalogRoot := downloadableDirCatalog(t, "")
	chdir(t, t.TempDir()) // no project manifest anywhere

	if _, errb, err := runNem(t, nemHomeDir, "catalog", "add", "demo", catalogRoot); err != nil {
		t.Fatalf("catalog add: %v\n%s", err, errb)
	}
	if _, errb, err := runNem(t, nemHomeDir, "use", "-g", "demo:tool"); err != nil {
		t.Fatalf("use -g: %v\n%s", err, errb)
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

	out, errb, err := runNem(t, nemHomeDir, "sync", "-g")
	if err != nil {
		t.Fatalf("sync -g: %v\nstdout: %s\nstderr: %s", err, out, errb)
	}
	if !strings.Contains(errb, "Installed tool v1.0.0") {
		t.Fatalf("narration missing install success line: %q", errb)
	}
	if !install.IsInstalled(h, "tool", "v1.0.0") {
		t.Fatal("sync -g did not reinstall tool v1.0.0")
	}
}

func TestSyncGlobalWithoutGlobalLockSucceedsSilently(t *testing.T) {
	nemHomeDir := t.TempDir()
	chdir(t, t.TempDir())

	out, errb, err := runNem(t, nemHomeDir, "sync", "-g")
	if err != nil {
		t.Fatalf("sync -g with no global lock: %v\nstderr: %s", err, errb)
	}
	if out != "" || errb != "" {
		t.Fatalf("sync -g with no global lock must print nothing: stdout=%q stderr=%q", out, errb)
	}
}

func TestSyncFailedInstallReportsErrorOnce(t *testing.T) {
	nemHomeDir := t.TempDir()
	catalogRoot := downloadableDirCatalog(t, "")
	projDir := t.TempDir()
	chdir(t, projDir)

	if _, errb, err := runNem(t, nemHomeDir, "catalog", "add", "demo", catalogRoot); err != nil {
		t.Fatalf("catalog add: %v\n%s", err, errb)
	}
	if _, errb, err := runNem(t, nemHomeDir, "use", "demo:tool"); err != nil {
		t.Fatalf("use: %v\n%s", err, errb)
	}

	// A locked version the catalog no longer offers: install must fail, and
	// the failure must reach stderr exactly once.
	lockPath := filepath.Join(projDir, "nem.lock")
	lock, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.ReplaceAll(string(lock), "v1.0.0", "v9.9.9")
	if err := os.WriteFile(lockPath, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	_, errb, err := runNem(t, nemHomeDir, "sync")
	if err == nil {
		t.Fatal("want install failure for a version the catalog does not offer")
	}
	if got := strings.Count(strings.ToLower(errb), "not offered"); got != 1 {
		t.Fatalf("error must appear exactly once on stderr, got %d:\n%s", got, errb)
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
	if _, err := ocix.SyncLocalCatalog(context.Background(), src, "v2", storePath, nil); err != nil {
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

func TestSyncDoesNotWarnAboutMissingInstalls(t *testing.T) {
	nemHomeDir := t.TempDir()
	projDir := t.TempDir()
	chdir(t, projDir)
	writeFile(t, filepath.Join(projDir, "nem.toml"), "")

	platforms := []string{spec.Current().String()}
	globalLock := &project.Lockfile{Path: testNemHome(nemHomeDir).GlobalLock(), Packages: []project.LockEntry{
		{Name: "gtool", Version: "v2.0.0", Catalog: "test", Direct: true, Platforms: platforms},
		{Name: "htool", Version: "v3.0.0", Catalog: "test", Direct: true, Platforms: platforms},
	}}
	if err := project.WriteLock(globalLock); err != nil {
		t.Fatalf("WriteLock: %v", err)
	}

	// `nem status` alone owns the not-installed summary.
	_, errb, err := runNem(t, nemHomeDir, "sync")
	if err != nil {
		t.Fatalf("sync: %v\n%s", err, errb)
	}
	if errb != "" {
		t.Fatalf("expected silent stderr, got: %q", errb)
	}
}
