package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/memory"

	"github.com/vi-dev/nem-cli/internal/fetch"
	"github.com/vi-dev/nem-cli/internal/fill"
	"github.com/vi-dev/nem-cli/internal/install"
	"github.com/vi-dev/nem-cli/internal/mirror"
	"github.com/vi-dev/nem-cli/internal/ocix"
	"github.com/vi-dev/nem-cli/internal/publish"
	"github.com/vi-dev/nem-cli/internal/spec"
)

// airArchiveFixtures holds per-package archive stores, created lazily so a
// package absent from the map still resolves to an empty
// (archive-not-found) store rather than a nil pointer — the same shape
// internal/fill's and internal/mirror's own opener fixtures use.
type airArchiveFixtures struct {
	mu     sync.Mutex
	stores map[string]oras.Target
}

func newAirArchiveFixtures() *airArchiveFixtures {
	return &airArchiveFixtures{stores: map[string]oras.Target{}}
}

func (f *airArchiveFixtures) open(name string) oras.Target {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok := f.stores[name]; ok {
		return s
	}
	s := memory.New()
	f.stores[name] = s
	return s
}

// writeCatalogPkg writes a pkg.yaml for name under dir/pkgs/<name>, the
// layout catalog publish reads.
func writeCatalogPkg(t *testing.T, dir, name, yaml string) {
	t.Helper()
	pkgDir := filepath.Join(dir, "pkgs", name)
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", pkgDir, err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "pkg.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatalf("write pkg.yaml for %s: %v", name, err)
	}
}

