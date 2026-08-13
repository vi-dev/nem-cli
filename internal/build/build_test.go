package build

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/vi-dev/nem-cli/internal/home"
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
			Steps: []struct{ Run string }{
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
			Output: "out", Steps: []struct{ Run string }{{Run: "exit 3"}},
		}}
	pkg.Build.Source.URL = srv.URL
	var b bytes.Buffer
	_, err := Build(context.Background(), h, nil, nil, pkg, Options{Version: "v1"}, report.New(&b, &b, report.Options{}), &b, &b)
	if err == nil {
		t.Fatal("want error when a build step exits non-zero")
	}
}
