package pkgtest

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vi-dev/nem-cli/internal/build"
	"github.com/vi-dev/nem-cli/internal/clean"
	"github.com/vi-dev/nem-cli/internal/envx"
	"github.com/vi-dev/nem-cli/internal/home"
	"github.com/vi-dev/nem-cli/internal/report"
	"github.com/vi-dev/nem-cli/internal/spec"
	"github.com/vi-dev/nem-cli/internal/usage"
)

// tarFixture builds a one-file tar.gz an install action can extract, so a
// test can drive a real install without a catalog or a network.
func tarFixture(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "artifact.tar.gz")
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	body := []byte("#!/bin/sh\necho hi\n")
	if err := tw.WriteHeader(&tar.Header{Name: "bin/tool", Mode: 0o755, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// installable returns a home plus a package whose install action extracts
// tarFixture's archive, and the archive's own path.
func installable(t *testing.T, steps ...spec.TestStep) (home.Home, *spec.Package, string) {
	t.Helper()
	root := t.TempDir()
	h := home.Resolve(func(k string) string { return map[string]string{"NEM_HOME": root}[k] })
	pkg := &spec.Package{
		Name:    "tool",
		Bins:    []string{"bin"},
		Install: []spec.Action{{Extract: &spec.ExtractAction{}}},
		Test:    steps,
	}
	return h, pkg, tarFixture(t, t.TempDir())
}

// installableWithLibs is installable for a package that also ships libs:,
// so its own alias has a lib dir contributing to the loader path.
func installableWithLibs(t *testing.T, steps ...spec.TestStep) (home.Home, *spec.Package, string) {
	t.Helper()
	root := t.TempDir()
	h := home.Resolve(func(k string) string { return map[string]string{"NEM_HOME": root}[k] })
	pkg := &spec.Package{
		Name:    "tool",
		Bins:    []string{"bin"},
		Libs:    []string{"lib"},
		Install: []spec.Action{{Extract: &spec.ExtractAction{}}},
		Test:    steps,
	}
	return h, pkg, tarFixture(t, t.TempDir())
}

func runInstall(t *testing.T, h home.Home, pkg *spec.Package, artifact string) error {
	t.Helper()
	_, err := runInstallOut(t, h, pkg, artifact)
	return err
}

// runInstallOut is runInstall with everything the reporter and the steps
// wrote captured.
func runInstallOut(t *testing.T, h home.Home, pkg *spec.Package, artifact string) (string, error) {
	t.Helper()
	var b bytes.Buffer
	err := InstallAndRun(context.Background(), h, nil, pkg, "v1", "", artifact,
		report.New(&b, &b, report.Options{}), &b, &b)
	return b.String(), err
}

// runInstallWithDeps is runInstallOut with deps passed through to
// InstallAndRun, for a test that needs a dependency on the loader path.
func runInstallWithDeps(t *testing.T, h home.Home, deps []build.ResolvedDep, pkg *spec.Package, artifact string) (string, error) {
	t.Helper()
	var b bytes.Buffer
	err := InstallAndRun(context.Background(), h, deps, pkg, "v1", "", artifact,
		report.New(&b, &b, report.Options{}), &b, &b)
	return b.String(), err
}

// aliasDirs lists the entries under h.Packages() carrying the test-install
// infix, so a test can assert an alias was cleaned up (or never created).
func aliasDirs(t *testing.T, h home.Home) []string {
	t.Helper()
	entries, err := os.ReadDir(h.Packages())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if strings.Contains(e.Name(), home.TestInstallInfix) {
			out = append(out, e.Name())
		}
	}
	return out
}

func TestInstallAndRunNoStepsSucceeds(t *testing.T) {
	h, pkg, artifact := installable(t)
	if err := runInstall(t, h, pkg, artifact); err != nil {
		t.Fatalf("InstallAndRun with no steps: %v", err)
	}
}

// otherPlatform returns a supported platform that is not the running one.
func otherPlatform() spec.Platform {
	other := spec.Platform{OS: "linux", Arch: "amd64"}
	if spec.Current() == other {
		other = spec.Platform{OS: "darwin", Arch: "arm64"}
	}
	return other
}

func TestInstallAndRunSkipsOtherPlatforms(t *testing.T) {
	h, pkg, artifact := installable(t, spec.TestStep{Run: "exit 1", Platforms: []spec.Platform{otherPlatform()}})
	if err := runInstall(t, h, pkg, artifact); err != nil {
		t.Fatalf("a step for another platform must not run: %v", err)
	}
}

// TestInstallAndRunSkipsAPackageUnsupportedHere pins the package constraint
// as the outer bound on every step: a package that excludes this platform is
// never installed here, so even an unconstrained step must not run.
func TestInstallAndRunSkipsAPackageUnsupportedHere(t *testing.T) {
	h, pkg, artifact := installable(t, spec.TestStep{Run: "exit 1"})
	pkg.Platforms = []spec.Platform{otherPlatform()}
	if err := runInstall(t, h, pkg, artifact); err != nil {
		t.Fatalf("a package unsupported here must not be tested: %v", err)
	}
	if got := aliasDirs(t, h); len(got) != 0 {
		t.Fatalf("nothing should have been installed, found %v", got)
	}
}

func TestInstallAndRunTestsARealInstall(t *testing.T) {
	h, pkg, artifact := installable(t, spec.TestStep{Run: `
set -e
[ -x "$NEM_PREFIX/bin/tool" ]
[ -f "$NEM_PREFIX/.nem-meta.yaml" ]
case "$NEM_PREFIX" in *-NEMTEST-*) ;; *) exit 1 ;; esac
`})
	if err := runInstall(t, h, pkg, artifact); err != nil {
		t.Fatalf("InstallAndRun: %v", err)
	}
	if got := aliasDirs(t, h); len(got) != 0 {
		t.Fatalf("alias must be removed after success, found %v", got)
	}
}

func TestInstallAndRunRemovesTheAliasAfterFailure(t *testing.T) {
	h, pkg, artifact := installable(t, spec.TestStep{Run: "exit 1"})
	if err := runInstall(t, h, pkg, artifact); err == nil {
		t.Fatal("want an error from the failing step")
	}
	if got := aliasDirs(t, h); len(got) != 0 {
		t.Fatalf("alias must be removed after failure, found %v", got)
	}
}

// TestInstallAndRunReportsARemovalFailure proves a failed alias cleanup
// surfaces in the returned error and names the directory, rather than being
// silently swallowed.
func TestInstallAndRunReportsARemovalFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses the permission that induces the removal failure")
	}
	h, pkg, artifact := installable(t, spec.TestStep{Run: `chmod 0500 "$(dirname "$NEM_PREFIX")"`})
	err := runInstall(t, h, pkg, artifact)
	dirs := aliasDirs(t, h)
	if len(dirs) != 1 {
		t.Fatalf("want the un-removable alias left behind, found %v", dirs)
	}
	defer os.Chmod(filepath.Join(h.Packages(), dirs[0]), 0o755)
	if err == nil {
		t.Fatal("want an error reporting the failed removal")
	}
	if !strings.Contains(err.Error(), dirs[0]) {
		t.Fatalf("error must name the leaked alias dir %q, got: %v", dirs[0], err)
	}
}

