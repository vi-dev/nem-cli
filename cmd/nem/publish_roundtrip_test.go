package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/oci"

	"github.com/vi-dev/nem-cli/internal/fetch"
	"github.com/vi-dev/nem-cli/internal/install"
	"github.com/vi-dev/nem-cli/internal/ocix"
	"github.com/vi-dev/nem-cli/internal/project"
	"github.com/vi-dev/nem-cli/internal/publish"
	"github.com/vi-dev/nem-cli/internal/spec"
)

// TestPublishToUseRoundTrip proves the full lint -> publish -> catalog
// add(oci) -> use cycle end to end with no network. A local-dir catalog is
// linted, published into a shared oci.Store standing in for a registry,
// added as an oci catalog by an independent consumer, and used: the
// consumer's registry archive probe misses (publish pushes manifests, not
// archives) and falls back to the same httptest server the dir catalog
// itself points at, verifying the pinned sha256 and installing the result.
func TestPublishToUseRoundTrip(t *testing.T) {
	ctx := context.Background()

	shared, err := oci.New(t.TempDir())
	if err != nil {
		t.Fatalf("oci.New: %v", err)
	}

	catalogRoot := downloadableDirCatalog(t)

	restorePublish := publish.SetTargetOpenerForTest(func(context.Context, string) (oras.Target, error) {
		return shared, nil
	})
	defer restorePublish()

	pubNemHome := t.TempDir()
	if _, errb, err := runNem(t, pubNemHome, "catalog", "lint", catalogRoot); err != nil {
		t.Fatalf("lint: %v\n%s", err, errb)
	}

	if _, errb, err := runNem(t, pubNemHome, "catalog", "publish", "example.com/cat:v2", catalogRoot, "--tag", "v2"); err != nil {
		t.Fatalf("publish: %v\n%s", err, errb)
	}
	if _, err := shared.Resolve(ctx, "v2"); err != nil {
		t.Fatalf("resolve published v2: %v", err)
	}

	consumerNemHome := t.TempDir()
	projDir := t.TempDir()
	chdir(t, projDir)

	if _, errb, err := runNem(t, consumerNemHome, "catalog", "add", "team", "example.com/cat:v2"); err != nil {
		t.Fatalf("catalog add: %v\n%s", err, errb)
	}

	origSync := syncCatalogStore
	syncCatalogStore = func(ctx context.Context, ref, storePath string) error {
		_, err := ocix.SyncFrom(ctx, shared, "v2", storePath)
		return err
	}
	defer func() { syncCatalogStore = origSync }()

	// publish pushes only package manifests, never archives, so a real
	// consumer's registry-archive probe always misses; stubbing pullArchive
	// to ErrArchiveNotFound reproduces that miss without a real registry and
	// exercises Acquire's upstream-url fallback.
	restoreFetch := fetch.SetPullArchiveForTest(func(context.Context, string, string, string, spec.Platform, string) (string, error) {
		return "", ocix.ErrArchiveNotFound
	})
	defer restoreFetch()

	out, errb, err := runNem(t, consumerNemHome, "use", "team:tool@v1.0.0")
	if err != nil {
		t.Fatalf("use: %v\nstdout: %s\nstderr: %s", err, out, errb)
	}
	if !strings.Contains(errb, "Installed tool v1.0.0") {
		t.Fatalf("narration missing install success line: %q", errb)
	}

	m, err := project.LoadManifest(filepath.Join(projDir, "nem.toml"))
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if len(m.Tools) != 1 || m.Tools[0].Key.String() != "team:tool" || m.Tools[0].Version != "v1.0.0" {
		t.Fatalf("manifest tools: %+v", m.Tools)
	}

	lf, err := project.LoadLock(filepath.Join(projDir, "nem.lock"))
	if err != nil {
		t.Fatalf("LoadLock: %v", err)
	}
	if len(lf.Packages) != 1 {
		t.Fatalf("lock packages: %+v", lf.Packages)
	}
	e := lf.Packages[0]
	if e.Name != "tool" || e.Version != "v1.0.0" || e.Catalog != "team" || !e.Direct {
		t.Fatalf("lock entry: %+v", e)
	}

	h := testNemHome(consumerNemHome)
	if !install.IsInstalled(h, "tool", "v1.0.0") {
		t.Fatal("tool v1.0.0 not installed")
	}
	installDir, err := h.PackageDir("tool", "v1.0.0")
	if err != nil {
		t.Fatalf("PackageDir: %v", err)
	}
	binPath := filepath.Join(installDir, "bin", "tool")
	got, err := os.ReadFile(binPath)
	if err != nil || string(got) != "tool binary bytes" {
		t.Fatalf("installed bin/tool: %q, %v", got, err)
	}

	whichOut, whichErr, err := runNem(t, consumerNemHome, "which", "tool")
	if err != nil {
		t.Fatalf("which: %v\n%s", err, whichErr)
	}
	resolved := strings.TrimSpace(whichOut)
	if !strings.HasPrefix(resolved, consumerNemHome) {
		t.Fatalf("which tool = %q, want a path under NEM_HOME %q", resolved, consumerNemHome)
	}
	if resolved != binPath {
		t.Fatalf("which tool = %q, want installed bin path %q", resolved, binPath)
	}
}
