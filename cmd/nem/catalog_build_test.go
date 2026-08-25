package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vi-dev/nem-cli/internal/build"
	"github.com/vi-dev/nem-cli/internal/home"
	"github.com/vi-dev/nem-cli/internal/report"
	"github.com/vi-dev/nem-cli/internal/spec"
)

func TestCatalogBuildProducesOutput(t *testing.T) {
	nemHomeDir := t.TempDir()
	tgz := makeTarGz(t, map[string]string{"src/README": "hi"})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(tgz) }))
	defer srv.Close()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "pkg.yaml"), "schema: 2\nname: tool\n"+
		"artifact: {oci: \":{{.Version}}\"}\ninstall: [{extract: {}}]\n"+
		"versions: [v1.0.0]\nbuild:\n  source: {url: \""+srv.URL+"\"}\n  output: out\n"+
		"  steps:\n    - run: mkdir -p \"$NEM_OUTPUT\" && echo built > \"$NEM_OUTPUT/marker\"\n")

	_, errb, err := runNem(t, nemHomeDir, "catalog", "build", filepath.Join(dir, "pkg.yaml"), "--version", "v1.0.0")
	if err != nil {
		t.Fatalf("catalog build: %v\n%s", err, errb)
	}
}

// TestCatalogBuildSkipsTestHookWhenManifestDeclaresNoTests proves the
// build never installs a test hook for a manifest with no test: block,
// which is what spares it from archiving an output tree nothing will
// consume.
func TestCatalogBuildSkipsTestHookWhenManifestDeclaresNoTests(t *testing.T) {
	nemHomeDir := t.TempDir()
	tgz := makeTarGz(t, map[string]string{"src/README": "hi"})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(tgz) }))
	defer srv.Close()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "pkg.yaml"), "schema: 2\nname: tool\n"+
		"artifact: {oci: \":{{.Version}}\"}\ninstall: [{extract: {}}]\n"+
		"versions: [v1.0.0]\nbuild:\n  source: {url: \""+srv.URL+"\"}\n  output: out\n"+
		"  steps:\n    - run: mkdir -p \"$NEM_OUTPUT\" && echo built > \"$NEM_OUTPUT/marker\"\n")

	called := false
	orig := runPkgTest
	runPkgTest = func(ctx context.Context, h home.Home, deps []build.ResolvedDep,
		pkg *spec.Package, version, catalogName, artifactPath string,
		rep report.Reporter, stdout, stderr io.Writer) error {
		called = true
		return nil
	}
	defer func() { runPkgTest = orig }()

	_, errb, err := runNem(t, nemHomeDir, "catalog", "build", filepath.Join(dir, "pkg.yaml"), "--version", "v1.0.0")
	if err != nil {
		t.Fatalf("catalog build: %v\n%s", err, errb)
	}
	if called {
		t.Fatal("build must not invoke the test hook for a manifest with no test: steps")
	}
}

func TestCatalogBuildPushFlag(t *testing.T) {
	nemHomeDir := t.TempDir()
	tgz := makeTarGz(t, map[string]string{"src/x": "y"})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(tgz) }))
	defer srv.Close()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "pkg.yaml"), "schema: 2\nname: tool\n"+
		"artifact: {oci: \":{{.Version}}\"}\ninstall: [{extract: {}}]\nversions: [v1.0.0]\n"+
		"build:\n  source: {url: \""+srv.URL+"\"}\n  output: out\n"+
		"  steps:\n    - run: mkdir -p \"$NEM_OUTPUT\" && echo x > \"$NEM_OUTPUT/marker\"\n")

	_, errb, err := runNem(t, nemHomeDir, "catalog", "build", filepath.Join(dir, "pkg.yaml"),
		"--version", "v1.0.0", "--push", "ghcr.io/x/cat:v2", "--dry-run")
	if err != nil {
		t.Fatalf("catalog build --push --dry-run: %v\n%s", err, errb)
	}
	if !strings.Contains(errb, "Dry-run") {
		t.Fatalf("expected dry-run narration, got: %s", errb)
	}
}