// TestInstallAndRunJoinsAStepFailureAndARemovalFailure proves a step failure
// and a cleanup failure both reach the caller rather than one overwriting
// the other.
func TestInstallAndRunJoinsAStepFailureAndARemovalFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses the permission that induces the removal failure")
	}
	h, pkg, artifact := installable(t, spec.TestStep{Run: `chmod 0500 "$(dirname "$NEM_PREFIX")"; exit 3`})
	err := runInstall(t, h, pkg, artifact)
	dirs := aliasDirs(t, h)
	if len(dirs) != 1 {
		t.Fatalf("want the un-removable alias left behind, found %v", dirs)
	}
	defer os.Chmod(filepath.Join(h.Packages(), dirs[0]), 0o755)
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "test step 1") {
		t.Fatalf("error must still report the step failure, got: %v", err)
	}
	if !strings.Contains(err.Error(), dirs[0]) {
		t.Fatalf("error must also report the removal failure naming %q, got: %v", dirs[0], err)
	}
}

// TestInstallAndRunLeavesNoUsageRow proves the alias's install leaves no
// row behind in usage.json: install.Install stamps whatever name it was
// given, so without cleanup the alias's row would survive forever, one per
// tested package per run.
func TestInstallAndRunLeavesNoUsageRow(t *testing.T) {
	h, pkg, artifact := installable(t, spec.TestStep{Run: "true"})
	if err := runInstall(t, h, pkg, artifact); err != nil {
		t.Fatalf("InstallAndRun: %v", err)
	}
	idx := usage.Load(h)
	for k := range idx {
		if strings.Contains(k, home.TestInstallInfix) {
			t.Fatalf("usage index still has an alias row %q: %v", k, idx)
		}
	}
}

