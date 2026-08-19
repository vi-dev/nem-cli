// Package resolve computes the dependency closure of a manifest's tools
// across nem's platform matrix and turns it into nem.lock entries.
package resolve

import (
	"context"
	"fmt"
	"sort"
	"strings"

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

// SonameConflictError reports two incompatible compat ranges required for
// one package in a single resolution.
type SonameConflictError struct{ Name, A, B string }

func (e *SonameConflictError) Error() string {
	return fmt.Sprintf("package %s: incompatible dependency ranges %q and %q", e.Name, e.A, e.B)
}

// PinConflictError reports a directly-pinned tool whose pin lost the
// resolution: a dependency edge demands a different version, and honoring
// it would install something the manifest's pin doesn't declare.
type PinConflictError struct{ Name, Pinned, Required string }

func (e *PinConflictError) Error() string {
	return fmt.Sprintf("package %s: pinned %s but a dependency requires %s", e.Name, e.Pinned, e.Required)
}

// compatComponents splits a version or compat string into dotted components,
// dropping an optional leading "v".
func compatComponents(s string) []string {
	return strings.Split(strings.TrimPrefix(s, "v"), ".")
}

// matchesCompat reports whether version has compat's components as a prefix.
func matchesCompat(version, compat string) bool {
	vs, cs := compatComponents(version), compatComponents(compat)
	if len(cs) > len(vs) {
		return false
	}
	for i := range cs {
		if vs[i] != cs[i] {
			return false
		}
	}
	return true
}

// selectCompat returns the highest version matching compat, or "" if none.
func selectCompat(versions []spec.VersionEntry, compat string) string {
	best := ""
	for _, v := range versions {
		if !matchesCompat(v.Version, compat) {
			continue
		}
		if best == "" || higher(v.Version, best) {
			best = v.Version
		}
	}
	return best
}

// mergeCompat combines two compat ranges: "" is identity; otherwise the
// tighter (longer) wins when one is a prefix of the other, and incompatible
// ranges report ok=false.
func mergeCompat(a, b string) (string, bool) {
	switch {
	case a == "":
		return b, true
	case b == "":
		return a, true
	}
	as, bs := compatComponents(a), compatComponents(b)
	n := len(as)
	if len(bs) < n {
		n = len(bs)
	}
	for i := 0; i < n; i++ {
		if as[i] != bs[i] {
			return "", false
		}
	}
	if len(as) >= len(bs) {
		return a, true
	}
	return b, true
}

// acc is one package's reconciled state across the whole resolution: the
// currently-winning version and its source, plus the union of platforms
// that need it. floating marks a version no contribution has demanded yet
// (only floating edges have reached the package), so any later exact
// requirement may replace it.
type acc struct {
	version      string
	compat       string
	catalog      string
	digest       string
	pkg          *spec.Package
	platforms    map[spec.Platform]bool
	onPath       bool
	onLoaderPath bool
	floating     bool
}

// contribution is how a single reach of a package feeds env composition:
// whether its bins go on PATH and whether its libs go on the loader path.
type contribution struct{ onPath, onLoaderPath bool }

func directContribution(pkg *spec.Package) contribution {
	return contribution{onPath: true, onLoaderPath: len(pkg.Libs) > 0}
}

func edgeContribution(kind spec.DepKind, pkg *spec.Package) contribution {
	if kind == spec.DepKindLink {
		return contribution{onPath: false, onLoaderPath: len(pkg.Libs) > 0}
	}
	return contribution{onPath: true, onLoaderPath: false}
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
			if err := walk(ctx, sources, r.name, r.version, r.catalog, r.digest, r.pkg, directContribution(r.pkg), platform, visited, accs); err != nil {
				return nil, err
			}
		}
	}

	// An exact pin must survive reconciliation verbatim: a dependency edge
	// floating it to another version would install something the manifest
	// doesn't declare, so that's a conflict for the user to resolve, not a
	// silent override.
	for _, t := range tools {
		if t.Version == "" {
			continue
		}
		if a := accs[t.Key.Name]; a != nil && a.version != t.Version {
			return nil, &PinConflictError{Name: t.Key.Name, Pinned: t.Version, Required: a.version}
		}
	}

	return resultFrom(accs, directNames), nil
}

// resultFrom builds the Result from reconciled accs; directNames marks which
// entries were top-level tools (nil → none are direct).
func resultFrom(accs map[string]*acc, directNames map[string]bool) *Result {
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
			Name: name, Version: a.version, Catalog: a.catalog,
			Direct: directNames[name], Platforms: platforms, Digest: a.digest,
			OnPath: a.onPath, OnLoaderPath: a.onLoaderPath,
		})
		pkgs[name] = a.pkg
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return &Result{Entries: entries, Pkgs: pkgs}
}

// ResolveBuild computes the closure of pkg's build.deps across spec.Supported,
// treating each build.dep as a dependency edge of pkg — the same edge walk (and
// edgeContribution role assignment) Resolve applies to a package's runtime
// deps. The package being built is not itself part of the result.
func ResolveBuild(ctx context.Context, pkg *spec.Package, sources []catalog.Named) (*Result, error) {
	// A stand-in root whose deps are pkg's build.deps: walkDeps walks these as
	// edges, so kind drives each dep's role via edgeContribution exactly as for
	// runtime deps. The stand-in is never reconciled, so it is not in the result.
	rootPkg := &spec.Package{Name: pkg.Name, Platforms: pkg.Platforms, Deps: pkg.Build.Deps}
	accs := map[string]*acc{}
	for _, platform := range spec.Supported {
		if !supports(rootPkg, platform) {
			continue
		}
		visited := map[string]bool{}
		if err := walkDeps(ctx, sources, rootPkg, platform, visited, accs); err != nil {
			return nil, err
		}
	}
	return resultFrom(accs, nil), nil
}

