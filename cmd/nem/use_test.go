package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vi-dev/nem-cli/internal/catalog"
	"github.com/vi-dev/nem-cli/internal/home"
	"github.com/vi-dev/nem-cli/internal/install"
	"github.com/vi-dev/nem-cli/internal/ocix"
	"github.com/vi-dev/nem-cli/internal/project"
	"github.com/vi-dev/nem-cli/internal/resolve"
)

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
// httptest server, with sha256 computed from the archive bytes.
func downloadableDirCatalog(t *testing.T) string {
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
`
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
	catalogRoot := downloadableDirCatalog(t)
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
	// "use demo:tool" (no @version) declares the latest version: the
	// manifest records an empty version, and the lock below pins the
	// resolved v1.0.0.
	if len(m.Tools) != 1 || m.Tools[0].Key.String() != "demo:tool" || m.Tools[0].Version != "" {
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
	catalogRoot := downloadableDirCatalog(t)
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
	catalogRoot := downloadableDirCatalog(t)
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

func TestUseErrNotSyncedHintWiring(t *testing.T) {
	nemHomeDir := t.TempDir()
	projDir := t.TempDir()
	chdir(t, projDir)

	// No catalog has been added: OpenConfig writes the default "official"
	// oci catalog, which has never been synced.
	_, errb, err := runNem(t, nemHomeDir, "use", "anything")
	if err == nil {
		t.Fatal("want error: official catalog is not synced")
	}
	if !errors.Is(err, ocix.ErrNotSynced) {
		t.Fatalf("want ocix.ErrNotSynced, got %v", err)
	}
	if !strings.Contains(errb, "nem catalog update") {
		t.Fatalf("stderr should carry the ErrNotSynced hint: %q", errb)
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
