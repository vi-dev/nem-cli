package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/vi-dev/nem-cli/internal/home"
	"github.com/vi-dev/nem-cli/internal/install"
	"github.com/vi-dev/nem-cli/internal/project"
	"github.com/vi-dev/nem-cli/internal/spec"
)

// writeFile is a small os.WriteFile wrapper that fails the test on error,
// used throughout to seed manifest/lock fixtures.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestExecCapturesProjectEnvVar(t *testing.T) {
	nemHomeDir := t.TempDir()
	projDir := t.TempDir()
	chdir(t, projDir)
	writeFile(t, filepath.Join(projDir, "nem.toml"), "[env]\nFOO = \"bar\"\n")

	out, errb, err := runNem(t, nemHomeDir, "exec", "--", "/bin/sh", "-c", "echo $FOO")
	if err != nil {
		t.Fatalf("exec: %v\n%s", err, errb)
	}
	if out != "bar\n" {
		t.Fatalf("stdout = %q, want %q", out, "bar\n")
	}
}

func TestExecWithNoProjectManifestStillRunsWithGlobalLayer(t *testing.T) {
	nemHomeDir := t.TempDir()
	writeFile(t, filepath.Join(nemHomeDir, "nem.toml"), "[env]\nBAR = \"baz\"\n")
	projDir := t.TempDir()
	chdir(t, projDir)

	out, errb, err := runNem(t, nemHomeDir, "exec", "--", "/bin/sh", "-c", "echo $BAR")
	if err != nil {
		t.Fatalf("exec: %v\n%s", err, errb)
	}
	if out != "baz\n" {
		t.Fatalf("stdout = %q, want %q", out, "baz\n")
	}
}

func TestExecExitCodePropagation(t *testing.T) {
	nemHomeDir := t.TempDir()
	projDir := t.TempDir()
	chdir(t, projDir)

	_, _, err := runNem(t, nemHomeDir, "exec", "--", "/bin/sh", "-c", "exit 7")
	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("exec error = %v, want *ExitError", err)
	}
	if exitErr.Code != 7 {
		t.Fatalf("exit code = %d, want 7", exitErr.Code)
	}
}

func TestExecNotFoundCommandExits127(t *testing.T) {
	nemHomeDir := t.TempDir()
	projDir := t.TempDir()
	chdir(t, projDir)

	_, _, err := runNem(t, nemHomeDir, "exec", "--", "definitely-not-a-real-command-xyz")
	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("exec error = %v, want *ExitError", err)
	}
	if exitErr.Code != 127 {
		t.Fatalf("exit code = %d, want 127", exitErr.Code)
	}
}

// TestExecLeadingPersistentFlagIsParsedNotPassedToChild proves a root
// persistent flag placed before "exec" is still parsed as nem's own flag
// (SetInterspersed(false) only stops parsing at the first positional
// argument, which is "--" or the child's own command name — never
// before it) rather than leaking into the child's argv.
func TestExecLeadingPersistentFlagIsParsedNotPassedToChild(t *testing.T) {
	nemHomeDir := t.TempDir()
	projDir := t.TempDir()
	chdir(t, projDir)
	t.Setenv("NEM_HOME", nemHomeDir)

	root := newRoot()
	var out, errb bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errb)
	root.SetArgs([]string{"--verbose", "exec", "--", "/bin/echo", "hi"})
	if err := root.Execute(); err != nil {
		t.Fatalf("exec: %v\n%s", err, errb.String())
	}
	if out.String() != "hi\n" {
		t.Fatalf("stdout = %q, want %q", out.String(), "hi\n")
	}
	if !flagVerbose {
		t.Fatal("--verbose before exec was not parsed as a root persistent flag")
	}
}

