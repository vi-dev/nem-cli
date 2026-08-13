package install_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vi-dev/nem-cli/internal/home"
	"github.com/vi-dev/nem-cli/internal/install"
	"github.com/vi-dev/nem-cli/internal/spec"
)

func testHome(t *testing.T) home.Home {
	t.Helper()
	dir := t.TempDir()
	return home.Resolve(func(k string) string {
		if k == "NEM_HOME" {
			return dir
		}
		return ""
	})
}

// fixtureYAML is a realistic pkg.yaml: an oci artifact, one extract action,
// and an env export using the {{.InstallDir}} template that Meta must keep
// raw rather than expand.
const fixtureYAML = `
schema: 2
name: tool
artifact:
  oci: ":{{.Version}}"
install:
  - extract: {}
bins: ["bin"]
env:
  - name: TOOL_HOME
    value: "{{.InstallDir}}"
versions:
  - v1.0.0
`

func fixturePkg(t *testing.T) *spec.Package {
	t.Helper()
	pkg, err := spec.Parse([]byte(fixtureYAML))
	if err != nil {
		t.Fatalf("parse fixture pkg.yaml: %v", err)
	}
	return pkg
}

// fixtureArtifact builds a tar.gz artifact containing bin/tool and writes it
// under dir, returning its path.
func fixtureArtifact(t *testing.T, dir string) string {
	t.Helper()
	archive := gzipBytes(t, buildTar(t, []tarEntry{
		{name: "bin/tool", content: []byte("binary"), mode: 0o755},
	}))
	return writeArtifact(t, dir, archive)
}

func TestInstallFullRoundTrip(t *testing.T) {
	h := testHome(t)
	pkg := fixturePkg(t)
	artifact := fixtureArtifact(t, t.TempDir())

	start := time.Now()
	if err := install.Install(context.Background(), h, pkg, "v1.0.0", "official", artifact); err != nil {
		t.Fatalf("Install: %v", err)
	}
	end := time.Now()

	installDir, err := h.PackageDir(pkg.Name, "v1.0.0")
	if err != nil {
		t.Fatalf("PackageDir: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(installDir, "bin", "tool"))
	if err != nil || string(got) != "binary" {
		t.Fatalf("bin/tool = %q, %v, want %q", got, err, "binary")
	}
	info, err := os.Stat(filepath.Join(installDir, "bin", "tool"))
	if err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("bin/tool mode = %v, %v, want 0755", info, err)
	}

	if !install.IsInstalled(h, pkg.Name, "v1.0.0") {
		t.Fatal("IsInstalled = false, want true")
	}

	meta, err := install.ReadMeta(h, pkg.Name, "v1.0.0")
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if meta.Package != "tool" || meta.Version != "v1.0.0" || meta.Catalog != "official" {
		t.Fatalf("meta identity: %+v", meta)
	}
	if len(meta.Bins) != 1 || meta.Bins[0] != "bin" {
		t.Fatalf("meta.Bins = %v, want [bin]", meta.Bins)
	}
	if len(meta.Env) != 1 || meta.Env[0].Name != "TOOL_HOME" || meta.Env[0].Value != "{{.InstallDir}}" {
		t.Fatalf("meta.Env = %+v, want raw {{.InstallDir}} template untouched", meta.Env)
	}
	if meta.InstalledAt.Before(start.Add(-time.Second)) || meta.InstalledAt.After(end.Add(time.Second)) {
		t.Fatalf("meta.InstalledAt = %v, want within [%v, %v]", meta.InstalledAt, start, end)
	}
}

func TestInstallSecondSameVersionAlreadyExistsFastPath(t *testing.T) {
	h := testHome(t)
	pkg := fixturePkg(t)
	tmp := t.TempDir()

	if err := install.Install(context.Background(), h, pkg, "v1.0.0", "official", fixtureArtifact(t, tmp)); err != nil {
		t.Fatalf("first Install: %v", err)
	}

	err := install.Install(context.Background(), h, pkg, "v1.0.0", "official", fixtureArtifact(t, tmp))
	want := "commit tool@v1.0.0: install dir already exists"
	if err == nil || err.Error() != want {
		t.Fatalf("second Install error = %v, want %q", err, want)
	}

	installDir, _ := h.PackageDir(pkg.Name, "v1.0.0")
	if _, statErr := os.Stat(filepath.Join(installDir, "bin", "tool")); statErr != nil {
		t.Fatalf("original install disturbed: %v", statErr)
	}

	assertNoStrayStaging(t, installDir)
}

func TestInstallActionsFailureLeavesNoInstallDirNoStrayStaging(t *testing.T) {
	h := testHome(t)
	pkg := fixturePkg(t)
	tmp := t.TempDir()
	badArtifact := writeArtifact(t, tmp, []byte("not an archive at all"))

	err := install.Install(context.Background(), h, pkg, "v1.0.0", "official", badArtifact)
	if err == nil || !strings.Contains(err.Error(), "unrecognized archive format") {
		t.Fatalf("Install error = %v, want unrecognized-format error", err)
	}

	installDir, _ := h.PackageDir(pkg.Name, "v1.0.0")
	if _, statErr := os.Stat(installDir); !os.IsNotExist(statErr) {
		t.Fatalf("install dir should not exist, stat err = %v", statErr)
	}
	if install.IsInstalled(h, pkg.Name, "v1.0.0") {
		t.Fatal("IsInstalled = true after failed install")
	}

	assertNoStrayStaging(t, installDir)
}

// assertNoStrayStaging globs installDir's parent for any leftover
// "<version>-<rand>.tmp" staging directory.
func assertNoStrayStaging(t *testing.T, installDir string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(installDir), "*-*.tmp"))
	if err != nil {
		t.Fatalf("glob staging leftovers: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("stray staging dirs left behind: %v", matches)
	}
}

