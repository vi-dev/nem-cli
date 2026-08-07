package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStatusListsToolsAndEnv(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "nem.toml"), []byte(
		"[tools]\ngo = \"v1.26.5\"\n\"dev:kubectl\" = \"v1.34.1\"\n\n[env]\nCGO_ENABLED = \"0\"\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "nem.lock"), []byte(
		"# machine-written by nem — do not edit\nversion = 1\n\n[[package]]\nname = \"go\"\nversion = \"v1.26.5\"\ncatalog = \"official\"\ndirect = true\nplatforms = [\"linux/amd64\"]\n"), 0o644)

	t.Setenv("NEM_HOME", filepath.Join(dir, "nemhome")) // empty global
	cwd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(cwd)

	root := newRoot()
	var out, errb bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errb)
	root.SetArgs([]string{"status", "--color", "never"})
	if err := root.Execute(); err != nil {
		t.Fatalf("status: %v\nstderr: %s", err, errb.String())
	}
	text := out.String()
	for _, want := range []string{
		"PACKAGE", "VERSION", "CATALOG", "LOCKED",
		"go", "v1.26.5", "yes",
		"kubectl", "dev", "no",
		"CGO_ENABLED", "0",
	} {
		if !bytes.Contains(out.Bytes(), []byte(want)) {
			t.Errorf("stdout missing %q:\n%s", want, text)
		}
	}
}

func TestStatusInstalledColumn(t *testing.T) {
	nemHomeDir := t.TempDir()
	catalogRoot := downloadableDirCatalog(t)
	projDir := t.TempDir()
	chdir(t, projDir)

	if _, errb, err := runNem(t, nemHomeDir, "catalog", "add", "demo", catalogRoot); err != nil {
		t.Fatalf("catalog add: %v\n%s", err, errb)
	}
	if _, errb, err := runNem(t, nemHomeDir, "use", "demo:tool"); err != nil {
		t.Fatalf("use: %v\n%s", err, errb)
	}

	out, errb, err := runNem(t, nemHomeDir, "status")
	if err != nil {
		t.Fatalf("status: %v\nstderr: %s", err, errb)
	}
	if !strings.Contains(out, "INSTALLED") {
		t.Fatalf("stdout missing INSTALLED header:\n%s", out)
	}
	var toolLine string
	for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if strings.HasPrefix(l, "tool ") {
			toolLine = l
		}
	}
	if toolLine == "" {
		t.Fatalf("no row for tool in status output:\n%s", out)
	}
	fields := strings.Fields(toolLine)
	if got := fields[len(fields)-1]; got != "yes" {
		t.Fatalf("tool row INSTALLED cell = %q, want yes: %q", got, toolLine)
	}
}

func TestStatusUnlockedToolShowsInstalledDash(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "nem.toml"), []byte("[tools]\ngo = \"v1.26.5\"\n"), 0o644)

	t.Setenv("NEM_HOME", filepath.Join(dir, "nemhome"))
	cwd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(cwd)

	root := newRoot()
	var out, errb bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errb)
	root.SetArgs([]string{"status", "--color", "never"})
	if err := root.Execute(); err != nil {
		t.Fatalf("status: %v\nstderr: %s", err, errb.String())
	}
	var toolLine string
	for _, l := range strings.Split(strings.TrimRight(out.String(), "\n"), "\n") {
		if strings.HasPrefix(l, "go ") {
			toolLine = l
		}
	}
	if toolLine == "" {
		t.Fatalf("no row for go in status output:\n%s", out.String())
	}
	fields := strings.Fields(toolLine)
	if got := fields[len(fields)-1]; got != "-" {
		t.Fatalf("unlocked tool row INSTALLED cell = %q, want -: %q", got, toolLine)
	}
}

func TestStatusNoManifestFails(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("NEM_HOME", filepath.Join(dir, "nemhome"))
	cwd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(cwd)

	root := newRoot()
	root.SetOut(new(bytes.Buffer))
	root.SetErr(new(bytes.Buffer))
	root.SetArgs([]string{"status"})
	if err := root.Execute(); err == nil {
		t.Fatal("want error when no nem.toml anywhere")
	}
}