// TestExecNoCommandGivenIsUsageError constructs root directly instead of
// through runNem: runNem always appends "--color never" after the given
// args, which — once exec's own "--" has already terminated flag parsing —
// would land in the child's argv instead of being parsed as a root flag,
// masking the empty-args case this test means to exercise.
func TestExecNoCommandGivenIsUsageError(t *testing.T) {
	nemHomeDir := t.TempDir()
	projDir := t.TempDir()
	chdir(t, projDir)
	t.Setenv("NEM_HOME", nemHomeDir)

	root := newRoot()
	var out, errb bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errb)
	root.SetArgs([]string{"exec", "--"})
	if err := root.Execute(); err == nil {
		t.Fatal("want error when no command follows --")
	}
	if ranHook {
		t.Fatal("hook ran for a missing exec command; usage errors must exit 2")
	}
}

// fakeToolYAML describes a minimal package whose only bin is bin/mytool,
// installed directly via install.Install rather than through a catalog, so
// exec's PATH resolution can be exercised without a real fetch.
const fakeToolYAML = `
schema: 2
name: mytool
artifact:
  oci: ":{{.Version}}"
install:
  - extract: {}
bins: ["bin"]
versions:
  - v1.0.0
`

// installFakeTool installs a "mytool" package directly into h whose
// bin/mytool script echoes marker, and returns the resulting lock entry so
// a test can splice it into a project's nem.lock.
func installFakeTool(t *testing.T, h home.Home, marker string) project.LockEntry {
	t.Helper()
	pkg, err := spec.Parse([]byte(fakeToolYAML))
	if err != nil {
		t.Fatalf("parse fake tool package: %v", err)
	}
	archive := makeTarGz(t, map[string]string{"bin/mytool": "#!/bin/sh\necho " + marker + "\n"})
	artifact := filepath.Join(t.TempDir(), "artifact.tar.gz")
	writeBinary(t, artifact, archive)

	if err := install.Install(context.Background(), h, pkg, "v1.0.0", "test", artifact); err != nil {
		t.Fatalf("install fake tool: %v", err)
	}
	return project.LockEntry{
		Name: "mytool", Version: "v1.0.0", Catalog: "test", Direct: true,
		Platforms: []string{spec.Current().String()},
	}
}

