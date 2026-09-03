// Package resolve computes the dependency closure of a manifest's tools
// across nem's platform matrix and turns it into nem.lock entries.
package resolve

import (
	"context"
	"fmt"
	"slices"
	"sort"

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

// PinConflictError reports a directly-pinned tool whose pin a dependency
// edge contradicts.
type PinConflictError struct{ Name, Pinned, Required string }

func (e *PinConflictError) Error() string {
	return fmt.Sprintf("package %s: pinned %s but a dependency requires %s", e.Name, e.Pinned, e.Required)
}

// demandKind classifies how a demand names a version.
type demandKind int

const (
	demandFloating demandKind = iota // bare dep edge or version-less direct tool: satisfied by anything
	demandRanged                     // link dep compat range
	demandExact                      // dep edge naming a version
	demandPinned                     // direct tool with an explicit manifest version
)

// demand is one version requirement recorded for a package, carrying the
// catalog data of the lookup that produced it.
type demand struct {
	kind    demandKind
	version string // exact/pinned: the demanded version; floating: resolved latest
	compat  string // ranged only
	direct  bool   // a directly declared tool, not a dep edge
	link    bool   // reached as a link dep
	catalog string
	digest  string
	pkg     *spec.Package
}

// isExact reports whether d names one version outright.
func (d demand) isExact() bool { return d.kind == demandExact || d.kind == demandPinned }

// candidate is one package's collected state: every distinct demand the
// walks recorded, plus the union of platforms and env roles that reach
// it. Versions are chosen from demands only after every walk completes,
// so no arrival order can change the outcome.
type candidate struct {
	platforms    map[spec.Platform]bool
	onPath       bool
	onLoaderPath bool
	demands      []demand
}

// collector accumulates candidates across the per-platform walks.
type collector struct {
	sources []catalog.Named
	cands   map[string]*candidate
}

func newCollector(sources []catalog.Named) *collector {
	return &collector{sources: sources, cands: map[string]*candidate{}}
}

// record notes platform and env-role reach for d's package and appends d
// unless an identical demand was already recorded. Catalog lookups
// enforce that a package declares the name it was looked up by.
func (c *collector) record(d demand, platform spec.Platform) {
	cand, ok := c.cands[d.pkg.Name]
	if !ok {
		cand = &candidate{platforms: map[spec.Platform]bool{}}
		c.cands[d.pkg.Name] = cand
	}
	cand.platforms[platform] = true
	cand.onPath = cand.onPath || !d.link
	cand.onLoaderPath = cand.onLoaderPath || ((d.direct || d.link) && len(d.pkg.Libs) > 0)
	for _, seen := range cand.demands {
		if seen.kind == d.kind && seen.version == d.version && seen.compat == d.compat && seen.direct == d.direct {
			return
		}
	}
	cand.demands = append(cand.demands, d)
}

// walk records d and, on the first visit per platform, descends into its
// package's deps; the first-visit guard terminates dependency cycles.
func (c *collector) walk(ctx context.Context, d demand, platform spec.Platform, visited map[string]bool) error {
	c.record(d, platform)
	if visited[d.pkg.Name] {
		return nil
	}
	visited[d.pkg.Name] = true
	return c.walkDeps(ctx, d.pkg, platform, visited)
}

// walkDeps walks each of pkg's deps for platform.
func (c *collector) walkDeps(ctx context.Context, pkg *spec.Package, platform spec.Platform, visited map[string]bool) error {
	for _, dep := range pkg.Deps {
		if !spec.PlatformsInclude(dep.Platforms, platform) {
			continue
		}
		depPkg, depCat, depDig, err := catalog.Lookup(ctx, c.sources, project.ToolKey{Name: dep.Name})
		if err != nil {
			return err
		}
		if !slices.Contains(depPkg.SupportedBy(), platform) {
			continue
		}
		d, err := edgeDemand(dep, depPkg, depCat, depDig)
		if err != nil {
			return err
		}
		if err := c.walk(ctx, d, platform, visited); err != nil {
			return err
		}
	}
	return nil
}

// edgeDemand builds a dep edge's demand: a compat range (validated to
// match some catalog version), an exact version (validated to exist), or
// a floating latest.
func edgeDemand(dep spec.Dep, pkg *spec.Package, catName, digest string) (demand, error) {
	d := demand{link: dep.Kind == spec.DepKindLink, catalog: catName, digest: digest, pkg: pkg}
	switch {
	case dep.Compat != "":
		if selectHighest(pkg.Versions, dep.Compat) == "" {
			return demand{}, &catalog.VersionNotFoundError{Name: dep.Name, Version: dep.Compat + ".x", Catalog: catName}
		}
		d.kind, d.compat = demandRanged, dep.Compat
	case dep.Version != "":
		version, err := resolveVersion(pkg, dep.Name, dep.Version, catName)
		if err != nil {
			return demand{}, err
		}
		d.kind, d.version = demandExact, version
	default:
		d.kind, d.version = demandFloating, pkg.Versions[0].Version
	}
	return d, nil
}

// Resolve computes the dependency closure of tools across spec.SupportedPlatforms,
// reconciling one version per package name, and returns nem.lock entries
// plus the package data behind each.
func Resolve(ctx context.Context, tools []Tool, sources []catalog.Named) (*Result, error) {
	directNames := make(map[string]bool, len(tools))
	roots := make([]demand, len(tools))
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
		kind := demandFloating
		if t.Version != "" {
			kind = demandPinned
		}
		directNames[t.Key.Name] = true
		roots[i] = demand{
			kind: kind, version: version, direct: true, catalog: catName, digest: dig, pkg: pkg,
		}
	}

	col := newCollector(sources)
	for _, platform := range spec.SupportedPlatforms {
		visited := map[string]bool{}
		for _, r := range roots {
			if !slices.Contains(r.pkg.SupportedBy(), platform) {
				continue
			}
			if err := col.walk(ctx, r, platform, visited); err != nil {
				return nil, err
			}
		}
	}
	return finalize(col.cands, directNames)
}

