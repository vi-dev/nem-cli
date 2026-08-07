// Package publish implements catalog-level validation and publishing for
// nem package catalogs.
package publish

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/vi-dev/nem-cli/internal/envx"
	"github.com/vi-dev/nem-cli/internal/spec"
)

// Finding is one problem found while linting a catalog. Pkg identifies the
// offending package by its containing directory name when linting a
// catalog directory, and is empty when linting a single manifest file or
// for catalog-level findings not tied to any single package.
type Finding struct {
	Pkg string
	Msg string
}

// String renders the finding as "<pkg>: <msg>", or just "<msg>" when Pkg
// is empty.
func (f Finding) String() string {
	if f.Pkg == "" {
		return f.Msg
	}
	return f.Pkg + ": " + f.Msg
}

// Lint validates every package manifest under dir and returns every
// problem found; it never stops at the first one. dir is either a catalog
// directory laid out as pkgs/<name>/pkg.yaml, or a path to a single
// pkg.yaml file. The returned error reports only I/O failures reading dir
// itself — validation problems are always Findings, never errors.
func Lint(dir string) ([]Finding, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", dir, err)
	}

	if !info.IsDir() {
		findings := lintPackage("", dir, false)
		sortFindings(findings)
		return findings, nil
	}

	pkgsDir := filepath.Join(dir, "pkgs")
	entries, err := os.ReadDir(pkgsDir)
	if os.IsNotExist(err) {
		return []Finding{{Msg: "no pkgs directory found"}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", pkgsDir, err)
	}

	var findings []Finding
	packages := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		packages++
		path := filepath.Join(pkgsDir, e.Name(), "pkg.yaml")
		findings = append(findings, lintPackage(e.Name(), path, true)...)
	}
	if packages == 0 {
		return []Finding{{Msg: "pkgs directory contains no packages"}}, nil
	}

	sortFindings(findings)
	return findings, nil
}

// lintPackage validates one manifest file. id is the containing directory
// name in catalog-walk mode (empty in single-file mode) and labels every
// finding produced for this package, since the directory is the
// package's authoritative identity in a directory catalog. When
// checkDirName is set, a non-empty declared name that differs from id
// produces a name-mismatch finding; an empty declared name is left to
// Validate, which already reports it as an invalid name.
func lintPackage(id, path string, checkDirName bool) []Finding {
	data, err := os.ReadFile(path)
	if err != nil {
		return []Finding{{Pkg: id, Msg: fmt.Sprintf("read %s: %v", path, err)}}
	}
	pkg, err := spec.Parse(data)
	if err != nil {
		return []Finding{{Pkg: id, Msg: err.Error()}}
	}

	var findings []Finding
	if err := pkg.Validate(); err != nil {
		findings = append(findings, Finding{Pkg: id, Msg: err.Error()})
	}
	if checkDirName && pkg.Name != "" && pkg.Name != id {
		findings = append(findings, Finding{
			Pkg: id,
			Msg: fmt.Sprintf("name %q does not match its directory %q", pkg.Name, id),
		})
	}
	findings = append(findings, lintTemplates(id, pkg)...)
	findings = append(findings, lintReservedEnv(id, pkg)...)
	return findings
}

// lintTemplates renders the artifact URL template — and, for
// github-fetched packages, the asset name template — across every
// declared version and every platform the package supports, reporting
// any render failure.
func lintTemplates(id string, pkg *spec.Package) []Finding {
	var findings []Finding
	for _, v := range pkg.Versions {
		for _, plat := range pkg.SupportedBy() {
			if _, err := pkg.ArtifactURL(v.Version, plat); err != nil {
				findings = append(findings, Finding{
					Pkg: id,
					Msg: fmt.Sprintf("artifact template for %s on %s: %v", v.Version, plat, err),
				})
			}
			if pkg.Artifact.GitHub != nil {
				if _, err := pkg.AssetName(v.Version, plat); err != nil {
					findings = append(findings, Finding{
						Pkg: id,
						Msg: fmt.Sprintf("asset template for %s on %s: %v", v.Version, plat, err),
					})
				}
			}
		}
	}
	return findings
}

// lintReservedEnv reports every env export whose name collides with a
// reserved environment variable.
func lintReservedEnv(id string, pkg *spec.Package) []Finding {
	var findings []Finding
	for _, e := range pkg.Env {
		if envx.IsReserved(e.Name) {
			findings = append(findings, Finding{
				Pkg: id,
				Msg: fmt.Sprintf("env %q is a reserved variable name", e.Name),
			})
		}
	}
	return findings
}

func sortFindings(findings []Finding) {
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Pkg != findings[j].Pkg {
			return findings[i].Pkg < findings[j].Pkg
		}
		return findings[i].Msg < findings[j].Msg
	})
}
