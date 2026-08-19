package build

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/oci"

	"github.com/vi-dev/nem-cli/internal/home"
	"github.com/vi-dev/nem-cli/internal/install"
	"github.com/vi-dev/nem-cli/internal/ocix"
	"github.com/vi-dev/nem-cli/internal/report"
	"github.com/vi-dev/nem-cli/internal/spec"
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