func TestInstallAndRunLeavesTheRealInstallAlone(t *testing.T) {
	h, pkg, artifact := installable(t, spec.TestStep{Run: "true"})
	real, err := h.PackageDir("tool", "v1")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "marker"), []byte("installed"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := runInstall(t, h, pkg, artifact); err != nil {
		t.Fatalf("InstallAndRun: %v", err)
	}

	b, err := os.ReadFile(filepath.Join(real, "marker"))
	if err != nil {
		t.Fatalf("the real install must be untouched: %v", err)
	}
	if string(b) != "installed" {
		t.Fatalf("real install marker = %q, want %q", string(b), "installed")
	}
}

// TestInstallAndRunReportsTheOutcomeOnce pins InstallAndRun as the only
// place either command announces a result, counted in agreeing English.
func TestInstallAndRunReportsTheOutcomeOnce(t *testing.T) {
	h, pkg, artifact := installable(t, spec.TestStep{Run: "true"})
	out, err := runInstallOut(t, h, pkg, artifact)
	if err != nil {
		t.Fatalf("InstallAndRun: %v", err)
	}
	if !strings.Contains(out, "Tested tool v1 (1 step)") {
		t.Fatalf("want a singular one-step report, got:\n%s", out)
	}
	if n := strings.Count(out, "Tested"); n != 1 {
		t.Fatalf("want exactly one completion line, got %d:\n%s", n, out)
	}

	h, pkg, artifact = installable(t, spec.TestStep{Run: "true"}, spec.TestStep{Run: "true"})
	out, err = runInstallOut(t, h, pkg, artifact)
	if err != nil {
		t.Fatalf("InstallAndRun: %v", err)
	}
	if !strings.Contains(out, "Tested tool v1 (2 steps)") {
		t.Fatalf("want a plural two-step report, got:\n%s", out)
	}
}

// TestInstallAndRunDoesNotReportAPassWhenNothingApplied guards the claim
// itself: a run that asserted nothing must not read like one that passed.
func TestInstallAndRunDoesNotReportAPassWhenNothingApplied(t *testing.T) {
	h, pkg, artifact := installable(t, spec.TestStep{Run: "true", Platforms: []spec.Platform{otherPlatform()}})
	out, err := runInstallOut(t, h, pkg, artifact)
	if err != nil {
		t.Fatalf("InstallAndRun: %v", err)
	}
	if strings.Contains(out, "Tested") {
		t.Fatalf("a run that asserted nothing must not report a pass, got:\n%s", out)
	}
	if !strings.Contains(out, "nothing asserted") {
		t.Fatalf("want a notice that no test applied, got:\n%s", out)
	}
}

func TestInstallAndRunFailingStepReportsIndexAndCommand(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "second-ran")
	h, pkg, artifact := installable(t,
		spec.TestStep{Run: "true"},
		spec.TestStep{Run: "exit 3"},
		spec.TestStep{Run: "touch " + marker},
	)
	err := runInstall(t, h, pkg, artifact)
	if err == nil {
		t.Fatal("want an error from the failing step")
	}
	if !strings.Contains(err.Error(), "test step 2") || !strings.Contains(err.Error(), "exit 3") {
		t.Fatalf("error must name step 2 and its command, got %v", err)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("steps after a failure must not run")
	}
}

func TestInstallAndRunUsesAScratchDirAndRemovesIt(t *testing.T) {
	record := filepath.Join(t.TempDir(), "cwd")
	t.Setenv("PKGTEST_RECORD", record)
	h, pkg, artifact := installable(t, spec.TestStep{Run: `pwd > "$PKGTEST_RECORD"`})
	if err := runInstall(t, h, pkg, artifact); err != nil {
		t.Fatalf("InstallAndRun: %v", err)
	}
	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("step did not record its cwd: %v", err)
	}
	cwd := strings.TrimSpace(string(data))
	if !strings.HasPrefix(cwd, h.Tmp()) {
		t.Fatalf("step ran in %q, want a dir under %q", cwd, h.Tmp())
	}
	if _, err := os.Stat(cwd); !os.IsNotExist(err) {
		t.Fatalf("scratch dir %q must be removed, stat err = %v", cwd, err)
	}
}

