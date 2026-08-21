package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vi-dev/nem-cli/internal/project"
	"github.com/vi-dev/nem-cli/internal/spec"
)

func TestEnvEmitsProjectVarOnStdoutOnly(t *testing.T) {
	nemHomeDir := t.TempDir()
	projDir := t.TempDir()
	chdir(t, projDir)

	if err := os.WriteFile(filepath.Join(projDir, "nem.toml"), []byte("[env]\nFOO = \"bar\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, errb, err := runNem(t, nemHomeDir, "env", "--shell", "bash")
	if err != nil {
		t.Fatalf("env: %v\n%s", err, errb)
	}
	if !strings.Contains(out, "export FOO='bar'\n") {
		t.Fatalf("stdout missing FOO export:\n%s", out)
	}
	if errb != "" {
		t.Fatalf("expected no stderr narration, got: %q", errb)
	}
}

func TestEnvWithNoProjectManifestStillEmitsGlobalLayer(t *testing.T) {
	nemHomeDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(nemHomeDir, "nem.toml"), []byte("[env]\nBAR = \"baz\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	projDir := t.TempDir()
	chdir(t, projDir)

	out, errb, err := runNem(t, nemHomeDir, "env", "--shell", "bash")
	if err != nil {
		t.Fatalf("env: %v\n%s", err, errb)
	}
	if !strings.Contains(out, "export BAR='baz'\n") {
		t.Fatalf("stdout missing global BAR export:\n%s", out)
	}
}

func TestEnvDoesNotWarnAboutUninstalledPackages(t *testing.T) {
	nemHomeDir := t.TempDir()
	projDir := t.TempDir()
	chdir(t, projDir)

	writeFile(t, filepath.Join(projDir, "nem.toml"), "")
	platforms := []string{spec.Current().String()}
	projLock := &project.Lockfile{Path: filepath.Join(projDir, "nem.lock"), Packages: []project.LockEntry{
		{Name: "ghost", Version: "v1.0.0", Catalog: "test", Direct: true, Platforms: platforms},
	}}
	if err := project.WriteLock(projLock); err != nil {
		t.Fatalf("WriteLock: %v", err)
	}
	globalLock := &project.Lockfile{Path: testNemHome(nemHomeDir).GlobalLock(), Packages: []project.LockEntry{
		{Name: "gtool", Version: "v2.0.0", Catalog: "test", Direct: true, Platforms: platforms},
		{Name: "htool", Version: "v3.0.0", Catalog: "test", Direct: true, Platforms: platforms},
	}}
	if err := project.WriteLock(globalLock); err != nil {
		t.Fatalf("WriteLock: %v", err)
	}

	// The activation hook re-evaluates `nem env` on every directory change
	// (and after `nem <anything> --help` via the sync-family wrapper), so
	// uninstalled packages must not produce any narration here — `nem sync`
	// and `nem status` own that warning.
	_, errb, err := runNem(t, nemHomeDir, "env", "--shell", "bash")
	if err != nil {
		t.Fatalf("env: %v\n%s", err, errb)
	}
	if errb != "" {
		t.Fatalf("expected silent stderr for uninstalled packages, got: %q", errb)
	}
}

func TestEnvFishShellErrors(t *testing.T) {
	nemHomeDir := t.TempDir()
	projDir := t.TempDir()
	chdir(t, projDir)

	out, _, err := runNem(t, nemHomeDir, "env", "--shell", "fish")
	if err == nil {
		t.Fatal("want error for fish shell")
	}
	if !strings.Contains(err.Error(), "fish") {
		t.Fatalf("error should mention fish: %v", err)
	}
	if out != "" {
		t.Fatalf("expected no stdout on error, got: %q", out)
	}
}
