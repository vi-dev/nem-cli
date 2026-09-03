package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vi-dev/nem-cli/internal/install"
	"github.com/vi-dev/nem-cli/internal/project"
)

// versionedDirCatalog writes a dir catalog holding one package per tools
// entry, each declaring the given versions (newest first). Every version of
// every package shares one real tar.gz served by an httptest server.
func versionedDirCatalog(t *testing.T, tools map[string][]string) string {
	t.Helper()
	archive := makeTarGz(t, map[string]string{"bin/tool": "tool binary bytes"})
	sum := sha256.Sum256(archive)
	sha := hex.EncodeToString(sum[:])

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(archive)
	}))
	t.Cleanup(srv.Close)

	root := t.TempDir()
	for name, versions := range tools {
		dir := filepath.Join(root, "pkgs", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		var b strings.Builder
		fmt.Fprintf(&b, `
schema: 2
name: %s
description: test tool %s
artifact:
  url: %q
install:
  - extract: {}
versions:
`, name, name, srv.URL)
		for _, v := range versions {
			fmt.Fprintf(&b, `  - version: %s
    sha256:
      darwin/arm64: %q
      darwin/amd64: %q
      linux/arm64: %q
      linux/amd64: %q
`, v, sha, sha, sha, sha)
		}
		if err := os.WriteFile(filepath.Join(dir, "pkg.yaml"), []byte(b.String()), 0o644); err != nil {
			t.Fatalf("write pkg.yaml: %v", err)
		}
	}
	return root
}

func TestUpdateBumpsAllDeclaredToolsToLatest(t *testing.T) {
	nemHomeDir := t.TempDir()
	catalogRoot := versionedDirCatalog(t, map[string][]string{"tool": {"v1.1.0", "v1.0.0"}})
	projDir := t.TempDir()
	chdir(t, projDir)

	if _, errb, err := runNem(t, nemHomeDir, "catalog", "add", "demo", catalogRoot); err != nil {
		t.Fatalf("catalog add: %v\n%s", err, errb)
	}
	if _, errb, err := runNem(t, nemHomeDir, "use", "demo:tool@v1.0.0"); err != nil {
		t.Fatalf("use: %v\n%s", err, errb)
	}

	out, errb, err := runNem(t, nemHomeDir, "update")
	if err != nil {
		t.Fatalf("update: %v\nstdout: %s\nstderr: %s", err, out, errb)
	}
	if out != "" {
		t.Fatalf("stdout must stay empty like use's: %q", out)
	}
	if !strings.Contains(errb, "Updated tool v1.0.0 → v1.1.0") {
		t.Fatalf("stderr should narrate the update: %q", errb)
	}

	m, err := project.LoadManifest(filepath.Join(projDir, "nem.toml"))
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if len(m.Tools) != 1 || m.Tools[0].Key.String() != "demo:tool" || m.Tools[0].Version != "v1.1.0" {
		t.Fatalf("manifest tools after update: %+v", m.Tools)
	}

	lf, err := project.LoadLock(filepath.Join(projDir, "nem.lock"))
	if err != nil {
		t.Fatalf("LoadLock: %v", err)
	}
	if len(lf.Packages) != 1 || lf.Packages[0].Version != "v1.1.0" {
		t.Fatalf("lock packages after update: %+v", lf.Packages)
	}

	if !install.IsInstalled(testNemHome(nemHomeDir), "tool", "v1.1.0") {
		t.Fatal("tool v1.1.0 not installed after update")
	}
}

