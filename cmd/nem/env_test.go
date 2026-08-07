package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