// TestInstallAndRunScratchDirIsSweptByClean pins the scratch dir's name to
// one nem clean reclaims. InstallAndRun removes its own dir, but a killed
// run cannot, and clean is then the only thing that will.
func TestInstallAndRunScratchDirIsSweptByClean(t *testing.T) {
	record := filepath.Join(t.TempDir(), "cwd")
	t.Setenv("PKGTEST_RECORD", record)
	h, pkg, artifact := installable(t, spec.TestStep{Run: `pwd > "$PKGTEST_RECORD"`})
	if err := runInstall(t, h, pkg, artifact); err != nil {
		t.Fatalf("InstallAndRun: %v", err)
	}
	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("step did not record its cwd: %v", err)
	}
	// Stand in for what a killed run leaves behind: the same directory,
	// under the same name, still on disk.
	leaked := strings.TrimSpace(string(data))
	if err := os.MkdirAll(leaked, 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := clean.Scan(h, false)
	if err != nil {
		t.Fatalf("clean.Scan: %v", err)
	}
	for _, item := range store.Staging {
		if item.Path == leaked {
			return
		}
	}
	t.Fatalf("nem clean does not sweep the scratch dir %q; it found %+v", leaked, store.Staging)
}

func TestInstallAndRunDropsInheritedLoaderPath(t *testing.T) {
	t.Setenv("LD_LIBRARY_PATH", "sentinel")
	t.Setenv("DYLD_LIBRARY_PATH", "sentinel")
	// dyld strips DYLD_* from restricted binaries (/bin/sh included) before
	// the script runs, so this can't tell InstallAndRun's own drop apart
	// from that stripping on darwin; skip that half of the check there.
	dyldCheck := `[ -z "$DYLD_LIBRARY_PATH" ]`
	if runtime.GOOS == "darwin" {
		dyldCheck = "true"
	}
	h, pkg, artifact := installable(t, spec.TestStep{Run: `
set -e
[ -n "$NEM_PREFIX" ]
[ "$NEM_VERSION" = v1 ]
[ -z "$LD_LIBRARY_PATH" ]
` + dyldCheck + `
case "$PATH" in "$NEM_PREFIX/bin":*) ;; *) exit 1 ;; esac
`})
	if err := runInstall(t, h, pkg, artifact); err != nil {
		t.Fatalf("environment assertions failed: %v", err)
	}
}

// TestInstallAndRunAppliesPackageEnvAgainstTheAlias proves a package's own
// env: export reaches a test step, rendered against the alias install dir
// rather than the package's real one — the same real-runtime template
// context (InstallDir, Version) applied to a different directory.
func TestInstallAndRunAppliesPackageEnvAgainstTheAlias(t *testing.T) {
	h, pkg, artifact := installable(t, spec.TestStep{Run: `
set -e
[ "$TOOL_HOME" = "$NEM_PREFIX" ]
case "$TOOL_HOME" in *` + home.TestInstallInfix + `*) ;; *) exit 1 ;; esac
`})
	pkg.Env = []spec.EnvExport{{Name: "TOOL_HOME", Value: "{{.InstallDir}}"}}
	if err := runInstall(t, h, pkg, artifact); err != nil {
		t.Fatalf("environment assertions failed: %v", err)
	}
}

// TestInstallAndRunSkipsAReservedPackageEnvExport proves a package env
// export named after a reserved variable is left out of the step's
// environment — PATH keeps the value InstallAndRun composed rather than
// being replaced by the export — and that the skip is reported so an
// author learns why their export was ignored.
func TestInstallAndRunSkipsAReservedPackageEnvExport(t *testing.T) {
	h, pkg, artifact := installable(t, spec.TestStep{Run: `
set -e
case "$PATH" in "$NEM_PREFIX/bin":*) ;; *) exit 1 ;; esac
`})
	pkg.Env = []spec.EnvExport{{Name: "PATH", Value: "clobbered"}}
	out, err := runInstallOut(t, h, pkg, artifact)
	if err != nil {
		t.Fatalf("a reserved package env export must be skipped, not applied: %v", err)
	}
	if !strings.Contains(out, `reserved env var "PATH"`) {
		t.Fatalf("want a reported warning naming the skipped export, got:\n%s", out)
	}
}

// TestInstallAndRunScrubsInheritedStagingAndOutputDir proves a step never
// sees a caller's own NEM_STAGING_DIR/NEM_OUTPUT: build.EnvContext supplies
// neither on this path, so without scrubbing them ComposeEnv would leave
// whatever the invoking shell already exported under those names in place.
func TestInstallAndRunScrubsInheritedStagingAndOutputDir(t *testing.T) {
	t.Setenv("NEM_STAGING_DIR", "/leftover/staging")
	t.Setenv("NEM_OUTPUT", "/leftover/output")
	h, pkg, artifact := installable(t, spec.TestStep{Run: `
set -e
[ -z "$NEM_STAGING_DIR" ]
[ -z "$NEM_OUTPUT" ]
`})
	if err := runInstall(t, h, pkg, artifact); err != nil {
		t.Fatalf("a caller's NEM_STAGING_DIR/NEM_OUTPUT must not reach a test step: %v", err)
	}
}

