package build

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/oci"

	"github.com/vi-dev/nem-cli/internal/catalog"
	"github.com/vi-dev/nem-cli/internal/home"
	"github.com/vi-dev/nem-cli/internal/install"
	"github.com/vi-dev/nem-cli/internal/ocix"
	"github.com/vi-dev/nem-cli/internal/report"
	"github.com/vi-dev/nem-cli/internal/spec"
	"github.com/vi-dev/nem-cli/internal/usage"
)

func TestBuildRunsStepsAndVerifies(t *testing.T) {
	nemHomeDir := t.TempDir()
	h := home.Resolve(func(k string) string {
		if k == "NEM_HOME" {
			return nemHomeDir
		}
		return ""
	})
	// A source tarball with nothing but a marker file.
	tgz := makeTarGz(t, map[string]string{"src/README": "hi"})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(tgz) }))
	defer srv.Close()

	pkg := &spec.Package{Schema: 2, Name: "tool",
		Artifact: spec.Artifact{OCI: ":{{.Version}}"},
		Install:  []spec.Action{{Extract: &spec.ExtractAction{}}},
		Versions: []spec.VersionEntry{{Version: "v1.0.0"}},
		Build: &spec.Build{
			Output: "out",
			Steps: []spec.BuildStep{
				{Run: "mkdir -p \"$NEM_OUTPUT/bin\" && echo \"$NEM_VERSION\" > \"$NEM_OUTPUT/bin/ver\""},
			},
		}}
	pkg.Build.Source.URL = srv.URL

	var out, errb bytes.Buffer
	res, err := Build(context.Background(), h, nil, nil, pkg, Options{Version: "v1.0.0"},
		report.New(&out, &errb, report.Options{}), &out, &errb)
	if err != nil {
		t.Fatalf("Build: %v\n%s", err, errb.String())
	}
	got, _ := os.ReadFile(filepath.Join(res.OutputDir, "bin", "ver"))
	if string(got) != "v1.0.0\n" {
		t.Fatalf("step did not run against NEM_* env; ver=%q", got)
	}
	if res.SourceVerified {
		t.Fatal("no sourceSha256 pinned → SourceVerified must be false (TOFU)")
	}
	if res.SourceSha256 == "" {
		t.Fatal("TOFU must report the computed sourceSha256")
	}
}

func TestBuildFailsOnStepError(t *testing.T) {
	nemHomeDir := t.TempDir()
	h := home.Resolve(func(k string) string { return map[string]string{"NEM_HOME": nemHomeDir}[k] })
	tgz := makeTarGz(t, map[string]string{"src/x": "y"})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(tgz) }))
	defer srv.Close()
	pkg := &spec.Package{Schema: 2, Name: "tool",
		Artifact: spec.Artifact{OCI: ":{{.Version}}"},
		Install:  []spec.Action{{Extract: &spec.ExtractAction{}}},
		Versions: []spec.VersionEntry{{Version: "v1"}},
		Build: &spec.Build{
			Output: "out", Steps: []spec.BuildStep{{Run: "exit 3"}},
		}}
	pkg.Build.Source.URL = srv.URL
	var b bytes.Buffer
	_, err := Build(context.Background(), h, nil, nil, pkg, Options{Version: "v1"}, report.New(&b, &b, report.Options{}), &b, &b)
	if err == nil {
		t.Fatal("want error when a build step exits non-zero")
	}
}

func TestBuildSkipsNonMatchingPlatformSteps(t *testing.T) {
	nemHomeDir := t.TempDir()
	h := home.Resolve(func(k string) string { return map[string]string{"NEM_HOME": nemHomeDir}[k] })
	tgz := makeTarGz(t, map[string]string{"src/x": "y"})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(tgz) }))
	defer srv.Close()

	other := spec.Platform{OS: "linux"}
	if spec.Current().OS == "linux" {
		other = spec.Platform{OS: "darwin"}
	}
	pkg := &spec.Package{Schema: 2, Name: "tool",
		Artifact: spec.Artifact{OCI: ":{{.Version}}"},
		Install:  []spec.Action{{Extract: &spec.ExtractAction{}}},
		Versions: []spec.VersionEntry{{Version: "v1"}},
		Build: &spec.Build{Output: "out", Steps: []spec.BuildStep{
			{Run: "exit 7", Platforms: []spec.Platform{other}},
			{Run: `mkdir -p "$NEM_OUTPUT" && echo ran > "$NEM_OUTPUT/marker"`,
				Platforms: []spec.Platform{{OS: spec.Current().OS}}},
		}}}
	pkg.Build.Source.URL = srv.URL

	var b bytes.Buffer
	res, err := Build(context.Background(), h, nil, nil, pkg, Options{Version: "v1"}, report.New(&b, &b, report.Options{}), &b, &b)
	if err != nil {
		t.Fatalf("Build: %v\n%s", err, b.String())
	}
	got, _ := os.ReadFile(filepath.Join(res.OutputDir, "marker"))
	if string(got) != "ran\n" {
		t.Fatalf("matching step did not run; marker=%q", got)
	}
}

