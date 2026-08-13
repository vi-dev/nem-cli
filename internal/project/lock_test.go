package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLockRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nem.lock")
	lf := &Lockfile{Path: path, Packages: []LockEntry{
		{Name: "helm", Version: "v3.16.0", Catalog: "official", Direct: false,
			Platforms: []string{"linux/arm64", "linux/amd64"}, Digest: "sha256:bbb"},
		{Name: "go", Version: "v1.26.5", Catalog: "official", Direct: true,
			Platforms: []string{"darwin/arm64", "darwin/amd64", "linux/arm64", "linux/amd64"},
			Digest:    "sha256:aaa"},
	}}
	if err := WriteLock(lf); err != nil {
		t.Fatalf("WriteLock: %v", err)
	}

	data, _ := os.ReadFile(path)
	text := string(data)
	if !strings.Contains(text, "machine-written") || !strings.Contains(text, "version = 2") {
		t.Fatalf("header missing:\n%s", text)
	}
	if strings.Index(text, `name = "go"`) > strings.Index(text, `name = "helm"`) {
		t.Fatalf("entries not sorted by name:\n%s", text)
	}

	loaded, err := LoadLock(path)
	if err != nil {
		t.Fatalf("LoadLock: %v", err)
	}
	if len(loaded.Packages) != 2 || loaded.Packages[0].Name != "go" ||
		loaded.Packages[0].Digest != "sha256:aaa" || !loaded.Packages[0].Direct {
		t.Fatalf("reload: %+v", loaded.Packages)
	}
}

func TestLoadLockMissingIsEmpty(t *testing.T) {
	lf, err := LoadLock(filepath.Join(t.TempDir(), "nem.lock"))
	if err != nil || len(lf.Packages) != 0 {
		t.Fatalf("missing lock: %+v, %v", lf, err)
	}
}

func TestLoadLockRejectsUnknownVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nem.lock")
	os.WriteFile(path, []byte("version = 9\n"), 0o644)
	if _, err := LoadLock(path); err == nil {
		t.Fatal("want error for unknown lock version")
	}
}

func TestLoadLockV1SynthesizesOnPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nem.lock")
	os.WriteFile(path, []byte("version = 1\n\n[[package]]\nname = \"go\"\nversion = \"v1.26.5\"\ncatalog = \"c\"\ndirect = true\nplatforms = [\"linux/amd64\"]\n"), 0o644)
	lf, err := LoadLock(path)
	if err != nil {
		t.Fatalf("LoadLock: %v", err)
	}
	if !lf.Packages[0].OnPath {
		t.Fatalf("v1 lock entries must synthesize on_path=true, got %+v", lf.Packages[0])
	}
}

func TestLockV2RoundTripPreservesRoles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nem.lock")
	lf := &Lockfile{Path: path, Packages: []LockEntry{
		{Name: "openssl", Version: "v3.4.0", Catalog: "c", Platforms: []string{"linux/amd64"}, OnPath: false, OnLoaderPath: true},
	}}
	if err := WriteLock(lf); err != nil {
		t.Fatalf("WriteLock: %v", err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "version = 2") {
		t.Fatalf("v2 lock must declare version = 2:\n%s", data)
	}
	loaded, err := LoadLock(path)
	if err != nil {
		t.Fatalf("LoadLock: %v", err)
	}
	e := loaded.Packages[0]
	if e.OnPath || !e.OnLoaderPath {
		t.Fatalf("roles not round-tripped: %+v", e)
	}
}

func TestLoadLockRejectsUnknownField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nem.lock")
	os.WriteFile(path, []byte("version = 1\n\n[[package]]\nname = \"go\"\nversion = \"v1.26.5\"\ntypo = true\n"), 0o644)
	_, err := LoadLock(path)
	if err == nil {
		t.Fatal("want error for unknown field in lock entry")
	}
	if !strings.Contains(err.Error(), "typo") {
		t.Fatalf("want error mentioning the unknown key, got %v", err)
	}
}