// TestInstallAndRunSetsLoaderPathForOwnLibs proves a package that ships
// libs: gets its own alias lib dir on the loader path — mirroring
// envx.buildLoaderPath, which always includes an on-loader-path entry's own
// lib dirs regardless of what its binaries' baked rpaths cover. No deps are
// passed here, so the package's own dir is the whole list.
func TestInstallAndRunSetsLoaderPathForOwnLibs(t *testing.T) {
	loaderVar := envx.LoaderPathVar()
	h, pkg, artifact := installableWithLibs(t, spec.TestStep{Run: `
set -e
[ "$` + loaderVar + `" = "$NEM_PREFIX/lib" ]
`})
	if err := runInstall(t, h, pkg, artifact); err != nil {
		t.Fatalf("environment assertions failed: %v", err)
	}
}

// TestInstallAndRunLoaderPathExcludesInheritedValue proves the loader
// variable a step observes is exactly the package's own dirs, never the
// caller's ambient value with those dirs appended — the prologue that sets
// it (see InstallAndRun) always assigns outright, it never reads the
// variable's existing value.
func TestInstallAndRunLoaderPathExcludesInheritedValue(t *testing.T) {
	t.Setenv("LD_LIBRARY_PATH", "sentinel")
	t.Setenv("DYLD_LIBRARY_PATH", "sentinel")
	loaderVar := envx.LoaderPathVar()
	h, pkg, artifact := installableWithLibs(t, spec.TestStep{Run: `
set -e
[ "$` + loaderVar + `" = "$NEM_PREFIX/lib" ]
`})
	if err := runInstall(t, h, pkg, artifact); err != nil {
		t.Fatalf("environment assertions failed: %v", err)
	}
}

// TestInstallAndRunSetsLoaderPathForOnLoaderPathDep proves an on-loader-path
// dependency's lib dir reaches the loader path alongside the package's own,
// with the package's own dir first — matching what a real install gets from
// envx.buildLoaderPath, and closing the gap a dependency's own binaries can
// leave: e.g. curl links brotli's libbrotlidec.so.1, which itself needs the
// sibling libbrotlicommon.so.1 out of the same lib dir, and brotli's
// libraries carry no rpath back to that sibling.
func TestInstallAndRunSetsLoaderPathForOnLoaderPathDep(t *testing.T) {
	loaderVar := envx.LoaderPathVar()
	depPrefix := t.TempDir()
	dep := build.ResolvedDep{
		Name: "brotli", Version: "v1", Prefix: depPrefix,
		OnLoaderPath: true, Libs: []string{"lib"},
	}
	h, pkg, artifact := installableWithLibs(t, spec.TestStep{Run: `
set -e
[ "$` + loaderVar + `" = "$NEM_PREFIX/lib:` + filepath.Join(depPrefix, "lib") + `" ]
`})
	out, err := runInstallWithDeps(t, h, []build.ResolvedDep{dep}, pkg, artifact)
	if err != nil {
		t.Fatalf("environment assertions failed: %v\n%s", err, out)
	}
}

// TestInstallAndRunLoaderPathExcludesDepsNotOnLoaderPath proves a dependency
// without OnLoaderPath contributes nothing, even though it ships libs: —
// the same rule envx.buildLoaderPath applies to a lock entry not marked
// onLoaderPath.
func TestInstallAndRunLoaderPathExcludesDepsNotOnLoaderPath(t *testing.T) {
	loaderVar := envx.LoaderPathVar()
	dep := build.ResolvedDep{
		Name: "linkonly", Version: "v1", Prefix: t.TempDir(),
		Libs: []string{"lib"},
	}
	h, pkg, artifact := installableWithLibs(t, spec.TestStep{Run: `
set -e
[ "$` + loaderVar + `" = "$NEM_PREFIX/lib" ]
`})
	out, err := runInstallWithDeps(t, h, []build.ResolvedDep{dep}, pkg, artifact)
	if err != nil {
		t.Fatalf("environment assertions failed: %v\n%s", err, out)
	}
}

