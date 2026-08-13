package build

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"

	"github.com/vi-dev/nem-cli/internal/catalog"
	"github.com/vi-dev/nem-cli/internal/fetch"
	"github.com/vi-dev/nem-cli/internal/home"
	"github.com/vi-dev/nem-cli/internal/install"
	"github.com/vi-dev/nem-cli/internal/report"
	"github.com/vi-dev/nem-cli/internal/resolve"
	"github.com/vi-dev/nem-cli/internal/spec"
)

// Options tailors one Build invocation.
type Options struct{ Version, Output, SourceSha256 string }

// Result is what a successful Build produced.
type Result struct {
	OutputDir      string
	SourceSha256   string
	SourceVerified bool
}

// Build runs pkg's build recipe end to end: fetch and unpack its source,
// resolve and install its build deps, run its steps with the composed
// build env, then verify the harvested output tree is conformant.
func Build(ctx context.Context, h home.Home, cfg *catalog.Config, sources []catalog.Named, pkg *spec.Package,
	opts Options, rep report.Reporter, stdout, stderr io.Writer) (Result, error) {
	if pkg.Build == nil {
		return Result{}, errors.New("package has no build section")
	}
	if err := pkg.Validate(); err != nil {
		return Result{}, err
	}

	version := opts.Version
	if version == "" {
		version = pkg.Versions[0].Version
	}

	if err := os.MkdirAll(h.Tmp(), 0o755); err != nil {
		return Result{}, fmt.Errorf("create tmp dir: %w", err)
	}
	staging, err := os.MkdirTemp(h.Tmp(), pkg.Name+"-build-*")
	if err != nil {
		return Result{}, fmt.Errorf("create staging dir: %w", err)
	}

	path, sha, verified, err := fetchBuildSource(ctx, pkg, version, opts.SourceSha256, staging)
	if err != nil {
		return Result{}, err
	}
	if !verified {
		rep.Info("record for reproducibility: sourceSha256: %s", sha)
	}

	srcDir := filepath.Join(staging, "src")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		return Result{}, fmt.Errorf("create source dir: %w", err)
	}
	srcRoot, err := unpackSource(path, srcDir)
	if err != nil {
		return Result{}, err
	}

	deps, err := resolveBuildDeps(ctx, h, cfg, sources, rep, pkg)
	if err != nil {
		return Result{}, err
	}

	prefix, err := h.PackageDir(pkg.Name, version)
	if err != nil {
		return Result{}, fmt.Errorf("package dir for %s@%s: %w", pkg.Name, version, err)
	}
	outputDir := filepath.Join(srcRoot, pkg.Build.Output)
	env := composeBuildEnv(os.Environ(), deps, buildContext{
		Version: version, Platform: spec.Current(), Prefix: prefix, StagingDir: staging, OutputDir: outputDir,
	})

	for i, step := range pkg.Build.Steps {
		c := exec.CommandContext(ctx, "sh", "-c", step.Run)
		c.Dir = srcRoot
		c.Env = env
		c.Stdout = stdout
		c.Stderr = stderr
		if err := c.Run(); err != nil {
			return Result{}, fmt.Errorf("build step %d (%q): %w", i+1, step.Run, err)
		}
	}

	final, err := harvest(outputDir, opts.Output)
	if err != nil {
		return Result{}, err
	}

	packagesRoot := filepath.Join(h.Root(), "packages")
	vs, err := VerifyConformance(final, []string{staging, packagesRoot})
	if err != nil {
		return Result{}, fmt.Errorf("verify %s: %w", final, err)
	}
	if len(vs) > 0 {
		return Result{}, conformanceError(vs)
	}

	return Result{OutputDir: final, SourceSha256: sha, SourceVerified: verified}, nil
}

// fetchBuildSource resolves the source sha256 policy (an explicit override,
// else the version entry's pin, else trust-on-first-use) and downloads
// pkg's build source for version accordingly.
func fetchBuildSource(ctx context.Context, pkg *spec.Package, version, shaOverride, staging string) (path, sha string, verified bool, err error) {
	want := shaOverride
	if want == "" {
		for _, v := range pkg.Versions {
			if v.Version == version {
				want = v.SourceSha256
				break
			}
		}
	}
	url, err := pkg.BuildSourceURL(version, spec.Current())
	if err != nil {
		return "", "", false, err
	}
	return fetchSource(ctx, http.DefaultClient, url, want, staging, fetch.Meta{Name: pkg.Name, Version: version, Platform: spec.Current()})
}

// resolveBuildDeps resolves pkg's build.deps as dependency edges of pkg (the
// same edge walk and role assignment a package's runtime deps get), installs
// them, then reads back each current-platform lock entry's install metadata
// to build the deps composeBuildEnv needs.
func resolveBuildDeps(ctx context.Context, h home.Home, cfg *catalog.Config, sources []catalog.Named, rep report.Reporter, pkg *spec.Package) ([]resolvedDep, error) {
	if len(pkg.Build.Deps) == 0 {
		return nil, nil
	}
	result, err := resolve.ResolveBuild(ctx, pkg, sources)
	if err != nil {
		return nil, err
	}
	if err := install.Run(ctx, h, rep, currentPlatformJobs(cfg, result)); err != nil {
		return nil, err
	}
	current := spec.Current().String()
	var deps []resolvedDep
	for _, e := range result.Entries {
		if !slices.Contains(e.Platforms, current) {
			continue
		}
		prefix, err := h.PackageDir(e.Name, e.Version)
		if err != nil {
			return nil, fmt.Errorf("package dir for %s@%s: %w", e.Name, e.Version, err)
		}
		meta, err := install.ReadMeta(h, e.Name, e.Version)
		if err != nil {
			return nil, err
		}
		deps = append(deps, resolvedDep{
			Name: e.Name, Version: e.Version, Prefix: prefix,
			OnPath: e.OnPath, OnLoaderPath: e.OnLoaderPath,
			Bins: meta.Bins, Libs: meta.Libs,
		})
	}
	return deps, nil
}

// currentPlatformJobs builds install jobs for the entries the running
// platform needs, pairing each with the oci ref of the catalog it came
// from (empty for a dir catalog, or when cfg is nil).
func currentPlatformJobs(cfg *catalog.Config, result *resolve.Result) []install.Job {
	current := spec.Current().String()
	var jobs []install.Job
	for _, entry := range result.Entries {
		if !slices.Contains(entry.Platforms, current) {
			continue
		}
		ref := ""
		if cfg != nil {
			if e := cfg.Find(entry.Catalog); e != nil && e.Type == "oci" {
				ref = e.Ref
			}
		}
		jobs = append(jobs, install.Job{
			Pkg:     result.Pkgs[entry.Name],
			Version: entry.Version,
			Catalog: entry.Catalog,
			Source:  fetch.Source{CatalogRef: ref},
		})
	}
	return jobs
}

// harvest places the built outputDir at its final location: moved to
// output when set, else left where the build produced it.
func harvest(outputDir, output string) (string, error) {
	if output == "" {
		return outputDir, nil
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return "", fmt.Errorf("create output parent dir: %w", err)
	}
	if err := os.Rename(outputDir, output); err != nil {
		return "", fmt.Errorf("move build output to %s: %w", output, err)
	}
	return output, nil
}

func conformanceError(vs []Violation) error {
	msg := fmt.Sprintf("%d conformance violation(s):", len(vs))
	for _, v := range vs {
		msg += fmt.Sprintf("\n  %s: %s (%s)", v.File, v.Ref, v.Reason)
	}
	return errors.New(msg)
}