// ResolveDeps computes the closure of deps across spec.SupportedPlatforms, walking
// each as a dependency edge of pkg. pkg is not itself part of the result.
func ResolveDeps(ctx context.Context, pkg *spec.Package, deps []spec.Dep, sources []catalog.Named) (*Result, error) {
	// Stand-in root carrying deps as ordinary dep edges; never itself
	// recorded, so absent from the result.
	rootPkg := &spec.Package{Name: pkg.Name, Platforms: pkg.Platforms, Deps: deps}
	col := newCollector(sources)
	for _, platform := range spec.SupportedPlatforms {
		if !slices.Contains(rootPkg.SupportedBy(), platform) {
			continue
		}
		visited := map[string]bool{}
		if err := col.walkDeps(ctx, rootPkg, platform, visited); err != nil {
			return nil, err
		}
	}
	return finalize(col.cands, nil)
}

// finalize chooses each candidate's version from its collected demands
// and builds the Result. Candidates finalize in name order, so the first
// error reported is the same whatever order the walks ran in.
func finalize(cands map[string]*candidate, directNames map[string]bool) (*Result, error) {
	names := make([]string, 0, len(cands))
	for name := range cands {
		names = append(names, name)
	}
	sort.Strings(names)

	entries := make([]project.LockEntry, 0, len(cands))
	pkgs := make(map[string]*spec.Package, len(cands))
	for _, name := range names {
		cand := cands[name]
		chosen, err := choose(name, cand.demands)
		if err != nil {
			return nil, err
		}
		platforms := make([]string, 0, len(cand.platforms))
		for _, p := range spec.SupportedPlatforms {
			if cand.platforms[p] {
				platforms = append(platforms, p.String())
			}
		}
		entries = append(entries, project.LockEntry{
			Name: name, Version: chosen.version, Catalog: chosen.catalog,
			Direct: directNames[name], Platforms: platforms, Digest: chosen.digest,
			OnPath: cand.onPath, OnLoaderPath: cand.onLoaderPath,
		})
		pkgs[name] = chosen.pkg
	}
	return &Result{Entries: entries, Pkgs: pkgs}, nil
}

// choose picks the version of name the demands agree on, or reports why
// none exists. A direct declaration owns the version list and the
// attribution, so a catalog qualifier keeps holding; without one, any
// demand serves — all of a name's dep demands share one lookup.
func choose(name string, demands []demand) (demand, error) {
	owner := &demands[0]
	for i := range demands {
		if demands[i].direct {
			owner = &demands[i]
		}
	}

	if v := pickVersion(owner.pkg.Versions, demands); v != "" {
		return withVersion(owner, v), nil
	}

	// Dropping the pin would resolve: the pin is what conflicts.
	if pin := pinnedOf(demands); pin != nil {
		rest := slices.DeleteFunc(slices.Clone(demands), func(d demand) bool { return d.kind == demandPinned })
		if v := pickVersion(owner.pkg.Versions, rest); v != "" {
			return demand{}, &PinConflictError{Name: name, Pinned: pin.version, Required: v}
		}
	}

	// Consistent requirements the version list cannot meet are a lookup
	// miss, not a conflict: an agreed exact version absent from the
	// list, or a range some other catalog's list satisfies.
	for _, d := range demands {
		if d.isExact() && satisfiesAll(d.version, demands) {
			return demand{}, &catalog.VersionNotFoundError{Name: name, Version: d.version, Catalog: owner.catalog}
		}
		if d.kind == demandRanged && pickVersion(d.pkg.Versions, demands) != "" {
			return demand{}, &catalog.VersionNotFoundError{Name: name, Version: d.compat + ".x", Catalog: owner.catalog}
		}
	}

	return demand{}, newCompatConflictError(name, requirements(demands)...)
}

// satisfies reports whether version v meets d's requirement.
func satisfies(v string, d demand) bool {
	switch d.kind {
	case demandRanged:
		return matchesCompat(v, d.compat)
	case demandExact, demandPinned:
		return v == d.version
	case demandFloating:
		return true
	}
	return true
}

func satisfiesAll(v string, demands []demand) bool {
	for _, d := range demands {
		if !satisfies(v, d) {
			return false
		}
	}
	return true
}

// pickVersion returns the version of versions the demands choose, or "":
// the highest satisfying one when a compat range is among them — a range
// names its highest match — and otherwise the first satisfying one, so
// list order names the latest.
func pickVersion(versions []spec.VersionEntry, demands []demand) string {
	ranged := slices.ContainsFunc(demands, func(d demand) bool { return d.kind == demandRanged })
	best := ""
	for _, v := range versions {
		if !satisfiesAll(v.Version, demands) {
			continue
		}
		if !ranged {
			return v.Version
		}
		if best == "" || spec.CompareVersions(v.Version, best) > 0 {
			best = v.Version
		}
	}
	return best
}

// requirements lists every non-floating demand's version or range.
func requirements(demands []demand) []string {
	var out []string
	for _, d := range demands {
		switch d.kind {
		case demandRanged:
			out = append(out, d.compat)
		case demandExact, demandPinned:
			out = append(out, d.version)
		}
	}
	return out
}

// withVersion returns a copy of d choosing version v.
func withVersion(d *demand, v string) demand {
	c := *d
	c.version = v
	return c
}

// pinnedOf returns the pinned demand, or nil.
func pinnedOf(demands []demand) *demand {
	for i := range demands {
		if demands[i].kind == demandPinned {
			return &demands[i]
		}
	}
	return nil
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