// TestInstallAndRunSkipsLoaderPathWhenNothingHasLibs proves that when
// neither the package under test nor any on-loader-path dependency ships
// libs:, the loader variable is left unset entirely — not exported empty —
// exactly as when there are no deps at all.
func TestInstallAndRunSkipsLoaderPathWhenNothingHasLibs(t *testing.T) {
	loaderVar := envx.LoaderPathVar()
	dep := build.ResolvedDep{Name: "dep", Version: "v1", Prefix: t.TempDir(), OnLoaderPath: true}
	h, pkg, artifact := installable(t, spec.TestStep{Run: `
set -e
[ -z "${` + loaderVar + `+x}" ]
`})
	out, err := runInstallWithDeps(t, h, []build.ResolvedDep{dep}, pkg, artifact)
	if err != nil {
		t.Fatalf("environment assertions failed: %v\n%s", err, out)
	}
}

// TestInstallAndRunFailingStepQuotesTheAuthorsScript proves a failing step's
// error still quotes exactly what the pkg.yaml author wrote, not the
// loader-path export line InstallAndRun prefixes onto it before running it.
func TestInstallAndRunFailingStepQuotesTheAuthorsScript(t *testing.T) {
	h, pkg, artifact := installableWithLibs(t, spec.TestStep{Run: "exit 3"})
	err := runInstall(t, h, pkg, artifact)
	if err == nil {
		t.Fatal("want an error from the failing step")
	}
	if !strings.Contains(err.Error(), `"exit 3"`) {
		t.Fatalf("error must quote the author's own script, got: %v", err)
	}
	if strings.Contains(err.Error(), "export") {
		t.Fatalf("error must not quote the loader-path prologue, got: %v", err)
	}
}

// TestComposeEnvNeverSetsLoaderPath proves the loader-path invariant one
// level below sh, where SIP cannot interfere: the shell-level assertion in
// TestInstallAndRunDropsInheritedLoaderPath cannot check $DYLD_LIBRARY_PATH
// on darwin, since dyld strips DYLD_* variables from /bin/sh before the
// script itself ever starts, no matter what InstallAndRun hands it. This
// test targets ComposeEnv directly, which only needs to prove it never adds
// or rewrites either var — dropping an inherited one is InstallAndRun's job
// (via build.ScrubEnv), not ComposeEnv's, so that half of the invariant
// isn't this test's to cover.
func TestComposeEnvNeverSetsLoaderPath(t *testing.T) {
	self := build.ResolvedDep{
		Name: "tool", Version: "v1", Prefix: "/prefix",
		OnPath: true, OnLoaderPath: true, Libs: []string{"lib"},
	}
	ectx := build.EnvContext{Version: "v1", Platform: spec.Current(), Prefix: "/prefix", AbsRpath: true}

	// With no ambient loader-path var, ComposeEnv must not add one itself.
	noAmbient := build.ComposeEnv(nil, []build.ResolvedDep{self}, ectx)
	if v, ok := lookupEnv(noAmbient, "LD_LIBRARY_PATH"); ok {
		t.Fatalf("ComposeEnv must never set LD_LIBRARY_PATH, got %q", v)
	}
	if v, ok := lookupEnv(noAmbient, "DYLD_LIBRARY_PATH"); ok {
		t.Fatalf("ComposeEnv must never set DYLD_LIBRARY_PATH, got %q", v)
	}

	// With a caller-supplied loader-path var already in base, ComposeEnv
	// must neither strip it nor overwrite it with a computed rpath value —
	// it must pass through exactly as given.
	base := []string{"LD_LIBRARY_PATH=sentinel", "DYLD_LIBRARY_PATH=sentinel"}
	withAmbient := build.ComposeEnv(base, []build.ResolvedDep{self}, ectx)
	if v, ok := lookupEnv(withAmbient, "LD_LIBRARY_PATH"); !ok || v != "sentinel" {
		t.Fatalf("LD_LIBRARY_PATH must pass through unchanged, got %q, present=%v", v, ok)
	}
	if v, ok := lookupEnv(withAmbient, "DYLD_LIBRARY_PATH"); !ok || v != "sentinel" {
		t.Fatalf("DYLD_LIBRARY_PATH must pass through unchanged, got %q, present=%v", v, ok)
	}
}

// lookupEnv finds key's value among "key=value" entries, matching on the
// key itself rather than a substring of the joined slice.
func lookupEnv(env []string, key string) (string, bool) {
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			return v, true
		}
	}
	return "", false
}
