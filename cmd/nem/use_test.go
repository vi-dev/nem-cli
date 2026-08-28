package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/oci"
	"oras.land/oras-go/v2/registry/remote/errcode"

	"github.com/vi-dev/nem-cli/internal/catalog"
	"github.com/vi-dev/nem-cli/internal/config"
	"github.com/vi-dev/nem-cli/internal/home"
	"github.com/vi-dev/nem-cli/internal/install"
	"github.com/vi-dev/nem-cli/internal/ocix"
	"github.com/vi-dev/nem-cli/internal/project"
	"github.com/vi-dev/nem-cli/internal/resolve"
	"github.com/vi-dev/nem-cli/internal/spec"
)

// stubSyncEmptyCatalog writes a schema-valid, empty catalog straight into
// storePath without touching a network. It is the default syncCatalogStore
// for this whole test binary (see TestMain): use's cold auto-sync runs
// ahead of every resolution, and most tests here never mean to reach a
// real registry just because a default catalog happens to be unsynced.
func stubSyncEmptyCatalog(ctx context.Context, ref, storePath string, progress ocix.ProgressFunc) error {
	dst, err := oci.New(storePath)
	if err != nil {
		return err
	}
	idx := ocispec.Index{
		Versioned:   specs.Versioned{SchemaVersion: 2},
		MediaType:   ocispec.MediaTypeImageIndex,
		Annotations: map[string]string{ocix.AnnotationSchemaVersion: ocix.SchemaVersion},
	}
	data, err := json.Marshal(idx)
	if err != nil {
		return err
	}
	desc := content.NewDescriptorFromBytes(ocispec.MediaTypeImageIndex, data)
	if err := dst.Push(ctx, desc, bytes.NewReader(data)); err != nil {
		return err
	}
	return dst.Tag(ctx, desc, ocix.LocalTag)
}

func TestMain(m *testing.M) {
	syncCatalogStore = stubSyncEmptyCatalog
	os.Exit(m.Run())
}

// chdir switches the process cwd to dir for the duration of t.
func chdir(t *testing.T, dir string) {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(cwd) })
}

// makeTarGz builds a gzip-compressed tar archive containing files, keyed by
// entry path.
func makeTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for name, content := range files {
		hdr := &tar.Header{Name: name, Mode: 0o755, Size: int64(len(content))}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write tar header: %v", err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatalf("write tar body: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buf.Bytes()
}

// downloadableDirCatalog writes a dir catalog rooted at a temp dir holding
// one package, "tool", whose artifact is a real tar.gz served by an
// httptest server, with sha256 computed from the archive bytes. extraYAML,
// when non-empty, is appended at the manifest's top level — e.g. a "test:"
// block — after the versions section.
func downloadableDirCatalog(t *testing.T, extraYAML string) string {
	t.Helper()
	archive := makeTarGz(t, map[string]string{"bin/tool": "tool binary bytes"})
	sum := sha256.Sum256(archive)
	sha := hex.EncodeToString(sum[:])

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(archive)
	}))
	t.Cleanup(srv.Close)

	root := t.TempDir()
	dir := filepath.Join(root, "pkgs", "tool")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	pkgYAML := `
schema: 2
name: tool
description: a test tool
artifact:
  url: "` + srv.URL + `"
install:
  - extract: {}
versions:
  - version: v1.0.0
    sha256:
      darwin/arm64: "` + sha + `"
      darwin/amd64: "` + sha + `"
      linux/arm64: "` + sha + `"
      linux/amd64: "` + sha + `"
` + extraYAML
	if err := os.WriteFile(filepath.Join(dir, "pkg.yaml"), []byte(pkgYAML), 0o644); err != nil {
		t.Fatalf("write pkg.yaml: %v", err)
	}
	return root
}

func testNemHome(nemHomeDir string) home.Home {
	return home.Resolve(func(k string) string {
		if k == "NEM_HOME" {
			return nemHomeDir
		}
		return ""
	})
}