func writeBinary(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestExecResolvesPathInjectedToolByBareName(t *testing.T) {
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

	out, errb, err := runNem(t, nemHomeDir, "exec", "--", "mytool")
	if err != nil {
		t.Fatalf("exec: %v\n%s", err, errb)
	}
	if out != "hello-from-mytool\n" {
		t.Fatalf("stdout = %q, want %q", out, "hello-from-mytool\n")
	}
}

func TestExecChildEnvHasNoSavedBookkeeping(t *testing.T) {
	nemHomeDir := t.TempDir()
	projDir := t.TempDir()
	chdir(t, projDir)
	writeFile(t, filepath.Join(projDir, "nem.toml"), "[env]\nFOO = \"bar\"\n")

	out, errb, err := runNem(t, nemHomeDir, "exec", "--", "/bin/sh", "-c", "env")
	if err != nil {
		t.Fatalf("exec: %v\n%s", err, errb)
	}
	// FOO is the var this run manages: exec must never synthesize its
	// shell.EnvScript-style save/restore bookkeeping for it, unlike `nem
	// env`/activate. Checking for the specific NEM_SAVED__FOO* keys (rather
	// than scanning for any "NEM_SAVED__" substring) keeps this assertion
	// accurate even when the outer test process itself is running inside an
	// activated nem shell with unrelated managed vars already saved.
	if strings.Contains(out, "NEM_SAVED__FOO") {
		t.Fatalf("child env leaked NEM_SAVED__FOO bookkeeping:\n%s", out)
	}
	if !strings.Contains(out, "FOO=bar") {
		t.Fatalf("child env missing composed FOO:\n%s", out)
	}
}

func TestExecComposeWarningsGoToStderr(t *testing.T) {
	nemHomeDir := t.TempDir()
	projDir := t.TempDir()
	chdir(t, projDir)
	writeFile(t, filepath.Join(projDir, "nem.toml"), "")

	lf := &project.Lockfile{Path: filepath.Join(projDir, "nem.lock"), Packages: []project.LockEntry{
		{Name: "ghost", Version: "v1.0.0", Catalog: "test", Direct: true, Platforms: []string{spec.Current().String()}},
	}}
	if err := project.WriteLock(lf); err != nil {
		t.Fatalf("WriteLock: %v", err)
	}

	_, errb, err := runNem(t, nemHomeDir, "exec", "--", "/bin/sh", "-c", "true")
	if err != nil {
		t.Fatalf("exec: %v\n%s", err, errb)
	}
	if !strings.Contains(errb, "no install metadata for ghost@v1.0.0") {
		t.Fatalf("stderr missing compose warning: %q", errb)
	}
}

// TestLookPathNeverReturnsBareNameForEmptyPathComponent guards against a
// regression where an empty PATH component (a common trailing-colon shell
// artifact, meaning "and the current directory") resolves via
// filepath.Join(".", name), which filepath.Clean reduces to a bare name.
// Handing exec.CommandContext a separator-free name makes *exec.Cmd run its
// own LookPath against the real process PATH instead of the composed one
// lookPath was given — silently reintroducing the exact ambient-PATH
// fallback this function exists to prevent.
func TestLookPathNeverReturnsBareNameForEmptyPathComponent(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	toolPath := filepath.Join(dir, "footool")
	writeFile(t, toolPath, "#!/bin/sh\necho footool-ran\n")
	if err := os.Chmod(toolPath, 0o755); err != nil {
		t.Fatal(err)
	}

	resolved, err := lookPath("footool", "/nonexistent-dir-xyz:")
	if err != nil {
		t.Fatalf("lookPath: %v", err)
	}
	if !strings.ContainsRune(resolved, os.PathSeparator) {
		t.Fatalf("lookPath returned a bare name %q; exec.Command would "+
			"re-resolve it against the real process PATH instead of the "+
			"composed one", resolved)
	}
	info, statErr := os.Stat(resolved)
	if statErr != nil || info.IsDir() {
		t.Fatalf("resolved path %q does not point at the installed tool: %v", resolved, statErr)
	}
}

// TestExecResolvesToolViaEmptyAmbientPathComponent exercises the same bug
// end to end through exec: with no project/global package providing
// "cwdtool", the only way exec can find it at all is by correctly
// resolving PATH's trailing empty component to the working directory. A
// bare-name resolution here would otherwise either fail outright or (worse)
// silently defer to *exec.Cmd's own ambient-PATH lookup instead of running
// the tool actually being pointed at.
func TestExecResolvesToolViaEmptyAmbientPathComponent(t *testing.T) {
	nemHomeDir := t.TempDir()
	projDir := t.TempDir()
	chdir(t, projDir)
	writeFile(t, filepath.Join(projDir, "nem.toml"), "")

	toolPath := filepath.Join(projDir, "cwdtool")
	writeFile(t, toolPath, "#!/bin/sh\necho from-cwd-empty-path-component\n")
	if err := os.Chmod(toolPath, 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", "/nonexistent-dir-xyz:")

	out, errb, err := runNem(t, nemHomeDir, "exec", "--", "cwdtool")
	if err != nil {
		t.Fatalf("exec: %v\n%s", err, errb)
	}
	if out != "from-cwd-empty-path-component\n" {
		t.Fatalf("stdout = %q, want %q", out, "from-cwd-empty-path-component\n")
	}
}

// TestExitCodeForSignalKill covers the 128+signal branch of exitCodeFor: a
// child killed by a signal must report 128 plus that signal's number, not
// its own exit code (there isn't one) or a generic failure code.
func TestExitCodeForSignalKill(t *testing.T) {
	cmd := exec.Command("/bin/sleep", "5")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal sleep: %v", err)
	}

	err := cmd.Wait()
	if err == nil {
		t.Fatal("want error from a signal-killed process")
	}
	want := 128 + int(syscall.SIGTERM)
	if got := exitCodeFor(err); got != want {
		t.Fatalf("exitCodeFor = %d, want %d", got, want)
	}
}
