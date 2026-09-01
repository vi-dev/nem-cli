package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenConfigReadOnlyMissingFileYieldsDefaultWithoutWriting(t *testing.T) {
	h := testHome(t)
	cfg, err := OpenConfigReadOnly(h)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Catalogs) != 1 || cfg.Catalogs[0].Name != "official" {
		t.Fatalf("want default official catalog, got %+v", cfg.Catalogs)
	}
	if _, err := os.Stat(h.Config()); !os.IsNotExist(err) {
		t.Fatalf("OpenConfigReadOnly must not create config.yaml, stat err = %v", err)
	}
}

func TestOpenConfigReadOnlyReadsExistingFile(t *testing.T) {
	h := testHome(t)
	data := []byte("catalogs:\n  - name: team\n    type: oci\n    ref: ghcr.io/x/y:v2\n")
	if err := os.MkdirAll(filepath.Dir(h.Config()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(h.Config(), data, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := OpenConfigReadOnly(h)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Catalogs) != 1 || cfg.Catalogs[0].Name != "team" {
		t.Fatalf("got %+v", cfg.Catalogs)
	}
}

func TestOpenConfigReadOnlyMalformedFileErrors(t *testing.T) {
	h := testHome(t)
	if err := os.MkdirAll(filepath.Dir(h.Config()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(h.Config(), []byte("catalogs: [not valid"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenConfigReadOnly(h); err == nil {
		t.Fatal("want error for malformed config.yaml")
	}
}