func TestBuildFailsWhenAllStepsFiltered(t *testing.T) {
	nemHomeDir := t.TempDir()
	h := home.Resolve(func(k string) string { return map[string]string{"NEM_HOME": nemHomeDir}[k] })
	tgz := makeTarGz(t, map[string]string{"src/x": "y"})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(tgz) }))
	defer srv.Close()

	other := spec.Platform{OS: "linux"}
	if spec.Current().OS == "linux" {
		other = spec.Platform{OS: "darwin"}
	}
	pkg := &spec.Package{Schema: 2, Name: "tool",
		Artifact: spec.Artifact{OCI: ":{{.Version}}"},
		Install:  []spec.Action{{Extract: &spec.ExtractAction{}}},
		Versions: []spec.VersionEntry{{Version: "v1"}},
		Build: &spec.Build{Output: "out", Steps: []spec.BuildStep{
			{Run: "exit 7", Platforms: []spec.Platform{other}},
		}}}
	pkg.Build.Source.URL = srv.URL

	var b bytes.Buffer
	_, err := Build(context.Background(), h, nil, nil, pkg, Options{Version: "v1"}, report.New(&b, &b, report.Options{}), &b, &b)
	if err == nil || !strings.Contains(err.Error(), "no build step applies to "+spec.Current().String()) {
		t.Fatalf("want no-applicable-step error naming the platform, got %v", err)
	}
}

func TestBuildPushRoundTripsThroughArchive(t *testing.T) {
	nemHomeDir := t.TempDir()
	h := home.Resolve(func(k string) string {
		if k == "NEM_HOME" {
			return nemHomeDir
		}
		return ""
	})
	store, err := oci.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	restore := archivesOpener
	archivesOpener = func(catalogRef, name string) (oras.Target, error) { return store, nil }
	defer func() { archivesOpener = restore }()

	tgz := makeTarGz(t, map[string]string{"src/README": "hi"})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(tgz) }))
	defer srv.Close()

	pkg := &spec.Package{Schema: 2, Name: "tool",
		Artifact: spec.Artifact{OCI: ":{{.Version}}"},
		Install:  []spec.Action{{Extract: &spec.ExtractAction{}}},
		Versions: []spec.VersionEntry{{Version: "v1.0.0"}},
		Build: &spec.Build{Output: "out", Steps: []spec.BuildStep{
			{Run: `mkdir -p "$NEM_OUTPUT/bin" && echo hello > "$NEM_OUTPUT/bin/tool"`},
		}}}
	pkg.Build.Source.URL = srv.URL

	var b bytes.Buffer
	res, err := Build(context.Background(), h, nil, nil, pkg,
		Options{Version: "v1.0.0", Push: "ghcr.io/x/cat:v2"},
		report.New(&b, &b, report.Options{}), &b, &b)
	if err != nil {
		t.Fatalf("build --push: %v\n%s", err, b.String())
	}
	if !res.Pushed {
		t.Fatal("Result.Pushed should be true")
	}

	// consumer read path: pull the pushed archive and install it
	pulled, err := ocix.PullArchiveFrom(context.Background(), store, "v1.0.0", spec.Current(), t.TempDir())
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	if err := install.Install(context.Background(), h, pkg, "v1.0.0", "cat", pulled); err != nil {
		t.Fatalf("install pulled archive: %v", err)
	}
	dir, _ := h.PackageDir("tool", "v1.0.0")
	if got, _ := os.ReadFile(filepath.Join(dir, "bin", "tool")); string(got) != "hello\n" {
		t.Fatalf("installed tool = %q, want hello", got)
	}
}

func TestBuildDryRunPushesNothing(t *testing.T) {
	nemHomeDir := t.TempDir()
	h := home.Resolve(func(k string) string {
		if k == "NEM_HOME" {
			return nemHomeDir
		}
		return ""
	})
	store, err := oci.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	restore := archivesOpener
	archivesOpener = func(catalogRef, name string) (oras.Target, error) { return store, nil }
	defer func() { archivesOpener = restore }()

	tgz := makeTarGz(t, map[string]string{"src/README": "hi"})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(tgz) }))
	defer srv.Close()

	pkg := &spec.Package{Schema: 2, Name: "tool",
		Artifact: spec.Artifact{OCI: ":{{.Version}}"},
		Install:  []spec.Action{{Extract: &spec.ExtractAction{}}},
		Versions: []spec.VersionEntry{{Version: "v1.0.0"}},
		Build: &spec.Build{Output: "out", Steps: []spec.BuildStep{
			{Run: `mkdir -p "$NEM_OUTPUT/bin" && echo hello > "$NEM_OUTPUT/bin/tool"`},
		}}}
	pkg.Build.Source.URL = srv.URL

	var b bytes.Buffer
	res, err := Build(context.Background(), h, nil, nil, pkg,
		Options{Version: "v1.0.0", Push: "ghcr.io/x/cat:v2", DryRun: true},
		report.New(&b, &b, report.Options{}), &b, &b)
	if err != nil {
		t.Fatalf("build --push --dry-run: %v\n%s", err, b.String())
	}
	if res.Pushed {
		t.Fatal("Result.Pushed should be false on dry-run")
	}

	if _, err := ocix.PullArchiveFrom(context.Background(), store, "v1.0.0", spec.Current(), t.TempDir()); !errors.Is(err, ocix.ErrArchiveNotFound) {
		t.Fatalf("dry-run pushed something: pull err = %v, want %v", err, ocix.ErrArchiveNotFound)
	}
}

