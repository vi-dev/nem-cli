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

// SonameConflictError reports two incompatible requirements for one
// package in a single resolution; each side is a compat range or an
// exact version, attributed to the package that demanded it ("" = a
// directly declared tool).
type SonameConflictError struct{ Name, A, ASource, B, BSource string }

func (e *SonameConflictError) Error() string {
	return fmt.Sprintf("package %s: %s requires %s but %s requires %s",
		e.Name, sourceLabel(e.ASource), e.A, sourceLabel(e.BSource), e.B)
}

// sourceLabel renders a demand's origin for error messages.
func sourceLabel(source string) string {
	if source == "" {
		return "nem.toml"
	}
	return source
}

// PinConflictError reports a directly-pinned tool whose pin cannot be
// honored: a dependency edge demands a different version, and installing
// it would contradict what the manifest declares.
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

// mergeCompat intersects two compat ranges: "" is identity, the tighter
// prefix wins — ("3", "3.4") → "3.4" — and disjoint ranges ("1", "3")
// report ok=false. The result is always one of the inputs verbatim.
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

// demandKind classifies how a contribution names a version.
type demandKind int

const (
	demandFloating demandKind = iota // bare dep edge: satisfied by anything
	demandRanged                     // link dep compat range
	demandExact                      // dep edge naming a version, or a version-less direct tool at latest
	demandPinned                     // direct tool with an explicit manifest version
)

