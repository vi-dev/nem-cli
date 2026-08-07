package main

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vi-dev/nem-cli/internal/project"
)

func TestWhichResolvesInstalledToolUnderNemHome(t *testing.T) {
	nemHomeDir := t.TempDir()
	h := testNemHome(nemHomeDir)
	projDir := t.TempDir()
	chdir(t, projDir)
	writeFile(t, filepath.Join(projDir, "nem.toml"), "")

	entry := installFakeTool(t, h, "hello-from-mytool")
	lf := &project.Lockfile{Path: filepath.Join(projDir, "nem.lock"), Packages: []project.LockEntry{entry}}
	if err := project.WriteLock(lf); err != nil {
		t.Fatalf("WriteLock: %v", err)
	}

	out, errb, err := runNem(t, nemHomeDir, "which", "mytool")
	if err != nil {
		t.Fatalf("which: %v\n%s", err, errb)
	}
	resolved := strings.TrimSuffix(out, "\n")
	if !filepath.IsAbs(resolved) {
		t.Fatalf("resolved path %q is not absolute", resolved)
	}
	wantPrefix := filepath.Join(nemHomeDir, "packages")
	if !strings.HasPrefix(resolved, wantPrefix) {
		t.Fatalf("resolved path = %q, want under %s", resolved, wantPrefix)
	}
}

// TestWhichAgreesWithExecForInstalledTool proves which resolves the exact
// path exec would actually run: both go through the same lookPath call
// against the same composed PATH, so a bare tool name can never point
// which and exec at different binaries.
func TestWhichAgreesWithExecForInstalledTool(t *testing.T) {
	nemHomeDir := t.TempDir()
	h := testNemHome(nemHomeDir)
	projDir := t.TempDir()
	chdir(t, projDir)
	writeFile(t, filepath.Join(projDir, "nem.toml"), "")

	entry := installFakeTool(t, h, "hello-from-mytool")
	lf := &project.Lockfile{Path: filepath.Join(projDir, "nem.lock"), Packages: []project.LockEntry{entry}}
	if err := project.WriteLock(lf); err != nil {
		t.Fatalf("WriteLock: %v", err)
	}

	whichOut, errb, err := runNem(t, nemHomeDir, "which", "mytool")
	if err != nil {
		t.Fatalf("which: %v\n%s", err, errb)
	}
	whichResolved := strings.TrimSuffix(whichOut, "\n")

	t.Setenv("NEM_HOME", nemHomeDir)
	_, pathValue, err := composedPath()
	if err != nil {
		t.Fatalf("composedPath: %v", err)
	}
	execResolved, err := lookPath("mytool", pathValue)
	if err != nil {
		t.Fatalf("lookPath: %v", err)
	}
	if whichResolved != execResolved {
		t.Fatalf("which resolved %q, exec's own lookPath resolved %q", whichResolved, execResolved)
	}
}

func TestWhichResolvesUnshadowedSystemTool(t *testing.T) {
	systemPath, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not found on system PATH")
	}
	nemHomeDir := t.TempDir()
	projDir := t.TempDir()
	chdir(t, projDir)

	out, errb, err := runNem(t, nemHomeDir, "which", "sh")
	if err != nil {
		t.Fatalf("which: %v\n%s", err, errb)
	}
	resolved := strings.TrimSuffix(out, "\n")
	if resolved != systemPath {
		t.Fatalf("resolved = %q, want system path %q", resolved, systemPath)
	}
}

func TestWhichUnknownToolExitsOneWithNoStdout(t *testing.T) {
	nemHomeDir := t.TempDir()
	projDir := t.TempDir()
	chdir(t, projDir)

	out, _, err := runNem(t, nemHomeDir, "which", "definitely-not-a-real-tool-xyz")
	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("which error = %v, want *ExitError", err)
	}
	if exitErr.Code != 1 {
		t.Fatalf("exit code = %d, want 1", exitErr.Code)
	}
	if out != "" {
		t.Fatalf("stdout = %q, want empty", out)
	}
}

func TestWhichMultipleToolsEachOwnLine(t *testing.T) {
	nemHomeDir := t.TempDir()
	h := testNemHome(nemHomeDir)
	projDir := t.TempDir()
	chdir(t, projDir)
	writeFile(t, filepath.Join(projDir, "nem.toml"), "")

	systemPath, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not found on system PATH")
	}

	entry := installFakeTool(t, h, "hello-from-mytool")
	lf := &project.Lockfile{Path: filepath.Join(projDir, "nem.lock"), Packages: []project.LockEntry{entry}}
	if err := project.WriteLock(lf); err != nil {
		t.Fatalf("WriteLock: %v", err)
	}

	out, errb, err := runNem(t, nemHomeDir, "which", "mytool", "sh")
	if err != nil {
		t.Fatalf("which: %v\n%s", err, errb)
	}
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("stdout lines = %v, want 2", lines)
	}
	if !strings.HasPrefix(lines[0], filepath.Join(nemHomeDir, "packages")) {
		t.Fatalf("first line = %q, want under nem packages dir", lines[0])
	}
	if lines[1] != systemPath {
		t.Fatalf("second line = %q, want %q", lines[1], systemPath)
	}
}

// TestWhichPartialResolutionStillExitsOneAfterPrintingFoundOnes proves an
// unresolved tool doesn't short-circuit the rest: every arg is resolved,
// the found ones are printed, and only then does the command exit 1.
func TestWhichPartialResolutionStillExitsOneAfterPrintingFoundOnes(t *testing.T) {
	nemHomeDir := t.TempDir()
	h := testNemHome(nemHomeDir)
	projDir := t.TempDir()
	chdir(t, projDir)
	writeFile(t, filepath.Join(projDir, "nem.toml"), "")

	entry := installFakeTool(t, h, "hello-from-mytool")
	lf := &project.Lockfile{Path: filepath.Join(projDir, "nem.lock"), Packages: []project.LockEntry{entry}}
	if err := project.WriteLock(lf); err != nil {
		t.Fatalf("WriteLock: %v", err)
	}

	out, _, err := runNem(t, nemHomeDir, "which", "mytool", "definitely-not-a-real-tool-xyz")
	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("which error = %v, want *ExitError", err)
	}
	if exitErr.Code != 1 {
		t.Fatalf("exit code = %d, want 1", exitErr.Code)
	}
	resolved := strings.TrimSuffix(out, "\n")
	if !strings.HasPrefix(resolved, filepath.Join(nemHomeDir, "packages")) {
		t.Fatalf("stdout = %q, want the resolved mytool path", resolved)
	}
}
