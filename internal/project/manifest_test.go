package project

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseToolKey(t *testing.T) {
	cases := []struct {
		in        string
		cat, name string
		ok        bool
	}{
		{"go", "", "go", true},
		{"dev:kubectl", "dev", "kubectl", true},
		{":go", "", "", false},
		{"dev:", "", "", false},
		{"Bad:go", "", "", false},
	}
	for _, c := range cases {
		k, err := ParseToolKey(c.in)
		if c.ok != (err == nil) {
			t.Errorf("%q: err=%v", c.in, err)
			continue
		}
		if c.ok && (k.Catalog != c.cat || k.Name != c.name) {
			t.Errorf("%q: got %+v", c.in, k)
		}
	}
}

func TestLoadManifest(t *testing.T) {
	dir := t.TempDir()
	toml := `
[tools]
go = "v1.26.5"
"dev:kubectl" = "v1.34.1"

[env]
KUBECONFIG = "$HOME/.kube/config"
`
	os.WriteFile(filepath.Join(dir, "nem.toml"), []byte(toml), 0o644)
	m, err := LoadManifest(filepath.Join(dir, "nem.toml"))
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if len(m.Tools) != 2 {
		t.Fatalf("tools: %+v", m.Tools)
	}
	// entries sorted by name for determinism
	if m.Tools[0].Key.Name != "go" || m.Tools[1].Key.Catalog != "dev" {
		t.Fatalf("tool entries: %+v", m.Tools)
	}
	if len(m.Env) != 1 || m.Env[0].Value != "$HOME/.kube/config" {
		t.Fatalf("env: %+v", m.Env)
	}
}

func TestLoadManifestMissingIsEmpty(t *testing.T) {
	m, err := LoadManifest(filepath.Join(t.TempDir(), "nem.toml"))
	if err != nil || len(m.Tools) != 0 {
		t.Fatalf("missing file: %+v, %v", m, err)
	}
}

func TestLoadManifestRejectsDuplicateName(t *testing.T) {
	dir := t.TempDir()
	toml := "[tools]\ngo = \"v1\"\n\"dev:go\" = \"v2\"\n"
	os.WriteFile(filepath.Join(dir, "nem.toml"), []byte(toml), 0o644)
	if _, err := LoadManifest(filepath.Join(dir, "nem.toml")); err == nil {
		t.Fatal("duplicate package name across catalogs must be rejected")
	}
}

func TestLoadManifestRejectsUnknownTable(t *testing.T) {
	dir := t.TempDir()
	toml := "[tool]\ngo = \"v1\"\n"
	os.WriteFile(filepath.Join(dir, "nem.toml"), []byte(toml), 0o644)
	_, err := LoadManifest(filepath.Join(dir, "nem.toml"))
	if err == nil {
		t.Fatal("want error for unknown table [tool]")
	}
	if !strings.Contains(err.Error(), "tool") {
		t.Fatalf("want error mentioning the unknown key, got %v", err)
	}
}

func TestDiscover(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "a", "b")
	os.MkdirAll(nested, 0o755)
	os.WriteFile(filepath.Join(root, "nem.toml"), []byte("[tools]\n"), 0o644)

	dir, err := Discover(nested)
	if err != nil || dir != root {
		t.Fatalf("Discover: %q, %v", dir, err)
	}

	lonely := t.TempDir()
	if _, err := Discover(lonely); !errors.Is(err, ErrNoManifest) {
		t.Fatalf("want ErrNoManifest, got %v", err)
	}
}