// TestAirGappedCatalogRoundTrip drives publish, fill, mirror, and install
// through the real CLI: fill a catalog from an httptest upstream, close
// it, mirror into a second store, then have an independent consumer add
// and install with no route back to the closed upstream. A second
// package fill never touched proves the negative case: absent from the
// mirror, it fails fast with a network error, never a hang.
func TestAirGappedCatalogRoundTrip(t *testing.T) {
	toolArchive := makeTarGz(t, map[string]string{"bin/tool": "tool binary bytes"})
	toolSHA := sha256Hex(t, string(toolArchive))
	curlSHA := sha256Hex(t, "curl payload, never fetched")

	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/tool/v1.0.0" {
			w.Write(toolArchive)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))

	catalogDir := t.TempDir()
	writeCatalogPkg(t, catalogDir, "tool", urlFillPkgYAML("tool", "v1.0.0", up.URL+"/tool/{{.Version}}", toolSHA))
	writeCatalogPkg(t, catalogDir, "curl", urlFillPkgYAML("curl", "1.0.0", up.URL+"/curl/{{.Version}}", curlSHA))

	srcCatalog := memory.New()
	srcArchives := newAirArchiveFixtures()

	connectedNemHome := t.TempDir()

	restorePublishTarget := publish.SetTargetOpener(func(context.Context, string) (oras.Target, error) {
		return srcCatalog, nil
	})
	_, errb, err := runNem(t, connectedNemHome, "catalog", "publish", "example.com/cat", catalogDir, "--tag", "v2")
	restorePublishTarget()
	if err != nil {
		t.Fatalf("publish: %v\n%s", err, errb)
	}

	t.Cleanup(fill.SetCatalogOpener(func(string) (oras.ReadOnlyTarget, string, error) {
		return srcCatalog, "v2", nil
	}))
	t.Cleanup(fill.SetArchivesOpener(func(_, name string) (oras.Target, error) {
		return srcArchives.open(name), nil
	}))
	t.Cleanup(fill.SetHTTPClient(up.Client()))

	// Fill only "tool": "curl" stays unfilled, so it is absent from the
	// mirror later — the fixture for the negative case below.
	_, errb, err = runNem(t, connectedNemHome, "catalog", "fill", "example.com/cat:v2", "--pkg", "tool")
	if err != nil {
		t.Fatalf("fill: %v\n%s", err, errb)
	}
	wantFillSummary := fmt.Sprintf("Filled 1 packages, %d fill(s), 0 heal(s), 0 present, 0 package(s) not fillable", len(spec.Supported))
	if !strings.Contains(errb, wantFillSummary) {
		t.Fatalf("fill summary = %q, want to contain %q", errb, wantFillSummary)
	}

	// The air gap begins: nothing from here on may reach the upstream again.
	up.Close()

	dstCatalog := memory.New()
	dstArchives := newAirArchiveFixtures()

	t.Cleanup(mirror.SetSrcCatalogOpener(func(string) (oras.ReadOnlyTarget, string, error) {
		return srcCatalog, "v2", nil
	}))
	t.Cleanup(mirror.SetDstCatalogOpener(func(string) (oras.Target, string, error) {
		return dstCatalog, "v2", nil
	}))
	t.Cleanup(mirror.SetSrcArchivesOpener(func(_, name string) (oras.ReadOnlyTarget, error) {
		return srcArchives.open(name), nil
	}))
	t.Cleanup(mirror.SetDstArchivesOpener(func(_, name string) (oras.Target, error) {
		return dstArchives.open(name), nil
	}))

	_, errb, err = runNem(t, connectedNemHome, "catalog", "mirror", "example.com/cat:v2", "internal.example.com/cat:v2")
	if err != nil {
		t.Fatalf("mirror: %v\n%s", err, errb)
	}
	if !strings.Contains(errb, "Mirrored 2 packages, 1 tag(s)") {
		t.Fatalf("mirror summary:\n%s", errb)
	}

	// Byte-identity: the mirrored catalog and the tool archive match the
	// source exactly.
	ctx := context.Background()
	srcCatDesc, err := srcCatalog.Resolve(ctx, "v2")
	if err != nil {
		t.Fatalf("resolve src catalog: %v", err)
	}
	dstCatDesc, err := dstCatalog.Resolve(ctx, "v2")
	if err != nil {
		t.Fatalf("resolve dst catalog: %v", err)
	}
	if srcCatDesc.Digest != dstCatDesc.Digest {
		t.Fatalf("dst catalog digest %s != src digest %s", dstCatDesc.Digest, srcCatDesc.Digest)
	}
	srcArchDesc, err := srcArchives.open("tool").Resolve(ctx, "v1.0.0")
	if err != nil {
		t.Fatalf("resolve src tool archive: %v", err)
	}
	dstArchDesc, err := dstArchives.open("tool").Resolve(ctx, "v1.0.0")
	if err != nil {
		t.Fatalf("resolve dst tool archive: %v", err)
	}
	if srcArchDesc.Digest != dstArchDesc.Digest {
		t.Fatalf("dst tool archive digest %s != src digest %s", dstArchDesc.Digest, srcArchDesc.Digest)
	}

	// Consumer: only ever touches the mirrored (dst) registry, never the
	// connected side and never the closed upstream.
	consumerNemHome := t.TempDir()
	projDir := t.TempDir()
	chdir(t, projDir)

	if _, errb, err := runNem(t, consumerNemHome, "catalog", "add", "air", "internal.example.com/cat:v2"); err != nil {
		t.Fatalf("catalog add: %v\n%s", err, errb)
	}

	origSyncCatalog := syncCatalog
	syncCatalog = func(ctx context.Context, ref, storePath string, progress ocix.ProgressFunc) (string, error) {
		store, err := ocix.SyncLocalCatalog(ctx, dstCatalog, "v2", storePath, progress)
		if err != nil {
			return "", err
		}
		return store.Digest(), nil
	}
	defer func() { syncCatalog = origSyncCatalog }()

	if _, errb, err := runNem(t, consumerNemHome, "catalog", "update"); err != nil {
		t.Fatalf("catalog update: %v\n%s", err, errb)
	}

	t.Cleanup(fetch.SetPullArchive(func(ctx context.Context, catalogRef, name, tag string, plat spec.Platform, dir string) (string, error) {
		return ocix.PullArchiveFrom(ctx, dstArchives.open(name), tag, plat, dir)
	}))

	out, errb, err := runNem(t, consumerNemHome, "use", "air:tool@v1.0.0")
	if err != nil {
		t.Fatalf("use: %v\nstdout: %s\nstderr: %s", err, out, errb)
	}
	if !strings.Contains(errb, "Installed tool v1.0.0") {
		t.Fatalf("narration missing install success line: %q", errb)
	}

	h := testNemHome(consumerNemHome)
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

	// Negative case: "curl" was never filled, so it is absent from the
	// mirror. Acquire's registry pull misses and falls back to the upstream
	// url, whose server is closed — an ordinary, fast network error, never
	// a hang.
	start := time.Now()
	_, errb, err = runNem(t, consumerNemHome, "use", "air:curl@1.0.0")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("use of a package absent from the mirror must fail")
	}
	if elapsed > 5*time.Second {
		t.Fatalf("use took %s to fail; want a fast network-class error, not a hang", elapsed)
	}
	if !strings.Contains(errb, "curl") {
		t.Fatalf("stderr should name the failing package: %q", errb)
	}
	if !strings.Contains(errb, "connection refused") || !strings.Contains(errb, "network") {
		t.Fatalf("stderr should carry an ordinary network-class error and hint: %q", errb)
	}
}