// walk visits name for platform: it always reconciles name's contribution
// (so conflicting versions from different paths are still compared), but
// only descends into its deps the first time it's reached on this
// platform — that first-wins guard is what stops a dependency cycle from
// recursing forever.
func walk(ctx context.Context, sources []catalog.Named, name, version, catName, digest string, pkg *spec.Package, contrib contribution, platform spec.Platform, visited map[string]bool, accs map[string]*acc) error {
	if err := reconcile(accs, name, version, "", false, catName, digest, pkg, contrib, platform); err != nil {
		return err
	}
	if visited[name] {
		return nil
	}
	visited[name] = true
	return walkDeps(ctx, sources, pkg, platform, visited, accs)
}

// walkDeps reconciles and (first-wins) recurses into pkg's deps for platform.
func walkDeps(ctx context.Context, sources []catalog.Named, pkg *spec.Package, platform spec.Platform, visited map[string]bool, accs map[string]*acc) error {
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
		depVersion, floating, err := resolveDepVersion(depPkg, dep, depCat)
		if err != nil {
			return err
		}
		if err := reconcile(accs, dep.Name, depVersion, dep.Compat, floating, depCat, depDig, depPkg, edgeContribution(dep.Kind, depPkg), platform); err != nil {
			return err
		}
		if visited[dep.Name] {
			continue
		}
		visited[dep.Name] = true
		if err := walkDeps(ctx, sources, depPkg, platform, visited, accs); err != nil {
			return err
		}
	}
	return nil
}

// resolveDepVersion picks a dep's version: the highest match within a link
// dep's compat range, else exact-or-latest. A version-less, compat-less dep
// is floating: it names no version at all, so its latest pick is only a
// default for reconcile to keep when nothing else determines the package's
// version.
func resolveDepVersion(pkg *spec.Package, dep spec.Dep, catName string) (version string, floating bool, err error) {
	if dep.Kind == spec.DepKindLink && dep.Compat != "" {
		if v := selectCompat(pkg.Versions, dep.Compat); v != "" {
			return v, false, nil
		}
		return "", false, &catalog.VersionNotFoundError{Name: dep.Name, Version: dep.Compat + ".x", Catalog: catName}
	}
	version, err = resolveVersion(pkg, dep.Name, dep.Version, catName)
	return version, dep.Version == "", err
}

// reconcile records name's contribution for platform, keeping the highest
// of the versions seen so far and the source (catalog/digest/pkg) that
// supplied it. Because higher only overwrites on a strictly greater
// version, an equal-version contribution never replaces the current one:
// when two sources resolve name to the same chosen version, the first one
// reconciled keeps its Catalog/Digest/pkg attribution. Reconcile order
// follows roots in tools order, with each root reconciled before its own
// dependency subtree is walked, so a direct tool's pin outranks a
// same-version dep discovered later.
//
// A non-empty compat narrows this to the intersection of every compat range
// seen for name so far: mergeCompat combines the new range with the
// accumulated one, and the merged range's highest matching version becomes
// the winner regardless of higher's ordering. compat ranges that don't
// intersect, or a merged range with no matching version among the deps
// already reconciled, report SonameConflictError.
//
// A floating contribution (a dep edge naming no version or compat) is
// satisfied by any version, so it never overrides or conflicts with a
// non-floating choice: its resolved-latest only stands while every
// contribution seen for name is floating. The first non-floating
// contribution replaces a floating version outright — even a lower one —
// and on an equal version leaves the first-processed attribution in place.
func reconcile(accs map[string]*acc, name, version, compat string, floating bool, catName, digest string, pkg *spec.Package, contrib contribution, platform spec.Platform) error {
	a, ok := accs[name]
	if !ok {
		accs[name] = &acc{
			version: version, compat: compat, catalog: catName, digest: digest, pkg: pkg,
			platforms:    map[spec.Platform]bool{platform: true},
			onPath:       contrib.onPath,
			onLoaderPath: contrib.onLoaderPath,
			floating:     floating,
		}
		return nil
	}
	a.platforms[platform] = true
	a.onPath = a.onPath || contrib.onPath
	a.onLoaderPath = a.onLoaderPath || contrib.onLoaderPath

	conflict := &SonameConflictError{Name: name, A: a.compat, B: compat}

	merged, ok := mergeCompat(a.compat, compat)
	if !ok {
		return conflict
	}
	if merged != "" {
		selected := selectCompat(a.pkg.Versions, merged)
		if selected == "" {
			return conflict
		}
		// Only a non-compat exact contribution is range-checked here; a compat
		// contribution was already picked within a compatible range, and a
		// floating one accepts the range's selection by definition. A non-compat
		// exact version outside the range is a conflict — resolving it silently
		// instead would let a compat edge quietly override an explicit pick.
		if compat == "" && !floating && !matchesCompat(version, merged) {
			return conflict
		}
		a.compat, a.version = merged, selected
		return nil
	}
	switch {
	case a.floating && !floating:
		if version != a.version {
			a.version, a.catalog, a.digest, a.pkg = version, catName, digest, pkg
		}
		a.floating = false
	case !a.floating && floating:
		// The accumulated version already satisfies a floating edge.
	case higher(version, a.version):
		a.version, a.catalog, a.digest, a.pkg = version, catName, digest, pkg
	}
	return nil
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

// higher reports whether candidate outranks current, using the shared
// catalog version ordering.
func higher(candidate, current string) bool {
	return spec.CompareVersions(candidate, current) > 0
}
