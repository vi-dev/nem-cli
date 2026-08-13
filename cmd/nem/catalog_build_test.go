package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
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
