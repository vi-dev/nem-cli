package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vi-dev/nem-cli/internal/install"
	"github.com/vi-dev/nem-cli/internal/project"
)

func TestLockRegeneratesLockAndInstalls(t *testing.T) {
	nemHomeDir := t.TempDir()
	catalogRoot := downloadableDirCatalog(t, "")
	projDir := t.TempDir()
	chdir(t, projDir)

	if _, errb, err := runNem(t, nemHomeDir, "catalog", "add", "demo", catalogRoot); err != nil {
		t.Fatalf("catalog add: %v\n%s", err, errb)
	}

	manifestBody := "[tools]\n\"demo:tool\" = \"v1.0.0\"\n"
	manifestPath := filepath.Join(projDir, "nem.toml")
	if err := os.WriteFile(manifestPath, []byte(manifestBody), 0o644); err != nil {
		t.Fatal(err)
	}

	_, errb, err := runNem(t, nemHomeDir, "lock")
	if err != nil {
		t.Fatalf("lock: %v\n%s", err, errb)
	}
	if !strings.Contains(errb, "Installed tool v1.0.0") {
		t.Fatalf("narration missing install success line: %q", errb)
	}

	lock, err := project.LoadLock(filepath.Join(projDir, "nem.lock"))
	if err != nil {
		t.Fatalf("LoadLock: %v", err)
	}
	if len(lock.Packages) != 1 || lock.Packages[0].Name != "tool" || lock.Packages[0].Version != "v1.0.0" {
		t.Fatalf("lock entries: %+v", lock.Packages)
	}
	if !install.IsInstalled(testNemHome(nemHomeDir), "tool", "v1.0.0") {
		t.Fatal("lock did not install tool v1.0.0")
	}

	after, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != manifestBody {
		t.Fatalf("lock must not rewrite nem.toml:\nbefore: %q\nafter:  %q", manifestBody, after)
	}
}

func TestLockUnpinnedToolFails(t *testing.T) {
	nemHomeDir := t.TempDir()
	projDir := t.TempDir()
	chdir(t, projDir)

	body := "[tools]\ntool = \"\"\n"
	if err := os.WriteFile(filepath.Join(projDir, "nem.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	_, errb, err := runNem(t, nemHomeDir, "lock")
	if err == nil {
		t.Fatal("want error for unpinned tool")
	}
	if !strings.Contains(err.Error(), "tool") {
		t.Fatalf("error should name the unpinned tool: %v", err)
	}
	if !strings.Contains(errb, "Pin exact versions") {
		t.Fatalf("stderr should carry the pin hint: %q", errb)
	}
	if _, err := os.Stat(filepath.Join(projDir, "nem.lock")); !os.IsNotExist(err) {
		t.Fatalf("nem.lock must not be written on validation failure: %v", err)
	}
}

func TestLockNoManifestFails(t *testing.T) {
	nemHomeDir := t.TempDir()
	projDir := t.TempDir()
	chdir(t, projDir)

	_, _, err := runNem(t, nemHomeDir, "lock")
	if err == nil {
		t.Fatal("want error when no nem.toml anywhere")
	}
	if !errors.Is(err, project.ErrNoManifest) {
		t.Fatalf("want project.ErrNoManifest, got %v", err)
	}
}

func TestLockGlobalMissingManifestFails(t *testing.T) {
	nemHomeDir := t.TempDir()
	chdir(t, t.TempDir())

	h := testNemHome(nemHomeDir)
	lockBody := "# machine-written by nem — do not edit\nversion = 2\n\n" +
		"[[package]]\nname = \"tool\"\nversion = \"v1.0.0\"\ncatalog = \"demo\"\n" +
		"direct = true\nplatforms = [\"linux/amd64\"]\non_path = true\n"
	if err := os.WriteFile(h.GlobalLock(), []byte(lockBody), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := runNem(t, nemHomeDir, "lock", "-g")
	if err == nil {
		t.Fatal("want error when the global manifest does not exist")
	}
	if !errors.Is(err, project.ErrNoManifest) {
		t.Fatalf("want project.ErrNoManifest, got %v", err)
	}

	after, err := os.ReadFile(h.GlobalLock())
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != lockBody {
		t.Fatalf("global lock must be untouched on error:\nbefore: %q\nafter:  %q", lockBody, after)
	}
}

func TestLockGlobalTargetsGlobalManifest(t *testing.T) {
	nemHomeDir := t.TempDir()
	catalogRoot := downloadableDirCatalog(t, "")
	projDir := t.TempDir()
	chdir(t, projDir)

	if _, errb, err := runNem(t, nemHomeDir, "catalog", "add", "demo", catalogRoot); err != nil {
		t.Fatalf("catalog add: %v\n%s", err, errb)
	}

	h := testNemHome(nemHomeDir)
	body := "[tools]\n\"demo:tool\" = \"v1.0.0\"\n"
	if err := os.WriteFile(h.GlobalManifest(), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	_, errb, err := runNem(t, nemHomeDir, "lock", "-g")
	if err != nil {
		t.Fatalf("lock -g: %v\n%s", err, errb)
	}

	lock, err := project.LoadLock(h.GlobalLock())
	if err != nil {
		t.Fatalf("LoadLock: %v", err)
	}
	if len(lock.Packages) != 1 || lock.Packages[0].Name != "tool" || lock.Packages[0].Version != "v1.0.0" {
		t.Fatalf("global lock entries: %+v", lock.Packages)
	}
	if !install.IsInstalled(h, "tool", "v1.0.0") {
		t.Fatal("lock -g did not install tool v1.0.0")
	}
	if _, err := os.Stat(filepath.Join(projDir, "nem.lock")); !os.IsNotExist(err) {
		t.Fatal("lock -g must not write a project nem.lock")
	}
}
