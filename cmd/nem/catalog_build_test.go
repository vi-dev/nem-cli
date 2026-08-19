package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
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
