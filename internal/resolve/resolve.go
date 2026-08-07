// Package resolve computes the dependency closure of a manifest's tools
// across nem's platform matrix and turns it into nem.lock entries.
package resolve

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"golang.org/x/mod/semver"

	"github.com/vi-dev/nem-cli/internal/catalog"
	"github.com/vi-dev/nem-cli/internal/project"
	"github.com/vi-dev/nem-cli/internal/spec"
)

// Tool is one entry from a manifest's [tools] table: the (optionally
// catalog-pinned) package key and an exact version, or "" for latest.
type Tool struct {
	Key     project.ToolKey
	Version string
}

// Result is a full resolution: lock entries covering all platforms, plus
// the package data behind each entry's chosen version, for install.
type Result struct {
	Entries []project.LockEntry
	Pkgs    map[string]*spec.Package
}

// UnsupportedPlatformError reports a directly-named tool whose package
// supports none of nem's platform matrix.
type UnsupportedPlatformError struct{ Name, Version string }

func (e *UnsupportedPlatformError) Error() string {
	return fmt.Sprintf("package %s@%s supports none of nem's platforms", e.Name, e.Version)
}

// acc is one package's reconciled state across the whole resolution: the
// currently-winning version and its source, plus the union of platforms
// that need it.
type acc struct {
	version   string
	catalog   string
	digest    string
	pkg       *spec.Package
	platforms map[spec.Platform]bool
}

// root is a direct tool's resolved starting point for the per-platform DFS.
type root struct {
	name, version, catalog, digest string
	pkg                            *spec.Package
}

// Resolve computes the dependency closure of tools across spec.Supported,
// reconciling one version per package name (highest wins), and returns
// nem.lock entries plus the package data behind each.
func Resolve(ctx context.Context, tools []Tool, sources []catalog.Named) (*Result, error) {
	directNames := make(map[string]bool, len(tools))
	roots := make([]root, len(tools))
	for i, t := range tools {
		pkg, catName, dig, err := catalog.Lookup(ctx, sources, t.Key)
		if err != nil {
			return nil, err
		}
		version, err := resolveVersion(pkg, t.Key.Name, t.Version, catName)
		if err != nil {
			return nil, err
		}
		if len(pkg.SupportedBy()) == 0 {
			return nil, &UnsupportedPlatformError{Name: t.Key.Name, Version: version}
		}
		directNames[t.Key.Name] = true
		roots[i] = root{name: t.Key.Name, version: version, catalog: catName, digest: dig, pkg: pkg}
	}

	accs := map[string]*acc{}
	for _, platform := range spec.Supported {
		visited := map[string]bool{}
		for _, r := range roots {
			if !supports(r.pkg, platform) {
				continue
			}
			if err := walk(ctx, sources, r.name, r.version, r.catalog, r.digest, r.pkg, platform, visited, accs); err != nil {
				return nil, err
			}
		}
	}

	entries := make([]project.LockEntry, 0, len(accs))
	pkgs := make(map[string]*spec.Package, len(accs))
	for name, a := range accs {
		platforms := make([]string, 0, len(a.platforms))
		for _, p := range spec.Supported {
			if a.platforms[p] {
				platforms = append(platforms, p.String())
			}
		}
		entries = append(entries, project.LockEntry{
			Name:      name,
			Version:   a.version,
			Catalog:   a.catalog,
			Direct:    directNames[name],
			Platforms: platforms,
			Digest:    a.digest,
		})
		pkgs[name] = a.pkg
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })

	return &Result{Entries: entries, Pkgs: pkgs}, nil
}

// walk visits name for platform: it always reconciles name's contribution
// (so conflicting versions from different paths are still compared), but
// only descends into its deps the first time it's reached on this
// platform — that first-wins guard is what stops a dependency cycle from
// recursing forever.
func walk(ctx context.Context, sources []catalog.Named, name, version, catName, digest string, pkg *spec.Package, platform spec.Platform, visited map[string]bool, accs map[string]*acc) error {
	reconcile(accs, name, version, catName, digest, pkg, platform)
	if visited[name] {
		return nil
	}
	visited[name] = true

	for _, dep := range pkg.Deps {
		if !depIncludes(dep, platform) {
			continue
		}
		depPkg, depCat, depDig, err := catalog.Lookup(ctx, sources, project.ToolKey{Name: dep.Name})
		if err != nil {
			return err
		}
		if !supports(depPkg, platform) {
			continue
		}
		depVersion, err := resolveVersion(depPkg, dep.Name, dep.Version, depCat)
		if err != nil {
			return err
		}
		if err := walk(ctx, sources, dep.Name, depVersion, depCat, depDig, depPkg, platform, visited, accs); err != nil {
			return err
		}
	}
	return nil
}

// reconcile records name's contribution for platform, keeping the highest
// of the versions seen so far and the source (catalog/digest/pkg) that
// supplied it.
func reconcile(accs map[string]*acc, name, version, catName, digest string, pkg *spec.Package, platform spec.Platform) {
	a, ok := accs[name]
	if !ok {
		accs[name] = &acc{
			version: version, catalog: catName, digest: digest, pkg: pkg,
			platforms: map[spec.Platform]bool{platform: true},
		}
		return
	}
	a.platforms[platform] = true
	if higher(version, a.version) {
		a.version, a.catalog, a.digest, a.pkg = version, catName, digest, pkg
	}
}

// supports reports whether pkg's declared platform support covers platform.
func supports(pkg *spec.Package, platform spec.Platform) bool {
	for _, p := range pkg.SupportedBy() {
		if p == platform {
			return true
		}
	}
	return false
}

// depIncludes reports whether a dep edge's platform constraint (empty =
// unconstrained) covers platform.
func depIncludes(dep spec.Dep, platform spec.Platform) bool {
	if len(dep.Platforms) == 0 {
		return true
	}
	for _, c := range dep.Platforms {
		if c.Matches(platform) {
			return true
		}
	}
	return false
}

// resolveVersion resolves "" to pkg's latest version, or checks that an
// explicit version exists among pkg's versions.
func resolveVersion(pkg *spec.Package, name, version, catName string) (string, error) {
	if version == "" {
		return pkg.Versions[0].Version, nil
	}
	for _, v := range pkg.Versions {
		if v.Version == version {
			return version, nil
		}
	}
	return "", &catalog.VersionNotFoundError{Name: name, Version: version, Catalog: catName}
}

// higher reports whether candidate outranks current: by semver when both
// parse as valid semver (accepting a missing leading "v"), else
// lexicographically.
func higher(candidate, current string) bool {
	cv, curv := withV(candidate), withV(current)
	if semver.IsValid(cv) && semver.IsValid(curv) {
		return semver.Compare(cv, curv) > 0
	}
	return strings.Compare(candidate, current) > 0
}

func withV(v string) string {
	if strings.HasPrefix(v, "v") {
		return v
	}
	return "v" + v
}