func TestUseCreatesManifestLockAndInstalls(t *testing.T) {
	nemHomeDir := t.TempDir()
	catalogRoot := downloadableDirCatalog(t, "")
	projDir := t.TempDir()
	chdir(t, projDir)

	if _, errb, err := runNem(t, nemHomeDir, "catalog", "add", "demo", catalogRoot); err != nil {
		t.Fatalf("catalog add: %v\n%s", err, errb)
	}

	out, errb, err := runNem(t, nemHomeDir, "use", "demo:tool")
	if err != nil {
		t.Fatalf("use: %v\nstdout: %s\nstderr: %s", err, out, errb)
	}
	if !strings.Contains(errb, "Installed tool v1.0.0") {
		t.Fatalf("narration missing install success line: %q", errb)
	}

	manifestPath := filepath.Join(projDir, "nem.toml")
	m, err := project.LoadManifest(manifestPath)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	// "use demo:tool" (no @version) resolves the latest and writes it
	// exactly: the manifest pins the resolved v1.0.0, matching the lock.
	if len(m.Tools) != 1 || m.Tools[0].Key.String() != "demo:tool" || m.Tools[0].Version != "v1.0.0" {
		t.Fatalf("manifest tools: %+v", m.Tools)
	}

	lockPath := filepath.Join(projDir, "nem.lock")
	lf, err := project.LoadLock(lockPath)
	if err != nil {
		t.Fatalf("LoadLock: %v", err)
	}
	if len(lf.Packages) != 1 {
		t.Fatalf("lock packages: %+v", lf.Packages)
	}
	e := lf.Packages[0]
	if e.Name != "tool" || e.Version != "v1.0.0" || e.Catalog != "demo" || !e.Direct || e.Digest != "" {
		t.Fatalf("lock entry: %+v", e)
	}
	if len(e.Platforms) != 4 {
		t.Fatalf("lock entry platforms: %+v", e.Platforms)
	}

	h := testNemHome(nemHomeDir)
	if !install.IsInstalled(h, "tool", "v1.0.0") {
		t.Fatal("tool v1.0.0 not installed")
	}
	installDir, err := h.PackageDir("tool", "v1.0.0")
	if err != nil {
		t.Fatalf("PackageDir: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(installDir, "bin", "tool"))
	if err != nil || string(got) != "tool binary bytes" {
		t.Fatalf("installed bin/tool: %q, %v", got, err)
	}
}

func TestUseUnknownVersionFails(t *testing.T) {
	nemHomeDir := t.TempDir()
	catalogRoot := downloadableDirCatalog(t, "")
	projDir := t.TempDir()
	chdir(t, projDir)

	if _, _, err := runNem(t, nemHomeDir, "catalog", "add", "demo", catalogRoot); err != nil {
		t.Fatal(err)
	}

	_, errb, err := runNem(t, nemHomeDir, "use", "demo:tool@v9.9.9")
	if err == nil {
		t.Fatal("want error for unknown version")
	}
	if !strings.Contains(err.Error(), "v9.9.9") {
		t.Fatalf("error should mention the unknown version: %v", err)
	}
	if !strings.Contains(errb, "v9.9.9") {
		t.Fatalf("stderr should mention the unknown version: %q", errb)
	}
	if _, err := os.Stat(filepath.Join(projDir, "nem.toml")); !os.IsNotExist(err) {
		t.Fatal("nem.toml must not be written on a failed use")
	}
}

func TestUnuseRemovesFromManifestAndLock(t *testing.T) {
	nemHomeDir := t.TempDir()
	catalogRoot := downloadableDirCatalog(t, "")
	projDir := t.TempDir()
	chdir(t, projDir)

	if _, _, err := runNem(t, nemHomeDir, "catalog", "add", "demo", catalogRoot); err != nil {
		t.Fatal(err)
	}
	if _, errb, err := runNem(t, nemHomeDir, "use", "demo:tool"); err != nil {
		t.Fatalf("use: %v\n%s", err, errb)
	}

	if _, errb, err := runNem(t, nemHomeDir, "unuse", "tool"); err != nil {
		t.Fatalf("unuse: %v\n%s", err, errb)
	}

	m, err := project.LoadManifest(filepath.Join(projDir, "nem.toml"))
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if len(m.Tools) != 0 {
		t.Fatalf("manifest tools after unuse: %+v", m.Tools)
	}

	lf, err := project.LoadLock(filepath.Join(projDir, "nem.lock"))
	if err != nil {
		t.Fatalf("LoadLock: %v", err)
	}
	if len(lf.Packages) != 0 {
		t.Fatalf("lock packages after unuse: %+v", lf.Packages)
	}

	// unuse never deletes an installed package.
	h := testNemHome(nemHomeDir)
	if !install.IsInstalled(h, "tool", "v1.0.0") {
		t.Fatal("unuse must not delete the installed package")
	}
}

func TestUnuseUnknownErrors(t *testing.T) {
	nemHomeDir := t.TempDir()
	projDir := t.TempDir()
	chdir(t, projDir)
	if err := os.WriteFile(filepath.Join(projDir, "nem.toml"), []byte("[tools]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := runNem(t, nemHomeDir, "unuse", "ghost")
	if err == nil {
		t.Fatal("want error for unknown package")
	}
	if !strings.Contains(err.Error(), "ghost") || !strings.Contains(err.Error(), "not declared") {
		t.Fatalf("error should name the package and say it's not declared: %v", err)
	}
}

// otherPlatform returns a supported platform other than the one the test
// runs on, so a resolved tool never reaches install (which would otherwise
// need a real registry or download target).
func otherPlatform(t *testing.T) spec.Platform {
	t.Helper()
	for _, p := range spec.Supported {
		if p != spec.Current() {
			return p
		}
	}
	t.Fatal("no alternate supported platform available")
	return spec.Platform{}
}

// fakeOCICatalogSync returns a syncCatalogStore replacement that records
// each ref+storePath it's called with and syncs a fake catalog holding a
// single "tool" package (restricted to plat) into storePath, bypassing any
// real registry.
func fakeOCICatalogSync(t *testing.T, calls *[]string, plat spec.Platform) func(ctx context.Context, ref, storePath string, progress ocix.ProgressFunc) error {
	t.Helper()
	yaml := fmt.Sprintf(`
schema: 2
name: tool
platforms: [%s]
artifact:
  oci: ":{{.Version}}"
install:
  - extract: {}
versions:
  - v1.0.0
`, plat)
	return func(ctx context.Context, ref, storePath string, progress ocix.ProgressFunc) error {
		*calls = append(*calls, ref+"|"+storePath)
		src, err := oci.New(t.TempDir())
		if err != nil {
			return err
		}
		ocix.PushFakeCatalogForTest(t, src, []ocix.FakeEntry{{
			Name: "tool", Description: "a test tool", Latest: "v1.0.0",
			YAML: []byte(yaml),
		}}, "2")
		_, err = ocix.SyncLocalCatalog(ctx, src, "v2", storePath, progress)
		return err
	}
}

func TestUseColdAutoSyncsUnsyncedOCICatalogBeforeResolve(t *testing.T) {
	nemHomeDir := t.TempDir()
	h := testNemHome(nemHomeDir)
	projDir := t.TempDir()
	chdir(t, projDir)

	if _, errb, err := runNem(t, nemHomeDir, "catalog", "add", "demo", "ghcr.io/x/y:v2"); err != nil {
		t.Fatalf("catalog add: %v\n%s", err, errb)
	}
	store, err := h.CatalogStore("demo")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(store); !os.IsNotExist(err) {
		t.Fatal("setup broken: catalog store must not exist before the first use")
	}

	var calls []string
	orig := syncCatalogStore
	syncCatalogStore = fakeOCICatalogSync(t, &calls, otherPlatform(t))
	defer func() { syncCatalogStore = orig }()

	_, errb, err := runNem(t, nemHomeDir, "use", "demo:tool")
	if err != nil {
		t.Fatalf("use: %v\n%s", err, errb)
	}
	// The default "official" catalog is also unsynced in a fresh nemHome,
	// so it's synced too; what this test proves is that "demo" — the
	// fixture whose store dir didn't exist yet — is among the catalogs
	// use synced before resolving.
	wantDemoCall := "ghcr.io/x/y:v2|" + store
	if !slices.Contains(calls, wantDemoCall) {
		t.Fatalf("syncCatalogStore not called for the unsynced demo catalog: %v", calls)
	}

	// cold-only: a store that's already synced (even from a previous use)
	// must never be re-synced.
	calls = nil
	if _, errb, err := runNem(t, nemHomeDir, "use", "demo:tool"); err != nil {
		t.Fatalf("second use: %v\n%s", err, errb)
	}
	if len(calls) != 0 {
		t.Fatalf("already-synced stores must not be re-synced, got calls %v", calls)
	}
}

// TestUseColdAutoSyncFailureDoesNotAbortWhenToolResolvesElsewhere covers the
// fresh-machine scenario: a cold oci catalog that fails to sync (e.g. the
// default "official" catalog before it's published) must not stop a `use`
// pinned to a package another catalog already provides.
func TestUseColdAutoSyncFailureDoesNotAbortWhenToolResolvesElsewhere(t *testing.T) {
	nemHomeDir := t.TempDir()
	catalogRoot := downloadableDirCatalog(t, "")
	projDir := t.TempDir()
	chdir(t, projDir)

	if _, _, err := runNem(t, nemHomeDir, "catalog", "add", "cold", "ghcr.io/x/y:v2"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runNem(t, nemHomeDir, "catalog", "add", "dir", catalogRoot); err != nil {
		t.Fatal(err)
	}

	orig := syncCatalogStore
	syncCatalogStore = func(ctx context.Context, ref, storePath string, progress ocix.ProgressFunc) error {
		return errors.New("network unreachable")
	}
	defer func() { syncCatalogStore = orig }()

	out, errb, err := runNem(t, nemHomeDir, "use", "dir:tool")
	if err != nil {
		t.Fatalf("use: %v\nstdout: %s\nstderr: %s", err, out, errb)
	}
	if !strings.Contains(errb, "Could not sync catalog cold") {
		t.Fatalf("stderr should warn about the cold catalog's sync failure: %q", errb)
	}
	if !strings.Contains(errb, "Installed tool v1.0.0") {
		t.Fatalf("use should still resolve and install from the dir catalog: %q", errb)
	}
	if _, err := os.Stat(filepath.Join(projDir, "nem.toml")); err != nil {
		t.Fatalf("nem.toml should be written when use succeeds: %v", err)
	}
}

// TestUseColdAutoSyncFailureSurfacesAtResolveWhenToolOnlyThere covers the
// other half: when the package really does live only in the catalog that
// failed to sync, use must still fail, but with the not-synced error and
// hint from resolution, not the raw sync error.
func TestUseColdAutoSyncFailureSurfacesAtResolveWhenToolOnlyThere(t *testing.T) {
	nemHomeDir := t.TempDir()
	projDir := t.TempDir()
	chdir(t, projDir)

	if _, _, err := runNem(t, nemHomeDir, "catalog", "add", "cold", "ghcr.io/x/y:v2"); err != nil {
		t.Fatal(err)
	}

	wantSyncErr := errors.New("network unreachable")
	orig := syncCatalogStore
	syncCatalogStore = func(ctx context.Context, ref, storePath string, progress ocix.ProgressFunc) error { return wantSyncErr }
	defer func() { syncCatalogStore = orig }()

	_, errb, err := runNem(t, nemHomeDir, "use", "cold:tool")
	if err == nil {
		t.Fatal("want error when the only catalog holding the tool never synced")
	}
	if !errors.Is(err, ocix.ErrNotSynced) {
		t.Fatalf("want ErrNotSynced from resolution, got %v", err)
	}
	if errors.Is(err, wantSyncErr) {
		t.Fatalf("use's error should come from resolution, not the sync failure: %v", err)
	}
	if !strings.Contains(errb, "Could not sync catalog cold") {
		t.Fatalf("stderr should still warn about the failed sync: %q", errb)
	}
	if !strings.Contains(errb, "nem catalog update") {
		t.Fatalf("stderr should carry the not-synced hint: %q", errb)
	}
	if _, err := os.Stat(filepath.Join(projDir, "nem.toml")); !os.IsNotExist(err) {
		t.Fatal("nem.toml must not be written when resolution fails")
	}
}

// TestUseColdAutoSyncFailureSkippedWhenToolResolvesInLaterCatalog covers the
// unqualified first-match walk: a cold catalog that fails to sync and sits
// ahead of the catalog holding the tool must be skipped, not abort
// resolution.
func TestUseColdAutoSyncFailureSkippedWhenToolResolvesInLaterCatalog(t *testing.T) {
	nemHomeDir := t.TempDir()
	catalogRoot := downloadableDirCatalog(t, "")
	projDir := t.TempDir()
	chdir(t, projDir)

	if _, _, err := runNem(t, nemHomeDir, "catalog", "add", "cold", "ghcr.io/x/y:v2"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runNem(t, nemHomeDir, "catalog", "add", "dir", catalogRoot); err != nil {
		t.Fatal(err)
	}

	orig := syncCatalogStore
	syncCatalogStore = func(ctx context.Context, ref, storePath string, progress ocix.ProgressFunc) error {
		return errors.New("network unreachable")
	}
	defer func() { syncCatalogStore = orig }()

	out, errb, err := runNem(t, nemHomeDir, "use", "tool")
	if err != nil {
		t.Fatalf("use: %v\nstdout: %s\nstderr: %s", err, out, errb)
	}
	if !strings.Contains(errb, "Could not sync catalog cold") {
		t.Fatalf("stderr should warn about the cold catalog's sync failure: %q", errb)
	}
	if !strings.Contains(errb, "Installed tool v1.0.0") {
		t.Fatalf("use should still resolve and install from the dir catalog: %q", errb)
	}
	if _, err := os.Stat(filepath.Join(projDir, "nem.toml")); err != nil {
		t.Fatalf("nem.toml should be written when use succeeds: %v", err)
	}
}

// TestUseUnqualifiedSurfacesNotSyncedWhenToolOnlyInUnsyncedCatalog covers the
// unqualified first-match walk's exhaustion case: when every catalog that
// could hold the tool failed to sync, use must fail with the not-synced
// error rather than a plain not-found.
func TestUseUnqualifiedSurfacesNotSyncedWhenToolOnlyInUnsyncedCatalog(t *testing.T) {
	nemHomeDir := t.TempDir()
	projDir := t.TempDir()
	chdir(t, projDir)

	if _, _, err := runNem(t, nemHomeDir, "catalog", "add", "cold", "ghcr.io/x/y:v2"); err != nil {
		t.Fatal(err)
	}

	wantSyncErr := errors.New("network unreachable")
	orig := syncCatalogStore
	syncCatalogStore = func(ctx context.Context, ref, storePath string, progress ocix.ProgressFunc) error { return wantSyncErr }
	defer func() { syncCatalogStore = orig }()

	_, errb, err := runNem(t, nemHomeDir, "use", "tool")
	if err == nil {
		t.Fatal("want error when the tool resolves in no synced catalog")
	}
	if !errors.Is(err, ocix.ErrNotSynced) {
		t.Fatalf("want ErrNotSynced from resolution, got %v", err)
	}
	if !strings.Contains(errb, "nem catalog update") {
		t.Fatalf("stderr should carry the not-synced hint: %q", errb)
	}
	if _, err := os.Stat(filepath.Join(projDir, "nem.toml")); !os.IsNotExist(err) {
		t.Fatal("nem.toml must not be written when resolution fails")
	}
}

func TestUseIgnoresDisabledCatalog(t *testing.T) {
	nemHomeDir := t.TempDir()
	catalogRoot := downloadableDirCatalog(t, "")
	projDir := t.TempDir()
	chdir(t, projDir)

	h := testNemHome(nemHomeDir)
	if err := config.SaveConfig(h, &config.Config{Catalogs: []config.CatalogEntry{
		{Name: "off", Type: "oci", Ref: "ghcr.io/x/off:v2", Disabled: true},
		{Name: "dir", Type: "dir", Path: catalogRoot},
	}}); err != nil {
		t.Fatal(err)
	}
	var coldSynced []string
	orig := syncCatalogStore
	syncCatalogStore = func(ctx context.Context, ref, storePath string, progress ocix.ProgressFunc) error {
		coldSynced = append(coldSynced, ref)
		return nil
	}
	defer func() { syncCatalogStore = orig }()

	if _, errb, err := runNem(t, nemHomeDir, "use", "tool"); err != nil {
		t.Fatalf("use: %v\nstderr: %s", err, errb)
	}
	if len(coldSynced) != 0 {
		t.Fatalf("disabled catalog must not be cold-synced, got %v", coldSynced)
	}
	if _, err := os.Stat(filepath.Join(projDir, "nem.toml")); err != nil {
		t.Fatalf("nem.toml should be written: %v", err)
	}
}

func TestHintForTable(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"not synced", ocix.ErrNotSynced, "nem catalog update"},
		{"catalog not found", &catalog.CatalogNotFoundError{Name: "dev"}, "nem catalog add"},
		{"package not found", &catalog.PackageNotFoundError{Name: "go", Catalogs: []string{"official"}}, "nem catalog update"},
		{"unsupported platform", &resolve.UnsupportedPlatformError{Name: "go", Version: "v1"}, "none of nem's platforms"},
		{"pin conflict", &resolve.PinConflictError{Name: "go", Pinned: "v1", Required: "v2"}, "nem use go@v2"},
		{"registry 401 with host", &errcode.ErrorResponse{Method: "GET", URL: &url.URL{Host: "ghcr.io"}, StatusCode: http.StatusUnauthorized}, "docker login ghcr.io"},
		{"registry 401 without host", &errcode.ErrorResponse{Method: "GET", URL: nil, StatusCode: http.StatusUnauthorized}, "docker login"},
		{"registry 404 is not a login hint", &errcode.ErrorResponse{Method: "GET", URL: &url.URL{Host: "ghcr.io"}, StatusCode: http.StatusNotFound}, ""},
		{"network error", &url.Error{Op: "Get", URL: "https://example.com", Err: errors.New("connection refused")}, "network"},
		{"unrecognized", errors.New("boom"), ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := hintFor(c.err)
			if c.want == "" {
				if got != "" {
					t.Fatalf("hintFor(%v) = %q, want empty", c.err, got)
				}
				return
			}
			if !strings.Contains(got, c.want) {
				t.Fatalf("hintFor(%v) = %q, want to contain %q", c.err, got, c.want)
			}
		})
	}
}