func TestUpdateSingleToolLeavesOthersPinned(t *testing.T) {
	nemHomeDir := t.TempDir()
	catalogRoot := versionedDirCatalog(t, map[string][]string{
		"toola": {"v1.1.0", "v1.0.0"},
		"toolb": {"v2.1.0", "v2.0.0"},
	})
	projDir := t.TempDir()
	chdir(t, projDir)

	if _, _, err := runNem(t, nemHomeDir, "catalog", "add", "demo", catalogRoot); err != nil {
		t.Fatal(err)
	}
	if _, errb, err := runNem(t, nemHomeDir, "use", "demo:toola@v1.0.0", "demo:toolb@v2.0.0"); err != nil {
		t.Fatalf("use: %v\n%s", err, errb)
	}

	out, errb, err := runNem(t, nemHomeDir, "update", "toola")
	if err != nil {
		t.Fatalf("update toola: %v\nstdout: %s\nstderr: %s", err, out, errb)
	}
	if out != "" {
		t.Fatalf("stdout must stay empty like use's: %q", out)
	}
	if !strings.Contains(errb, "Updated toola v1.0.0 → v1.1.0") {
		t.Fatalf("stderr should narrate the update: %q", errb)
	}
	if strings.Contains(errb, "Updated toolb") {
		t.Fatalf("the unselected tool must not be narrated as updated: %q", errb)
	}

	m, err := project.LoadManifest(filepath.Join(projDir, "nem.toml"))
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	got := map[string]string{}
	for _, tool := range m.Tools {
		got[tool.Key.Name] = tool.Version
	}
	if got["toola"] != "v1.1.0" {
		t.Fatalf("toola should update to v1.1.0, manifest: %+v", m.Tools)
	}
	if got["toolb"] != "v2.0.0" {
		t.Fatalf("toolb must stay pinned at v2.0.0, manifest: %+v", m.Tools)
	}
}

func TestUpdateNarratesAfterInstall(t *testing.T) {
	nemHomeDir := t.TempDir()
	catalogRoot := versionedDirCatalog(t, map[string][]string{"tool": {"v1.1.0", "v1.0.0"}})
	projDir := t.TempDir()
	chdir(t, projDir)

	if _, _, err := runNem(t, nemHomeDir, "catalog", "add", "demo", catalogRoot); err != nil {
		t.Fatal(err)
	}
	if _, errb, err := runNem(t, nemHomeDir, "use", "demo:tool@v1.0.0"); err != nil {
		t.Fatalf("use: %v\n%s", err, errb)
	}

	_, errb, err := runNem(t, nemHomeDir, "update")
	if err != nil {
		t.Fatalf("update: %v\n%s", err, errb)
	}
	installed := strings.Index(errb, "Installed tool v1.1.0")
	updated := strings.Index(errb, "Updated tool v1.0.0 → v1.1.0")
	if installed < 0 || updated < 0 {
		t.Fatalf("stderr should carry both install and update narration: %q", errb)
	}
	if updated < installed {
		t.Fatalf("Updated must not print before the install finished: %q", errb)
	}
}