func TestInstallRecordsLibsInMeta(t *testing.T) {
	h := testHome(t)
	pkg := fixturePkg(t)
	pkg.Libs = []string{"lib"}

	if err := install.Install(context.Background(), h, pkg, "v1.0.0", "official", fixtureArtifact(t, t.TempDir())); err != nil {
		t.Fatalf("Install: %v", err)
	}

	meta, err := install.ReadMeta(h, pkg.Name, "v1.0.0")
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if len(meta.Libs) != 1 || meta.Libs[0] != "lib" {
		t.Fatalf("meta.Libs = %v, want [lib]", meta.Libs)
	}
}

func TestIsInstalledFalseWhenAbsent(t *testing.T) {
	h := testHome(t)
	if install.IsInstalled(h, "tool", "v1.0.0") {
		t.Fatal("IsInstalled = true, want false")
	}
}

func TestReadMetaErrorWhenNotInstalled(t *testing.T) {
	h := testHome(t)
	if _, err := install.ReadMeta(h, "tool", "v1.0.0"); err == nil {
		t.Fatal("ReadMeta: expected error for uninstalled package")
	}
}

// TestInstallConcurrentLostRaceCommitError exercises both places that must
// produce the identical "already exists" error: the fast pre-staging check
// and the os.Rename-onto-existing-target commit failure. Two goroutines
// race to install the same name@version; exactly one must win, and the
// loser's error text must match regardless of which of the two checks
// caught it.
func TestInstallConcurrentLostRaceCommitError(t *testing.T) {
	h := testHome(t)
	pkg := fixturePkg(t)
	tmp := t.TempDir()

	const n = 2
	errs := make([]error, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := range n {
		artifact := fixtureArtifact(t, filepath.Join(tmp, fmt.Sprint(i)))
		go func(i int, artifact string) {
			defer wg.Done()
			errs[i] = install.Install(context.Background(), h, pkg, "v1.0.0", "official", artifact)
		}(i, artifact)
	}
	wg.Wait()

	successes, failures := 0, 0
	want := "commit tool@v1.0.0: install dir already exists"
	for _, err := range errs {
		switch {
		case err == nil:
			successes++
		case err.Error() == want:
			failures++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if successes != 1 || failures != n-1 {
		t.Fatalf("successes=%d failures=%d, want 1 and %d (errs=%v)", successes, failures, n-1, errs)
	}

	installDir, _ := h.PackageDir(pkg.Name, "v1.0.0")
	assertNoStrayStaging(t, installDir)
}