// demand is one version requirement recorded for a package, carrying the
// provenance and catalog data of the lookup that produced it.
type demand struct {
	kind    demandKind
	version string // exact/pinned: the demanded version; floating: resolved latest
	compat  string // ranged only
	source  string // the depending package, "" for a directly declared tool
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

// root is a direct tool's resolved starting point for the per-platform walks.
type root struct {
	name string
	d    demand
}

// collector accumulates candidates across the per-platform walks.
type collector struct {
	sources []catalog.Named
	cands   map[string]*candidate
}

func newCollector(sources []catalog.Named) *collector {
	return &collector{sources: sources, cands: map[string]*candidate{}}
}

// record notes platform and env-role reach for name and appends d unless
// an identical demand was already recorded.
func (c *collector) record(name string, d demand, contrib contribution, platform spec.Platform) {
	cand, ok := c.cands[name]
	if !ok {
		cand = &candidate{platforms: map[spec.Platform]bool{}}
		c.cands[name] = cand
	}
	cand.platforms[platform] = true
	cand.onPath = cand.onPath || contrib.onPath
	cand.onLoaderPath = cand.onLoaderPath || contrib.onLoaderPath
	for _, seen := range cand.demands {
		if seen.kind == d.kind && seen.version == d.version && seen.compat == d.compat && seen.source == d.source {
			return
		}
	}
	cand.demands = append(cand.demands, d)
}

// walk records d for name and, on the first visit per platform, descends
// into its package's deps; the first-visit guard terminates dependency
// cycles.
func (c *collector) walk(ctx context.Context, name string, d demand, contrib contribution, platform spec.Platform, visited map[string]bool) error {
	c.record(name, d, contrib, platform)
	if visited[name] {
		return nil
	}
	visited[name] = true
	return c.walkDeps(ctx, d.pkg, platform, visited)
}

// walkDeps records and (first-visit) recurses into pkg's deps for platform.
func (c *collector) walkDeps(ctx context.Context, pkg *spec.Package, platform spec.Platform, visited map[string]bool) error {
	for _, dep := range pkg.Deps {
		if !spec.PlatformsInclude(dep.Platforms, platform) {
			continue
		}
		depPkg, depCat, depDig, err := catalog.Lookup(ctx, c.sources, project.ToolKey{Name: dep.Name})
		if err != nil {
			return err
		}
		if !supports(depPkg, platform) {
			continue
		}
		d, err := edgeDemand(dep, depPkg, depCat, depDig, pkg.Name)
		if err != nil {
			return err
		}
		if err := c.walk(ctx, dep.Name, d, edgeContribution(dep.Kind, depPkg), platform, visited); err != nil {
			return err
		}
	}
	return nil
}

// edgeDemand builds a dep edge's demand: a compat range (validated to
// match some catalog version), an exact version (validated to exist), or
// a floating latest.
func edgeDemand(dep spec.Dep, pkg *spec.Package, catName, digest, from string) (demand, error) {
	d := demand{source: from, catalog: catName, digest: digest, pkg: pkg}
	switch {
	case dep.Compat != "":
		if selectCompat(pkg.Versions, dep.Compat) == "" {
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

// Resolve computes the dependency closure of tools across spec.Supported,
// reconciling one version per package name, and returns nem.lock entries
// plus the package data behind each.
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
		kind := demandExact
		if t.Version != "" {
			kind = demandPinned
		}
		directNames[t.Key.Name] = true
		roots[i] = root{name: t.Key.Name, d: demand{
			kind: kind, version: version, catalog: catName, digest: dig, pkg: pkg,
		}}
	}

	col := newCollector(sources)
	for _, platform := range spec.Supported {
		visited := map[string]bool{}
		for _, r := range roots {
			if !supports(r.d.pkg, platform) {
				continue
			}
			if err := col.walk(ctx, r.name, r.d, directContribution(r.d.pkg), platform, visited); err != nil {
				return nil, err
			}
		}
	}
	return finalize(col.cands, directNames)
}

// ResolveBuild computes the closure of pkg's build.deps across
// spec.Supported, walking each build.dep as a dependency edge of pkg. The
// package being built is not itself part of the result.
func ResolveBuild(ctx context.Context, pkg *spec.Package, sources []catalog.Named) (*Result, error) {
	// Stand-in root carrying build.deps as ordinary dep edges; never
	// itself recorded, so absent from the result.
	rootPkg := &spec.Package{Name: pkg.Name, Platforms: pkg.Platforms, Deps: pkg.Build.Deps}
	col := newCollector(sources)
	for _, platform := range spec.Supported {
		if !supports(rootPkg, platform) {
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
//
// With a catalog holding 2.0, 1.9 and 1.8, demands combine as:
//
//	exact 1.9  + exact 1.8    → conflict (PinConflictError when pinned)
//	exact 1.9  + exact 1.9    → 1.9   the pin, else the first demand, wins
//	floating   + exact 1.8    → 1.8   floating accepts any choice
//	floating   + compat "1"   → 1.9   the range's highest
//	compat "1" + compat "1.8" → 1.8   ranges intersect, tighter wins
//	compat "1" + compat "2"   → conflict
//	compat "1" + exact 1.8    → 1.8   in-range exact beats range default
//	compat "1" + exact 2.0    → conflict (PinConflictError when pinned)
//
// Conflict sides are ordered by version then source, not arrival, so the
// same conflict reports identically for every walk order. Attribution
// (catalog/digest/pkg) follows the demand that determined the version;
// arrival order breaks remaining ties.
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
		for _, p := range spec.Supported {
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

// choose picks the version name's demands agree on, returning the demand
// that determined it (with version filled in), or reports the conflict.
func choose(name string, demands []demand) (demand, error) {
	// Fold ranges in canonical order so a multi-range conflict reports
	// the same pair whatever order the walks recorded them. The merged
	// range is always one demand's compat verbatim, so its owner names a
	// range every other folded range accepts.
	ranged := make([]demand, 0, len(demands))
	for _, d := range demands {
		if d.kind == demandRanged {
			ranged = append(ranged, d)
		}
	}
	sort.Slice(ranged, func(i, j int) bool {
		if c := spec.CompareVersions(ranged[i].compat, ranged[j].compat); c != 0 {
			return c < 0
		}
		return ranged[i].source < ranged[j].source
	})
	merged := ""
	var rangeOwner *demand
	for i := range ranged {
		d := &ranged[i]
		m, ok := mergeCompat(merged, d.compat)
		if !ok {
			return demand{}, conflictErr(name, merged, rangeOwner.source, d.compat, d.source)
		}
		if m != merged {
			merged, rangeOwner = m, d
		}
	}

	exacts := make([]demand, 0, len(demands))
	for _, d := range demands {
		if d.isExact() {
			exacts = append(exacts, d)
		}
	}
	if len(exacts) > 0 {
		sorted := append([]demand(nil), exacts...)
		sort.Slice(sorted, func(i, j int) bool {
			if c := spec.CompareVersions(sorted[i].version, sorted[j].version); c != 0 {
				return c < 0
			}
			return sorted[i].source < sorted[j].source
		})
		for _, d := range sorted[1:] {
			if d.version == sorted[0].version {
				continue
			}
			if pin := pinnedOf(exacts); pin != nil {
				return demand{}, &PinConflictError{Name: name, Pinned: pin.version, Required: otherVersion(sorted, pin.version)}
			}
			return demand{}, conflictErr(name, sorted[0].version, sorted[0].source, d.version, d.source)
		}
		best := exacts[0]
		for _, d := range exacts[1:] {
			if attributionRank(d) > attributionRank(best) {
				best = d
			}
		}
		if merged != "" && !matchesCompat(best.version, merged) {
			if best.kind == demandPinned {
				return demand{}, &PinConflictError{Name: name, Pinned: best.version, Required: selectCompat(rangeOwner.pkg.Versions, merged)}
			}
			return demand{}, conflictErr(name, best.version, best.source, merged, rangeOwner.source)
		}
		return best, nil
	}

	if merged != "" {
		// Non-empty by the owner invariant: merged is the owner's own
		// compat, which edgeDemand validated against the owner's catalog.
		chosen := *rangeOwner
		chosen.version = selectCompat(rangeOwner.pkg.Versions, merged)
		return chosen, nil
	}

	// Floating only: highest resolved latest; ties keep the first walked.
	best := demands[0]
	for _, d := range demands[1:] {
		if higher(d.version, best.version) {
			best = d
		}
	}
	return best, nil
}

// attributionRank orders equal-version exact demands for attribution: a
// pin over a version-less direct tool (whose catalog qualifier must
// hold) over dep edges; append order breaks remaining ties.
func attributionRank(d demand) int {
	switch {
	case d.kind == demandPinned:
		return 2
	case d.source == "":
		return 1
	}
	return 0
}

// pinnedOf returns the pinned demand among exacts, or nil.
func pinnedOf(exacts []demand) *demand {
	for i := range exacts {
		if exacts[i].kind == demandPinned {
			return &exacts[i]
		}
	}
	return nil
}

// otherVersion returns the lowest version in sorted differing from pinned.
func otherVersion(sorted []demand, pinned string) string {
	for _, d := range sorted {
		if d.version != pinned {
			return d.version
		}
	}
	return ""
}

// conflictErr builds a SonameConflictError with canonically ordered
// sides — by version/range, then source — so the same conflict reports
// identically for every walk order.
func conflictErr(name, a, aSource, b, bSource string) *SonameConflictError {
	if c := spec.CompareVersions(a, b); c > 0 || (c == 0 && aSource > bSource) {
		a, aSource, b, bSource = b, bSource, a, aSource
	}
	return &SonameConflictError{Name: name, A: a, ASource: aSource, B: b, BSource: bSource}
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