func TestUpdateFailedInstallDoesNotClaimUpdated(t *testing.T) {
	nemHomeDir := t.TempDir()
	projDir := t.TempDir()
	chdir(t, projDir)

	archive := makeTarGz(t, map[string]string{"bin/tool": "tool binary bytes"})
	sum := sha256.Sum256(archive)
	sha := hex.EncodeToString(sum[:])
	var serveGarbage atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveGarbage.Load() {
			w.Write([]byte("not the archive"))
			return
		}
		w.Write(archive)
	}))
	t.Cleanup(srv.Close)

	root := t.TempDir()
	dir := filepath.Join(root, "pkgs", "tool")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	pkgYAML := fmt.Sprintf(`
schema: 2
name: tool
description: a test tool
artifact:
  url: %q
install:
  - extract: {}
versions:
  - version: v1.1.0
    sha256: {darwin/arm64: %q, darwin/amd64: %q, linux/arm64: %q, linux/amd64: %q}
  - version: v1.0.0
    sha256: {darwin/arm64: %q, darwin/amd64: %q, linux/arm64: %q, linux/amd64: %q}
`, srv.URL, sha, sha, sha, sha, sha, sha, sha, sha)
	if err := os.WriteFile(filepath.Join(dir, "pkg.yaml"), []byte(pkgYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, _, err := runNem(t, nemHomeDir, "catalog", "add", "demo", root); err != nil {
		t.Fatal(err)
	}
	if _, errb, err := runNem(t, nemHomeDir, "use", "demo:tool@v1.0.0"); err != nil {
		t.Fatalf("use: %v\n%s", err, errb)
	}

	serveGarbage.Store(true)
	_, errb, err := runNem(t, nemHomeDir, "update")
	if err == nil {
		t.Fatal("want error when the new version fails to install")
	}
	if strings.Contains(errb, "Updated tool") {
		t.Fatalf("a failed install must not be narrated as updated: %q", errb)
	}
}

func TestUpdateUpToDateIsQuietSuccess(t *testing.T) {
	nemHomeDir := t.TempDir()
	catalogRoot := versionedDirCatalog(t, map[string][]string{"tool": {"v1.0.0"}})
	projDir := t.TempDir()
	chdir(t, projDir)

	if _, _, err := runNem(t, nemHomeDir, "catalog", "add", "demo", catalogRoot); err != nil {
		t.Fatal(err)
	}
	if _, errb, err := runNem(t, nemHomeDir, "use", "demo:tool"); err != nil {
		t.Fatalf("use: %v\n%s", err, errb)
	}

	out, errb, err := runNem(t, nemHomeDir, "update")
	if err != nil {
		t.Fatalf("update on an up-to-date project must succeed: %v\n%s", err, errb)
	}
	if out != "" {
		t.Fatalf("stdout must stay empty when nothing changes: %q", out)
	}
	if !strings.Contains(errb, "up to date") {
		t.Fatalf("stderr should say the tools are up to date: %q", errb)
	}
}

func TestUpdateDryRunWritesNothing(t *testing.T) {
	nemHomeDir := t.TempDir()
	catalogRoot := versionedDirCatalog(t, map[string][]string{"tool": {"v1.1.0", "v1.0.0"}})
	projDir := t.TempDir()
	chdir(t, projDir)

	if _, _, err := runNem(t, nemHomeDir, "catalog", "add", "demo", catalogRoot); err != nil {
		t.Fatal(err)
	}
	if _, errb, err := runNem(t, nemHomeDir, "use", "demo:tool@v1.0.0"); err != nil {
		t.Fatalf("use: %v\n%s", err, errb)
	}

	out, errb, err := runNem(t, nemHomeDir, "update", "--dry-run")
	if err != nil {
		t.Fatalf("update --dry-run: %v\n%s", err, errb)
	}
	if !strings.Contains(out, "tool") || !strings.Contains(out, "v1.0.0") || !strings.Contains(out, "v1.1.0") {
		t.Fatalf("stdout should list the would-be change: %q", out)
	}

	m, err := project.LoadManifest(filepath.Join(projDir, "nem.toml"))
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if len(m.Tools) != 1 || m.Tools[0].Version != "v1.0.0" {
		t.Fatalf("dry run must not touch the manifest: %+v", m.Tools)
	}
	lf, err := project.LoadLock(filepath.Join(projDir, "nem.lock"))
	if err != nil {
		t.Fatalf("LoadLock: %v", err)
	}
	if len(lf.Packages) != 1 || lf.Packages[0].Version != "v1.0.0" {
		t.Fatalf("dry run must not touch the lock: %+v", lf.Packages)
	}
	if install.IsInstalled(testNemHome(nemHomeDir), "tool", "v1.1.0") {
		t.Fatal("dry run must not install anything")
	}
}

func TestUpdateUndeclaredPackageErrors(t *testing.T) {
	nemHomeDir := t.TempDir()
	projDir := t.TempDir()
	chdir(t, projDir)
	if err := os.WriteFile(filepath.Join(projDir, "nem.toml"), []byte("[tools]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := runNem(t, nemHomeDir, "update", "ghost")
	if err == nil {
		t.Fatal("want error for undeclared package")
	}
	if !strings.Contains(err.Error(), "ghost") || !strings.Contains(err.Error(), "not declared") {
		t.Fatalf("error should name the package and say it's not declared: %v", err)
	}
}

func TestUpdateRefusesDowngrade(t *testing.T) {
	nemHomeDir := t.TempDir()
	catalogRoot := versionedDirCatalog(t, map[string][]string{"tool": {"v1.0.0"}})
	projDir := t.TempDir()
	chdir(t, projDir)

	if _, _, err := runNem(t, nemHomeDir, "catalog", "add", "demo", catalogRoot); err != nil {
		t.Fatal(err)
	}
	// The declared version is newer than anything the catalog offers, the
	// state a catalog rollback leaves behind.
	if err := os.WriteFile(filepath.Join(projDir, "nem.toml"), []byte("[tools]\ntool = 'v2.0.0'\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := runNem(t, nemHomeDir, "update")
	if err == nil {
		t.Fatal("want error when the catalog's latest is older than the declared version")
	}
	for _, want := range []string{"downgrade", "v2.0.0", "v1.0.0", "nem use tool@v1.0.0"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q should contain %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "catalog's latest") {
		t.Fatalf("the refusal must not claim a cause it cannot know: %v", err)
	}

	m, err := project.LoadManifest(filepath.Join(projDir, "nem.toml"))
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if len(m.Tools) != 1 || m.Tools[0].Version != "v2.0.0" {
		t.Fatalf("a refused downgrade must leave the manifest untouched: %+v", m.Tools)
	}
	if _, err := os.Stat(filepath.Join(projDir, "nem.lock")); !os.IsNotExist(err) {
		t.Fatal("a refused downgrade must not write a lock")
	}
}

func TestUpdateWarnsWhenCatalogStale(t *testing.T) {
	nemHomeDir := t.TempDir()
	h := testNemHome(nemHomeDir)
	projDir := t.TempDir()
	chdir(t, projDir)

	if _, _, err := runNem(t, nemHomeDir, "catalog", "add", "demo", "ghcr.io/x/y:v2"); err != nil {
		t.Fatal(err)
	}
	var calls []string
	orig := syncCatalogStore
	syncCatalogStore = fakeOCICatalogSync(t, &calls, otherPlatform(t))
	defer func() { syncCatalogStore = orig }()

	if _, errb, err := runNem(t, nemHomeDir, "use", "demo:tool"); err != nil {
		t.Fatalf("use: %v\n%s", err, errb)
	}

	store, err := h.CatalogStore("demo")
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-72 * time.Hour)
	if err := os.Chtimes(filepath.Join(store, "index.json"), old, old); err != nil {
		t.Fatalf("age the mirror: %v", err)
	}

	_, errb, err := runNem(t, nemHomeDir, "update")
	if err != nil {
		t.Fatalf("update: %v\n%s", err, errb)
	}
	if strings.Contains(errb, "last synced") {
		t.Fatalf("a mirror under a week old must not warn: %q", errb)
	}

	old = time.Now().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(filepath.Join(store, "index.json"), old, old); err != nil {
		t.Fatalf("age the mirror: %v", err)
	}

	_, errb, err = runNem(t, nemHomeDir, "update")
	if err != nil {
		t.Fatalf("update: %v\n%s", err, errb)
	}
	if !strings.Contains(errb, "Catalog demo last synced 8 days ago") {
		t.Fatalf("stderr should warn about the stale mirror: %q", errb)
	}
	if !strings.Contains(errb, "nem catalog update") {
		t.Fatalf("stderr should hint at refreshing catalogs: %q", errb)
	}
	if strings.Contains(errb, "Catalog official") {
		t.Fatalf("uninvolved catalogs must not be warned about: %q", errb)
	}
}

func TestUpdateBoundsFloatsToDependencyConstraints(t *testing.T) {
	nemHomeDir := t.TempDir()
	projDir := t.TempDir()
	chdir(t, projDir)

	archive := makeTarGz(t, map[string]string{"bin/tool": "tool binary bytes"})
	sum := sha256.Sum256(archive)
	sha := hex.EncodeToString(sum[:])
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(archive)
	}))
	t.Cleanup(srv.Close)

	root := t.TempDir()
	shaBlock := fmt.Sprintf("{darwin/arm64: %q, darwin/amd64: %q, linux/arm64: %q, linux/amd64: %q}", sha, sha, sha, sha)
	writePkg := func(name, deps string) {
		dir := filepath.Join(root, "pkgs", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		yaml := fmt.Sprintf(`
schema: 2
name: %s
description: test package %s
%sartifact:
  url: %q
install:
  - extract: {}
versions:
  - version: v2.0.0
    sha256: %s
  - version: v1.0.0
    sha256: %s
`, name, name, deps, srv.URL, shaBlock, shaBlock)
		if err := os.WriteFile(filepath.Join(dir, "pkg.yaml"), []byte(yaml), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writePkg("lib", "")
	writePkg("app", "deps:\n  - {name: lib, version: v1.0.0}\n")
	writePkg("tool", "")

	if _, _, err := runNem(t, nemHomeDir, "catalog", "add", "demo", root); err != nil {
		t.Fatal(err)
	}
	if _, errb, err := runNem(t, nemHomeDir, "use", "demo:lib@v1.0.0", "demo:app@v1.0.0", "demo:tool@v1.0.0"); err != nil {
		t.Fatalf("use: %v\n%s", err, errb)
	}

	_, errb, err := runNem(t, nemHomeDir, "update")
	if err != nil {
		t.Fatalf("update must bound conflicting floats, not fail: %v\n%s", err, errb)
	}
	if strings.Contains(errb, "Held") {
		t.Fatalf("bounded resolution needs no holds: %q", errb)
	}
	for _, want := range []string{"Updated app v1.0.0 → v2.0.0", "Updated tool v1.0.0 → v2.0.0"} {
		if !strings.Contains(errb, want) {
			t.Fatalf("stderr missing %q: %q", want, errb)
		}
	}

	m, err := project.LoadManifest(filepath.Join(projDir, "nem.toml"))
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	got := map[string]string{}
	for _, tool := range m.Tools {
		got[tool.Key.Name] = tool.Version
	}
	want := map[string]string{"lib": "v1.0.0", "app": "v2.0.0", "tool": "v2.0.0"}
	for name, v := range want {
		if got[name] != v {
			t.Fatalf("manifest %s = %q, want %q (all: %v)", name, got[name], v, got)
		}
	}
}

func TestUpdateUpgradesToHighestWithinCompat(t *testing.T) {
	nemHomeDir := t.TempDir()
	projDir := t.TempDir()
	chdir(t, projDir)

	archive := makeTarGz(t, map[string]string{"bin/tool": "tool binary bytes"})
	sum := sha256.Sum256(archive)
	sha := hex.EncodeToString(sum[:])
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(archive)
	}))
	t.Cleanup(srv.Close)

	root := t.TempDir()
	shaBlock := fmt.Sprintf("{darwin/arm64: %q, darwin/amd64: %q, linux/arm64: %q, linux/amd64: %q}", sha, sha, sha, sha)
	lib := fmt.Sprintf(`
schema: 2
name: lib
description: a library
libs: [lib]
artifact:
  url: %q
install:
  - extract: {}
versions:
  - version: v2.0.0
    sha256: %s
  - version: v1.5.0
    sha256: %s
  - version: v1.0.0
    sha256: %s
`, srv.URL, shaBlock, shaBlock, shaBlock)
	app := fmt.Sprintf(`
schema: 2
name: app
description: an app
deps:
  - {name: lib, kind: link, compat: "1"}
artifact:
  url: %q
install:
  - extract: {}
versions:
  - version: v1.0.0
    sha256: %s
`, srv.URL, shaBlock)
	for name, yaml := range map[string]string{"lib": lib, "app": app} {
		dir := filepath.Join(root, "pkgs", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "pkg.yaml"), []byte(yaml), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if _, _, err := runNem(t, nemHomeDir, "catalog", "add", "demo", root); err != nil {
		t.Fatal(err)
	}
	if _, errb, err := runNem(t, nemHomeDir, "use", "demo:lib@v1.0.0", "demo:app"); err != nil {
		t.Fatalf("use: %v\n%s", err, errb)
	}

	_, errb, err := runNem(t, nemHomeDir, "update")
	if err != nil {
		t.Fatalf("update: %v\n%s", err, errb)
	}
	if !strings.Contains(errb, "Updated lib v1.0.0 → v1.5.0") {
		t.Fatalf("lib should rise to the range's highest: %q", errb)
	}
	if strings.Contains(errb, "constrained by") {
		t.Fatalf("a bounded update is normal operation and must not be narrated: %q", errb)
	}

	m, err := project.LoadManifest(filepath.Join(projDir, "nem.toml"))
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	for _, tool := range m.Tools {
		if tool.Key.Name == "lib" && tool.Version != "v1.5.0" {
			t.Fatalf("manifest lib = %q, want v1.5.0", tool.Version)
		}
	}
}

func TestUpdateGlobalTargetsGlobalManifest(t *testing.T) {
	nemHomeDir := t.TempDir()
	catalogRoot := versionedDirCatalog(t, map[string][]string{"tool": {"v1.1.0", "v1.0.0"}})
	projDir := t.TempDir()
	chdir(t, projDir)

	if _, _, err := runNem(t, nemHomeDir, "catalog", "add", "demo", catalogRoot); err != nil {
		t.Fatal(err)
	}
	if _, errb, err := runNem(t, nemHomeDir, "use", "-g", "demo:tool@v1.0.0"); err != nil {
		t.Fatalf("use -g: %v\n%s", err, errb)
	}

	_, errb, err := runNem(t, nemHomeDir, "update", "-g")
	if err != nil {
		t.Fatalf("update -g: %v\n%s", err, errb)
	}

	m, err := project.LoadManifest(testNemHome(nemHomeDir).GlobalManifest())
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if len(m.Tools) != 1 || m.Tools[0].Version != "v1.1.0" {
		t.Fatalf("global manifest after update -g: %+v", m.Tools)
	}
	if _, err := os.Stat(filepath.Join(projDir, "nem.toml")); !os.IsNotExist(err) {
		t.Fatal("update -g must not create a project manifest")
	}
}

func TestUpdateGlobalWithoutGlobalManifestErrors(t *testing.T) {
	nemHomeDir := t.TempDir()
	projDir := t.TempDir()
	chdir(t, projDir)

	_, _, err := runNem(t, nemHomeDir, "update", "-g")
	if err == nil {
		t.Fatal("want error when no global manifest exists")
	}
	if !errors.Is(err, project.ErrNoManifest) {
		t.Fatalf("want ErrNoManifest, got %v", err)
	}
}

func TestUpdateAcceptsQualifiedDeclaredName(t *testing.T) {
	nemHomeDir := t.TempDir()
	catalogRoot := versionedDirCatalog(t, map[string][]string{"tool": {"v1.1.0", "v1.0.0"}})
	projDir := t.TempDir()
	chdir(t, projDir)

	if _, _, err := runNem(t, nemHomeDir, "catalog", "add", "demo", catalogRoot); err != nil {
		t.Fatal(err)
	}
	if _, errb, err := runNem(t, nemHomeDir, "use", "demo:tool@v1.0.0"); err != nil {
		t.Fatalf("use: %v\n%s", err, errb)
	}

	_, errb, err := runNem(t, nemHomeDir, "update", "demo:tool")
	if err != nil {
		t.Fatalf("update by the declared qualified name must work: %v\n%s", err, errb)
	}
	if !strings.Contains(errb, "Updated tool v1.0.0 → v1.1.0") {
		t.Fatalf("stderr: %q", errb)
	}

	_, _, err = runNem(t, nemHomeDir, "update", "other:tool")
	if err == nil {
		t.Fatal("want error for a mismatched catalog qualifier")
	}
	if !strings.Contains(err.Error(), "demo:tool") {
		t.Fatalf("error should name the declared key: %v", err)
	}
}

func TestUpdateRefusesDowngradeBelowDeclared(t *testing.T) {
	nemHomeDir := t.TempDir()
	catalogRoot := versionedDirCatalog(t, map[string][]string{"tool": {"v1.2.3", "v1.3.0-rc1"}})
	projDir := t.TempDir()
	chdir(t, projDir)

	if _, _, err := runNem(t, nemHomeDir, "catalog", "add", "demo", catalogRoot); err != nil {
		t.Fatal(err)
	}
	if _, errb, err := runNem(t, nemHomeDir, "use", "demo:tool@v1.3.0-rc1"); err != nil {
		t.Fatalf("use: %v\n%s", err, errb)
	}

	_, _, err := runNem(t, nemHomeDir, "update")
	if err == nil {
		t.Fatal("a pick below the declared version must fail the update")
	}
	if !strings.Contains(err.Error(), "refusing to downgrade tool from v1.3.0-rc1 to v1.2.3") {
		t.Fatalf("error should name the refused downgrade: %v", err)
	}

	m, err := project.LoadManifest(filepath.Join(projDir, "nem.toml"))
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if len(m.Tools) != 1 || m.Tools[0].Version != "v1.3.0-rc1" {
		t.Fatalf("a refused update must write nothing: %+v", m.Tools)
	}
}

func TestUpdateSurfacesDeclaredConflict(t *testing.T) {
	nemHomeDir := t.TempDir()
	projDir := t.TempDir()
	chdir(t, projDir)

	archive := makeTarGz(t, map[string]string{"bin/tool": "tool binary bytes"})
	sum := sha256.Sum256(archive)
	sha := hex.EncodeToString(sum[:])
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(archive)
	}))
	t.Cleanup(srv.Close)

	root := t.TempDir()
	shaBlock := fmt.Sprintf("{darwin/arm64: %q, darwin/amd64: %q, linux/arm64: %q, linux/amd64: %q}", sha, sha, sha, sha)
	for name, dep := range map[string]string{
		"lib":  "",
		"app1": "deps:\n  - {name: lib, version: v1.0.0}\n",
		"app2": "deps:\n  - {name: lib, version: v2.0.0}\n",
	} {
		dir := filepath.Join(root, "pkgs", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		yaml := fmt.Sprintf(`
schema: 2
name: %s
description: test package %s
%sartifact:
  url: %q
install:
  - extract: {}
versions:
  - version: v2.0.0
    sha256: %s
  - version: v1.0.0
    sha256: %s
`, name, name, dep, srv.URL, shaBlock, shaBlock)
		if err := os.WriteFile(filepath.Join(dir, "pkg.yaml"), []byte(yaml), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if _, _, err := runNem(t, nemHomeDir, "catalog", "add", "demo", root); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projDir, "nem.toml"), []byte("[tools]\napp1 = 'v1.0.0'\napp2 = 'v1.0.0'\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, errb, err := runNem(t, nemHomeDir, "update")
	if err == nil {
		t.Fatal("a conflicted declared environment must fail the update")
	}
	if !strings.Contains(err.Error(), "conflicting requirements v1.0.0, v2.0.0") {
		t.Fatalf("error should list the clashing requirements: %v", err)
	}
	if strings.Contains(errb, "Held") {
		t.Fatalf("no hold narration on the error path: %q", errb)
	}
}

func TestUpdateStaleWarningSurvivesRefusal(t *testing.T) {
	nemHomeDir := t.TempDir()
	h := testNemHome(nemHomeDir)
	projDir := t.TempDir()
	chdir(t, projDir)

	if _, _, err := runNem(t, nemHomeDir, "catalog", "add", "demo", "ghcr.io/x/y:v2"); err != nil {
		t.Fatal(err)
	}
	var calls []string
	orig := syncCatalogStore
	syncCatalogStore = fakeOCICatalogSync(t, &calls, otherPlatform(t))
	defer func() { syncCatalogStore = orig }()

	if _, errb, err := runNem(t, nemHomeDir, "use", "demo:tool"); err != nil {
		t.Fatalf("use: %v\n%s", err, errb)
	}
	if err := os.WriteFile(filepath.Join(projDir, "nem.toml"), []byte("[tools]\n\"demo:tool\" = 'v2.0.0'\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	store, err := h.CatalogStore("demo")
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(filepath.Join(store, "index.json"), old, old); err != nil {
		t.Fatalf("age the mirror: %v", err)
	}

	_, errb, err := runNem(t, nemHomeDir, "update")
	if err == nil {
		t.Fatal("want the downgrade refusal")
	}
	if !strings.Contains(err.Error(), "refusing to downgrade") {
		t.Fatalf("error: %v", err)
	}
	if !strings.Contains(err.Error(), "nem use demo:tool@v1.0.0") {
		t.Fatalf("the hint must keep the catalog qualifier: %v", err)
	}
	if !strings.Contains(errb, "Catalog demo last synced 8 days ago") {
		t.Fatalf("the refusal must not hide the stale-mirror warning: %q", errb)
	}
}

func TestSyncAgePhrase(t *testing.T) {
	cases := []struct {
		age  time.Duration
		want string
	}{
		{25 * time.Hour, "1 day"},
		{47 * time.Hour, "1 day"},
		{72 * time.Hour, "3 days"},
	}
	for _, c := range cases {
		if got := syncAgePhrase(c.age); got != c.want {
			t.Errorf("syncAgePhrase(%v) = %q, want %q", c.age, got, c.want)
		}
	}
}

func TestUpdateVersionArgRefused(t *testing.T) {
	nemHomeDir := t.TempDir()
	projDir := t.TempDir()
	chdir(t, projDir)
	if err := os.WriteFile(filepath.Join(projDir, "nem.toml"), []byte("[tools]\ntool = 'v1.0.0'\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := runNem(t, nemHomeDir, "update", "tool@v1.1.0")
	if err == nil {
		t.Fatal("want error for versioned argument")
	}
	if !strings.Contains(err.Error(), "nem use") {
		t.Fatalf("error should point at `nem use` for version pinning: %v", err)
	}
}