// depDirCatalog writes a one-package dir catalog whose sole package
// downloads depArchive from an httptest server, its sha256 pinned for
// every supported platform so spec.Validate accepts it.
func depDirCatalog(t *testing.T, name, version string, depArchive []byte) string {
	t.Helper()
	sum := sha256.Sum256(depArchive)
	sha := hex.EncodeToString(sum[:])
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(depArchive) }))
	t.Cleanup(srv.Close)

	root := t.TempDir()
	dir := filepath.Join(root, "pkgs", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	yaml := `
schema: 2
name: ` + name + `
artifact:
  url: "` + srv.URL + `"
install:
  - extract: {}
libs: ["lib"]
versions:
  - version: "` + version + `"
    sha256:
      darwin/arm64: "` + sha + `"
      darwin/amd64: "` + sha + `"
      linux/arm64: "` + sha + `"
      linux/amd64: "` + sha + `"
`
	if err := os.WriteFile(filepath.Join(dir, "pkg.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatalf("write dep pkg.yaml: %v", err)
	}
	return root
}

// TestBuildRestampsAlreadyInstalledBuildDep guards the case a same-key
// assertion right after a fresh install can't distinguish: install.Install
// already stamps a build dep the first time install.Run installs it, so a
// naive "the dep's key is present after Build" check would pass even if
// Build itself never stamped deps. Here the dep is pre-installed and its
// stamp backdated past Stamp's debounce window before Build runs, so only
// Build's own stamp — not install's — can explain the refreshed timestamp.
func TestBuildRestampsAlreadyInstalledBuildDep(t *testing.T) {
	nemHomeDir := t.TempDir()
	h := home.Resolve(func(k string) string {
		if k == "NEM_HOME" {
			return nemHomeDir
		}
		return ""
	})

	depArchive := makeTarGz(t, map[string]string{"lib/libdep.so": "dep bytes"})
	catalogRoot := depDirCatalog(t, "dep", "9.9.9", depArchive)
	sources := []catalog.Named{{Name: "cat", Source: catalog.NewDir(catalogRoot)}}

	depPkg, _, err := catalog.NewDir(catalogRoot).Load(context.Background(), "dep")
	if err != nil {
		t.Fatalf("load dep pkg: %v", err)
	}
	artifactPath := filepath.Join(t.TempDir(), "dep.tar.gz")
	if err := os.WriteFile(artifactPath, depArchive, 0o644); err != nil {
		t.Fatalf("write dep artifact: %v", err)
	}
	if err := install.Install(context.Background(), h, depPkg, "9.9.9", "cat", artifactPath); err != nil {
		t.Fatalf("pre-install dep: %v", err)
	}

	old := time.Now().Add(-2 * time.Hour)
	idx := usage.Load(h)
	idx[usage.Key("dep", "9.9.9")] = old
	if err := usage.Save(h, idx); err != nil {
		t.Fatalf("backdate dep stamp: %v", err)
	}

	srcTgz := makeTarGz(t, map[string]string{"src/README": "hi"})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(srcTgz) }))
	defer srv.Close()

	pkg := &spec.Package{Schema: 2, Name: "consumer",
		Artifact: spec.Artifact{OCI: ":{{.Version}}"},
		Install:  []spec.Action{{Extract: &spec.ExtractAction{}}},
		Versions: []spec.VersionEntry{{Version: "v1.0.0"}},
		Build: &spec.Build{
			Output: "out",
			Deps:   []spec.Dep{{Name: "dep", Version: "9.9.9"}},
			Steps: []spec.BuildStep{
				{Run: `mkdir -p "$NEM_OUTPUT/bin" && echo hi > "$NEM_OUTPUT/bin/consumer"`},
			},
		}}
	pkg.Build.Source.URL = srv.URL

	var out, errb bytes.Buffer
	if _, err := Build(context.Background(), h, nil, sources, pkg, Options{Version: "v1.0.0"},
		report.New(&out, &errb, report.Options{}), &out, &errb); err != nil {
		t.Fatalf("Build: %v\n%s", err, errb.String())
	}

	got := usage.Load(h)
	if _, ok := got.LastUsed("consumer", "v1.0.0"); !ok {
		t.Error("built package was not stamped")
	}
	depStamp, ok := got.LastUsed("dep", "9.9.9")
	if !ok {
		t.Fatal("dep stamp missing after build")
	}
	if !depStamp.After(old) {
		t.Fatalf("build did not refresh an already-installed dep's stamp: got %v, want after %v", depStamp, old)
	}
}
