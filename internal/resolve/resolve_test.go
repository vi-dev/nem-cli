package resolve_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vi-dev/nem-cli/internal/catalog"
	"github.com/vi-dev/nem-cli/internal/project"
	"github.com/vi-dev/nem-cli/internal/resolve"
	"github.com/vi-dev/nem-cli/internal/spec"
)

// writePkg parses yaml (to derive the package name) and writes it into a
// dir catalog rooted at root.
func writePkg(t *testing.T, root, yaml string) {
	t.Helper()
	pkg, err := spec.Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	dir := filepath.Join(root, "pkgs", pkg.Name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pkg.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatalf("write pkg.yaml: %v", err)
	}
}

func namedSources(root string) []catalog.Named {
	return []catalog.Named{{Name: "cat", Source: catalog.NewDir(root)}}
}

func entry(t *testing.T, res *resolve.Result, name string) project.LockEntry {
	t.Helper()
	for _, e := range res.Entries {
		if e.Name == name {
			return e
		}
	}
	t.Fatalf("no entry %q in %+v", name, res.Entries)
	return project.LockEntry{}
}

func TestResolveSingleToolLatest(t *testing.T) {
	root := t.TempDir()
	writePkg(t, root, `
schema: 2
name: a
artifact:
  oci: ":{{.Version}}"
install:
  - extract: {}
versions:
  - v2.0.0
  - v1.0.0
`)
	tools := []resolve.Tool{{Key: project.ToolKey{Name: "a"}}}
	res, err := resolve.Resolve(context.Background(), tools, namedSources(root))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(res.Entries) != 1 {
		t.Fatalf("want 1 entry, got %+v", res.Entries)
	}
	e := res.Entries[0]
	if e.Name != "a" || e.Version != "v2.0.0" || e.Catalog != "cat" || !e.Direct || e.Digest != "" {
		t.Fatalf("entry: %+v", e)
	}
	if len(e.Platforms) != 4 {
		t.Fatalf("platforms: %v", e.Platforms)
	}
	if res.Pkgs["a"] == nil || res.Pkgs["a"].Name != "a" {
		t.Fatalf("Pkgs[a]: %+v", res.Pkgs["a"])
	}
}

func TestResolveRangePicksHighestMatch(t *testing.T) {
	root := t.TempDir()
	// Versions listed out of order: a range names its highest match, not
	// the first listed.
	writePkg(t, root, `
schema: 2
name: lib
libs: [lib]
artifact:
  oci: ":{{.Version}}"
install:
  - extract: {}
versions:
  - v2.0.0
  - v1.8.0
  - v1.9.0
`)
	writePkg(t, root, `
schema: 2
name: app
deps:
  - {name: lib, kind: link, compat: "1"}
artifact:
  oci: ":{{.Version}}"
install:
  - extract: {}
versions:
  - v1.0.0
`)
	tools := []resolve.Tool{{Key: project.ToolKey{Name: "app"}}}
	res, err := resolve.Resolve(context.Background(), tools, namedSources(root))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if e := entry(t, res, "lib"); e.Version != "v1.9.0" {
		t.Fatalf("the range must name its highest match: %+v", e)
	}
}

func TestResolveVersionlessKeepsListOrderLatest(t *testing.T) {
	root := t.TempDir()
	// A stable promoted above a newer-comparing prerelease: list order,
	// not version comparison, names the latest.
	writePkg(t, root, `
schema: 2
name: a
artifact:
  oci: ":{{.Version}}"
install:
  - extract: {}
versions:
  - v1.2.3
  - v1.3.0-rc1
`)
	tools := []resolve.Tool{{Key: project.ToolKey{Name: "a"}}}
	res, err := resolve.Resolve(context.Background(), tools, namedSources(root))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if e := entry(t, res, "a"); e.Version != "v1.2.3" {
		t.Fatalf("latest must be the list's top satisfying entry: %+v", e)
	}
}

func TestResolveDepChain(t *testing.T) {
	root := t.TempDir()
	writePkg(t, root, `
schema: 2
name: a
deps:
  - name: b
artifact:
  oci: ":{{.Version}}"
install:
  - extract: {}
versions:
  - v1.0.0
`)
	writePkg(t, root, `
schema: 2
name: b
artifact:
  oci: ":{{.Version}}"
install:
  - extract: {}
versions:
  - v1.0.0
`)
	tools := []resolve.Tool{{Key: project.ToolKey{Name: "a"}}}
	res, err := resolve.Resolve(context.Background(), tools, namedSources(root))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(res.Entries) != 2 {
		t.Fatalf("want 2 entries, got %+v", res.Entries)
	}
	a := entry(t, res, "a")
	b := entry(t, res, "b")
	if !a.Direct {
		t.Fatalf("a should be direct: %+v", a)
	}
	if b.Direct {
		t.Fatalf("b should be indirect: %+v", b)
	}
	if len(b.Platforms) != 4 {
		t.Fatalf("b platforms should be the union (all 4): %v", b.Platforms)
	}
}

func TestResolvePlatformConstrainedDep(t *testing.T) {
	root := t.TempDir()
	writePkg(t, root, `
schema: 2
name: a
deps:
  - name: b
    platforms: [linux]
artifact:
  oci: ":{{.Version}}"
install:
  - extract: {}
versions:
  - v1.0.0
`)
	writePkg(t, root, `
schema: 2
name: b
artifact:
  oci: ":{{.Version}}"
install:
  - extract: {}
versions:
  - v1.0.0
`)
	tools := []resolve.Tool{{Key: project.ToolKey{Name: "a"}}}
	res, err := resolve.Resolve(context.Background(), tools, namedSources(root))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	b := entry(t, res, "b")
	want := []string{"linux/arm64", "linux/amd64"}
	if len(b.Platforms) != len(want) {
		t.Fatalf("b platforms: got %v, want %v", b.Platforms, want)
	}
	for i, p := range want {
		if b.Platforms[i] != p {
			t.Fatalf("b platforms: got %v, want %v", b.Platforms, want)
		}
	}
}

// TestResolveExactDepsDivergeConflict proves two dep edges demanding
// different exact versions of one package conflict — in any tools order,
// with canonical error fields naming both demands.
func TestResolveExactDepsDivergeConflict(t *testing.T) {
	root := t.TempDir()
	writePkg(t, root, `
schema: 2
name: d
artifact:
  oci: ":{{.Version}}"
install:
  - extract: {}
versions:
  - v2.0.0
  - v1.0.0
`)
	writePkg(t, root, `
schema: 2
name: p1
deps:
  - name: d
    version: v1.0.0
artifact:
  oci: ":{{.Version}}"
install:
  - extract: {}
versions:
  - v1.0.0
`)
	writePkg(t, root, `
schema: 2
name: p2
deps:
  - name: d
    version: v2.0.0
artifact:
  oci: ":{{.Version}}"
install:
  - extract: {}
versions:
  - v1.0.0
`)
	orders := map[string][]string{
		"p1 first": {"p1", "p2"},
		"p2 first": {"p2", "p1"},
	}
	for name, order := range orders {
		t.Run(name, func(t *testing.T) {
			tools := make([]resolve.Tool, len(order))
			for i, n := range order {
				tools[i] = resolve.Tool{Key: project.ToolKey{Name: n}}
			}
			_, err := resolve.Resolve(context.Background(), tools, namedSources(root))
			var sce *resolve.CompatConflictError
			if !errors.As(err, &sce) {
				t.Fatalf("want CompatConflictError, got %v", err)
			}
			if sce.Name != "d" || strings.Join(sce.Compats, ",") != "v1.0.0,v2.0.0" {
				t.Fatalf("conflict fields should be canonical in any order: %+v", sce)
			}
			if !strings.Contains(err.Error(), "conflicting requirements v1.0.0, v2.0.0") {
				t.Fatalf("error should state the requirements without attribution: %v", err)
			}
		})
	}
}

// TestResolveUnpinnedRootYieldsToExactDep proves a version-less direct
// tool floats: a dep edge naming an exact version decides it.
func TestResolveUnpinnedRootYieldsToExactDep(t *testing.T) {
	root := t.TempDir()
	writePkg(t, root, `
schema: 2
name: a
artifact:
  oci: ":{{.Version}}"
install:
  - extract: {}
versions:
  - v2.0.0
  - v1.0.0
`)
	writePkg(t, root, `
schema: 2
name: b
deps:
  - name: a
    version: v1.0.0
artifact:
  oci: ":{{.Version}}"
install:
  - extract: {}
versions:
  - v1.0.0
`)
	orders := map[string][]resolve.Tool{
		"root first": {
			{Key: project.ToolKey{Name: "a"}},
			{Key: project.ToolKey{Name: "b"}},
		},
		"dep first": {
			{Key: project.ToolKey{Name: "b"}},
			{Key: project.ToolKey{Name: "a"}},
		},
	}
	for name, tools := range orders {
		t.Run(name, func(t *testing.T) {
			res, err := resolve.Resolve(context.Background(), tools, namedSources(root))
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if e := entry(t, res, "a"); e.Version != "v1.0.0" || e.Catalog != "cat" || !e.Direct {
				t.Fatalf("the unpinned root must yield to the exact dep: %+v", e)
			}
		})
	}
}

func TestResolveMissingExplicitVersion(t *testing.T) {
	root := t.TempDir()
	writePkg(t, root, `
schema: 2
name: a
artifact:
  oci: ":{{.Version}}"
install:
  - extract: {}
versions:
  - v1.0.0
`)
	tools := []resolve.Tool{{Key: project.ToolKey{Name: "a"}, Version: "v9.9.9"}}
	_, err := resolve.Resolve(context.Background(), tools, namedSources(root))
	var vnf *catalog.VersionNotFoundError
	if !errors.As(err, &vnf) {
		t.Fatalf("want VersionNotFoundError, got %v", err)
	}
	if vnf.Name != "a" || vnf.Version != "v9.9.9" || vnf.Catalog != "cat" {
		t.Fatalf("VersionNotFoundError: %+v", vnf)
	}
}

// zeroPlatformSource is a hand-built catalog.Source: nem's platform matrix
// restricts every string that spec.ParsePlatform accepts, so a package
// supporting none of it can't be expressed in a real pkg.yaml — only by
// constructing *spec.Package directly.
type zeroPlatformSource struct{ pkg *spec.Package }

func (s zeroPlatformSource) Load(_ context.Context, name string) (*spec.Package, string, error) {
	if name != s.pkg.Name {
		return nil, "", &catalog.PackageNotFoundError{Name: name}
	}
	return s.pkg, "", nil
}

func (s zeroPlatformSource) Versions(ctx context.Context, name string) ([]string, error) {
	pkg, _, err := s.Load(ctx, name)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(pkg.Versions))
	for i, v := range pkg.Versions {
		out[i] = v.Version
	}
	return out, nil
}

func (s zeroPlatformSource) Summaries(context.Context) ([]catalog.Summary, error) { return nil, nil }

func TestResolveZeroPlatformDirectTool(t *testing.T) {
	pkg := &spec.Package{
		Schema:    2,
		Name:      "z",
		Platforms: []spec.Platform{{OS: "plan9"}},
		Versions:  []spec.VersionEntry{{Version: "v1.0.0"}},
	}
	sources := []catalog.Named{{Name: "cat", Source: zeroPlatformSource{pkg: pkg}}}
	tools := []resolve.Tool{{Key: project.ToolKey{Name: "z"}}}

	_, err := resolve.Resolve(context.Background(), tools, sources)
	var upe *resolve.UnsupportedPlatformError
	if !errors.As(err, &upe) {
		t.Fatalf("want UnsupportedPlatformError, got %v", err)
	}
	if upe.Name != "z" || upe.Version != "v1.0.0" {
		t.Fatalf("UnsupportedPlatformError: %+v", upe)
	}
}

func TestResolvePinnedToolKey(t *testing.T) {
	rootA := t.TempDir()
	writePkg(t, rootA, `
schema: 2
name: shared
artifact:
  oci: ":{{.Version}}"
install:
  - extract: {}
versions:
  - v1.0.0
`)
	rootB := t.TempDir()
	writePkg(t, rootB, `
schema: 2
name: shared
artifact:
  oci: ":{{.Version}}"
install:
  - extract: {}
versions:
  - v2.0.0
`)
	sources := []catalog.Named{{Name: "a", Source: catalog.NewDir(rootA)}, {Name: "b", Source: catalog.NewDir(rootB)}}

	toolsB := []resolve.Tool{{Key: project.ToolKey{Catalog: "b", Name: "shared"}}}
	resB, err := resolve.Resolve(context.Background(), toolsB, sources)
	if err != nil {
		t.Fatalf("Resolve (pin b): %v", err)
	}
	eB := entry(t, resB, "shared")
	if eB.Catalog != "b" || eB.Version != "v2.0.0" {
		t.Fatalf("pinned to b: %+v", eB)
	}

	toolsA := []resolve.Tool{{Key: project.ToolKey{Catalog: "a", Name: "shared"}}}
	resA, err := resolve.Resolve(context.Background(), toolsA, sources)
	if err != nil {
		t.Fatalf("Resolve (pin a): %v", err)
	}
	eA := entry(t, resA, "shared")
	if eA.Catalog != "a" || eA.Version != "v1.0.0" {
		t.Fatalf("pinned to a: %+v", eA)
	}
}

// TestResolveEqualVersionPinKeepsAttribution proves attribution follows
// the demand that determined the version: a catalog-pinned direct tool
// keeps its catalog in the lock even when a bare dep edge resolves the
// same version through another catalog first — in either tools order.
func TestResolveEqualVersionPinKeepsAttribution(t *testing.T) {
	rootA := t.TempDir()
	writePkg(t, rootA, `
schema: 2
name: shared
artifact:
  oci: ":{{.Version}}"
install:
  - extract: {}
versions:
  - v1.0.0
`)
	writePkg(t, rootA, `
schema: 2
name: consumer
deps:
  - name: shared
artifact:
  oci: ":{{.Version}}"
install:
  - extract: {}
versions:
  - v1.0.0
`)
	rootB := t.TempDir()
	writePkg(t, rootB, `
schema: 2
name: shared
artifact:
  oci: ":{{.Version}}"
install:
  - extract: {}
versions:
  - v1.0.0
`)
	sources := []catalog.Named{
		{Name: "a", Source: catalog.NewDir(rootA)},
		{Name: "b", Source: catalog.NewDir(rootB)},
	}

	tools := []resolve.Tool{
		{Key: project.ToolKey{Catalog: "b", Name: "shared"}, Version: "v1.0.0"},
		{Key: project.ToolKey{Name: "consumer"}},
	}
	res, err := resolve.Resolve(context.Background(), tools, sources)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	shared := entry(t, res, "shared")
	if shared.Version != "v1.0.0" || shared.Catalog != "b" {
		t.Fatalf("want the catalog-pinned direct tool (catalog b) to win the tie, got %+v", shared)
	}

	toolsSwapped := []resolve.Tool{
		{Key: project.ToolKey{Name: "consumer"}},
		{Key: project.ToolKey{Catalog: "b", Name: "shared"}, Version: "v1.0.0"},
	}
	resSwapped, err := resolve.Resolve(context.Background(), toolsSwapped, sources)
	if err != nil {
		t.Fatalf("Resolve (swapped): %v", err)
	}
	sharedSwapped := entry(t, resSwapped, "shared")
	if sharedSwapped.Catalog != "b" {
		t.Fatalf("the pin's catalog must win the tie in either order, got %+v", sharedSwapped)
	}
}

// TestResolveCatalogQualifiedRootKeepsAttribution proves a
// catalog-qualified, version-less direct tool keeps its catalog in the
// lock over an equal-version exact dep edge resolved through another
// catalog — in either tools order.
func TestResolveCatalogQualifiedRootKeepsAttribution(t *testing.T) {
	rootA := t.TempDir()
	writePkg(t, rootA, `
schema: 2
name: shared
artifact: {oci: ":{{.Version}}"}
install: [{extract: {}}]
versions: [v1.0.0]
`)
	writePkg(t, rootA, `
schema: 2
name: app
deps: [{name: shared, version: v1.0.0}]
artifact: {oci: ":{{.Version}}"}
install: [{extract: {}}]
versions: [v1.0.0]
`)
	rootB := t.TempDir()
	writePkg(t, rootB, `
schema: 2
name: shared
artifact: {oci: ":{{.Version}}"}
install: [{extract: {}}]
versions: [v1.0.0]
`)
	sources := []catalog.Named{
		{Name: "a", Source: catalog.NewDir(rootA)},
		{Name: "b", Source: catalog.NewDir(rootB)},
	}
	orders := map[string][]resolve.Tool{
		"tool first": {
			{Key: project.ToolKey{Catalog: "b", Name: "shared"}},
			{Key: project.ToolKey{Name: "app"}},
		},
		"dep first": {
			{Key: project.ToolKey{Name: "app"}},
			{Key: project.ToolKey{Catalog: "b", Name: "shared"}},
		},
	}
	for name, tools := range orders {
		t.Run(name, func(t *testing.T) {
			res, err := resolve.Resolve(context.Background(), tools, sources)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if shared := entry(t, res, "shared"); shared.Catalog != "b" {
				t.Fatalf("the qualified tool's catalog must win in either order, got %+v", shared)
			}
		})
	}
}

func TestResolveUnpinnedQualifiedRootSelectsFromItsCatalog(t *testing.T) {
	rootA := t.TempDir()
	writePkg(t, rootA, `
schema: 2
name: shared
artifact: {oci: ":{{.Version}}"}
install: [{extract: {}}]
versions: [v3.0.0, v1.0.0]
`)
	writePkg(t, rootA, `
schema: 2
name: app
deps: [{name: shared, version: v1.0.0}]
artifact: {oci: ":{{.Version}}"}
install: [{extract: {}}]
versions: [v1.0.0]
`)
	rootB := t.TempDir()
	writePkg(t, rootB, `
schema: 2
name: shared
artifact: {oci: ":{{.Version}}"}
install: [{extract: {}}]
versions: [v2.0.0, v1.0.0]
`)
	sources := []catalog.Named{
		{Name: "a", Source: catalog.NewDir(rootA)},
		{Name: "b", Source: catalog.NewDir(rootB)},
	}
	tools := []resolve.Tool{
		{Key: project.ToolKey{Catalog: "b", Name: "shared"}},
		{Key: project.ToolKey{Name: "app"}},
	}
	res, err := resolve.Resolve(context.Background(), tools, sources)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if e := entry(t, res, "shared"); e.Version != "v1.0.0" || e.Catalog != "b" {
		t.Fatalf("the yielded pick must come from the qualified catalog: %+v", e)
	}
}

func TestResolveUnpinnedQualifiedRootMissingBoundVersionErrors(t *testing.T) {
	rootA := t.TempDir()
	writePkg(t, rootA, `
schema: 2
name: shared
artifact: {oci: ":{{.Version}}"}
install: [{extract: {}}]
versions: [v3.0.0, v1.0.0]
`)
	writePkg(t, rootA, `
schema: 2
name: app
deps: [{name: shared, version: v1.0.0}]
artifact: {oci: ":{{.Version}}"}
install: [{extract: {}}]
versions: [v1.0.0]
`)
	rootB := t.TempDir()
	writePkg(t, rootB, `
schema: 2
name: shared
artifact: {oci: ":{{.Version}}"}
install: [{extract: {}}]
versions: [v2.0.0]
`)
	sources := []catalog.Named{
		{Name: "a", Source: catalog.NewDir(rootA)},
		{Name: "b", Source: catalog.NewDir(rootB)},
	}
	tools := []resolve.Tool{
		{Key: project.ToolKey{Catalog: "b", Name: "shared"}},
		{Key: project.ToolKey{Name: "app"}},
	}
	_, err := resolve.Resolve(context.Background(), tools, sources)
	var vnf *catalog.VersionNotFoundError
	if !errors.As(err, &vnf) {
		t.Fatalf("want VersionNotFoundError when the bound version is absent from the qualified catalog, got %v", err)
	}
	if vnf.Catalog != "b" {
		t.Fatalf("error should name the qualified catalog: %+v", vnf)
	}
}

func TestResolveQualifiedRootMissingRangeErrors(t *testing.T) {
	rootA := t.TempDir()
	writePkg(t, rootA, `
schema: 2
name: shared
libs: [lib]
artifact: {oci: ":{{.Version}}"}
install: [{extract: {}}]
versions: [v3.0.0, v1.9.5]
`)
	writePkg(t, rootA, `
schema: 2
name: app
deps: [{name: shared, kind: link, compat: "1.9"}]
artifact: {oci: ":{{.Version}}"}
install: [{extract: {}}]
versions: [v1.0.0]
`)
	rootB := t.TempDir()
	writePkg(t, rootB, `
schema: 2
name: shared
libs: [lib]
artifact: {oci: ":{{.Version}}"}
install: [{extract: {}}]
versions: [v2.0.0]
`)
	sources := []catalog.Named{
		{Name: "a", Source: catalog.NewDir(rootA)},
		{Name: "b", Source: catalog.NewDir(rootB)},
	}
	tools := []resolve.Tool{
		{Key: project.ToolKey{Catalog: "b", Name: "shared"}},
		{Key: project.ToolKey{Name: "app"}},
	}
	_, err := resolve.Resolve(context.Background(), tools, sources)
	var vnf *catalog.VersionNotFoundError
	if !errors.As(err, &vnf) {
		t.Fatalf("want VersionNotFoundError when the qualified catalog lists no version in range, got %v", err)
	}
	if vnf.Catalog != "b" {
		t.Fatalf("error should name the qualified catalog: %+v", vnf)
	}
}

// TestResolveDisjointRangesCanonicalConflict proves a multi-range
// conflict reports every range canonically: the same ordered set for
// every tools permutation.
func TestResolveDisjointRangesCanonicalConflict(t *testing.T) {
	root := t.TempDir()
	for _, p := range []struct{ name, compat string }{
		{"r1", "1"}, {"r2", "2"}, {"r3", "3"},
	} {
		writePkg(t, root, `
schema: 2
name: `+p.name+`
platforms: [darwin/arm64]
deps: [{name: openssl, kind: link, compat: "`+p.compat+`"}]
artifact: {oci: ":{{.Version}}"}
install: [{extract: {}}]
versions: [v1.0.0]
`)
	}
	writePkg(t, root, `
schema: 2
name: openssl
platforms: [darwin/arm64]
libs: [lib]
artifact: {oci: ":{{.Version}}"}
install: [{extract: {}}]
versions: [v3.0.0, v2.0.0, v1.0.0]
`)
	perms := [][]string{
		{"r1", "r2", "r3"}, {"r1", "r3", "r2"}, {"r2", "r1", "r3"},
		{"r2", "r3", "r1"}, {"r3", "r1", "r2"}, {"r3", "r2", "r1"},
	}
	for _, order := range perms {
		t.Run(strings.Join(order, ","), func(t *testing.T) {
			tools := make([]resolve.Tool, len(order))
			for i, n := range order {
				tools[i] = resolve.Tool{Key: project.ToolKey{Name: n}}
			}
			_, err := resolve.Resolve(context.Background(), tools, namedSources(root))
			var sce *resolve.CompatConflictError
			if !errors.As(err, &sce) {
				t.Fatalf("want CompatConflictError, got %v", err)
			}
			if sce.Name != "openssl" || strings.Join(sce.Compats, ",") != "1,2,3" {
				t.Fatalf("conflict should list every range canonically in any order: %+v", sce)
			}
		})
	}
}

func TestResolveRolesRunVsLink(t *testing.T) {
	root := t.TempDir()
	writePkg(t, root, `
schema: 2
name: gpgme
deps:
  - name: gpg
  - name: openssl
    kind: link
artifact: {oci: ":{{.Version}}"}
install: [{extract: {}}]
versions: [v1.0.0]
`)
	writePkg(t, root, `
schema: 2
name: gpg
artifact: {oci: ":{{.Version}}"}
install: [{extract: {}}]
versions: [v1.0.0]
`)
	writePkg(t, root, `
schema: 2
name: openssl
libs: [lib]
artifact: {oci: ":{{.Version}}"}
install: [{extract: {}}]
versions: [v3.4.0]
`)
	tools := []resolve.Tool{{Key: project.ToolKey{Name: "gpgme"}}}
	res, err := resolve.Resolve(context.Background(), tools, namedSources(root))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	gpgme := entry(t, res, "gpgme")
	gpg := entry(t, res, "gpg")
	openssl := entry(t, res, "openssl")
	if !gpgme.OnPath {
		t.Fatalf("gpgme (direct) should be on_path: %+v", gpgme)
	}
	if !gpg.OnPath || gpg.OnLoaderPath {
		t.Fatalf("gpg (run dep) should be on_path only: %+v", gpg)
	}
	if openssl.OnPath || !openssl.OnLoaderPath {
		t.Fatalf("openssl (link dep with libs) should be on_loader_path only, never on_path: %+v", openssl)
	}
}

// TestResolveDepsBuildRolesRunVsLink mirrors TestResolveRolesRunVsLink but
// for build.deps: they must get their roles via the same edge path
// (edgeContribution), not the direct-tool path.
func TestResolveDepsBuildRolesRunVsLink(t *testing.T) {
	root := t.TempDir()
	writePkg(t, root, `
schema: 2
name: openssl
libs: [lib]
artifact: {oci: ":{{.Version}}"}
install: [{extract: {}}]
versions: [v1.0.0]
`)
	writePkg(t, root, `
schema: 2
name: make
artifact: {oci: ":{{.Version}}"}
install: [{extract: {}}]
versions: [v1.0.0]
`)
	pkg := &spec.Package{
		Schema: 2,
		Name:   "tool",
		Build: &spec.Build{
			Deps: []spec.Dep{
				{Name: "openssl", Kind: spec.DepKindLink},
				{Name: "make"},
			},
		},
	}
	res, err := resolve.ResolveDeps(context.Background(), pkg, pkg.Build.Deps, namedSources(root))
	if err != nil {
		t.Fatalf("ResolveDeps: %v", err)
	}
	openssl := entry(t, res, "openssl")
	makePkg := entry(t, res, "make")
	if !openssl.OnLoaderPath || openssl.OnPath {
		t.Fatalf("openssl (link build dep with libs) should be on_loader_path only: %+v", openssl)
	}
	if !makePkg.OnPath || makePkg.OnLoaderPath {
		t.Fatalf("make (run build dep) should be on_path only: %+v", makePkg)
	}
}

func TestResolveLinkDepFloatsWithinCompat(t *testing.T) {
	root := t.TempDir()
	writePkg(t, root, `
schema: 2
name: app
deps:
  - name: openssl
    kind: link
    compat: "3"
artifact: {oci: ":{{.Version}}"}
install: [{extract: {}}]
versions: [v1.0.0]
`)
	writePkg(t, root, `
schema: 2
name: openssl
libs: [lib]
artifact: {oci: ":{{.Version}}"}
install: [{extract: {}}]
versions:
  - v4.0.0
  - v3.5.1
  - v3.4.0
  - v1.1.1
`)
	res, err := resolve.Resolve(context.Background(), []resolve.Tool{{Key: project.ToolKey{Name: "app"}}}, namedSources(root))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if v := entry(t, res, "openssl").Version; v != "v3.5.1" {
		t.Fatalf("openssl floated to %s, want highest 3.x (v3.5.1)", v)
	}
}

func TestResolveIncompatibleCompatConflicts(t *testing.T) {
	root := t.TempDir()
	writePkg(t, root, `
schema: 2
name: p1
deps: [{name: openssl, kind: link, compat: "1"}]
artifact: {oci: ":{{.Version}}"}
install: [{extract: {}}]
versions: [v1.0.0]
`)
	writePkg(t, root, `
schema: 2
name: p2
deps: [{name: openssl, kind: link, compat: "3"}]
artifact: {oci: ":{{.Version}}"}
install: [{extract: {}}]
versions: [v1.0.0]
`)
	writePkg(t, root, `
schema: 2
name: openssl
libs: [lib]
artifact: {oci: ":{{.Version}}"}
install: [{extract: {}}]
versions: [v3.5.1, v1.1.1]
`)
	tools := []resolve.Tool{{Key: project.ToolKey{Name: "p1"}}, {Key: project.ToolKey{Name: "p2"}}}
	_, err := resolve.Resolve(context.Background(), tools, namedSources(root))
	var sce *resolve.CompatConflictError
	if !errors.As(err, &sce) {
		t.Fatalf("want CompatConflictError, got %v", err)
	}
	if sce.Name != "openssl" {
		t.Fatalf("conflict should name openssl: %+v", sce)
	}
}

func TestResolveCompatTighterAndWiderReconcile(t *testing.T) {
	root := t.TempDir()
	writePkg(t, root, `
schema: 2
name: p1
deps: [{name: openssl, kind: link, compat: "3.4"}]
artifact: {oci: ":{{.Version}}"}
install: [{extract: {}}]
versions: [v1.0.0]
`)
	writePkg(t, root, `
schema: 2
name: p2
deps: [{name: openssl, kind: link, compat: "3"}]
artifact: {oci: ":{{.Version}}"}
install: [{extract: {}}]
versions: [v1.0.0]
`)
	writePkg(t, root, `
schema: 2
name: openssl
libs: [lib]
artifact: {oci: ":{{.Version}}"}
install: [{extract: {}}]
versions: [v3.5.1, v3.4.9, v3.4.2, v1.1.1]
`)
	tools := []resolve.Tool{{Key: project.ToolKey{Name: "p1"}}, {Key: project.ToolKey{Name: "p2"}}}
	res, err := resolve.Resolve(context.Background(), tools, namedSources(root))
	if err != nil {
		t.Fatalf("compatible ranges must reconcile, got error: %v", err)
	}
	if v := entry(t, res, "openssl").Version; v != "v3.4.9" {
		t.Fatalf("openssl = %s, want highest satisfying both 3 and 3.4 (v3.4.9)", v)
	}
}

func TestResolveCycleTerminates(t *testing.T) {
	root := t.TempDir()
	writePkg(t, root, `
schema: 2
name: a
deps:
  - name: b
artifact:
  oci: ":{{.Version}}"
install:
  - extract: {}
versions:
  - v1.0.0
`)
	writePkg(t, root, `
schema: 2
name: b
deps:
  - name: a
artifact:
  oci: ":{{.Version}}"
install:
  - extract: {}
versions:
  - v1.0.0
`)
	tools := []resolve.Tool{{Key: project.ToolKey{Name: "a"}}}

	done := make(chan struct{})
	var res *resolve.Result
	var err error
	go func() {
		res, err = resolve.Resolve(context.Background(), tools, namedSources(root))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Resolve did not terminate on a dependency cycle")
	}
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(res.Entries) != 2 {
		t.Fatalf("want 2 entries (a, b), got %+v", res.Entries)
	}
}

func TestResolvePinnedRootDepConflictErrors(t *testing.T) {
	root := t.TempDir()
	writePkg(t, root, `
schema: 2
name: a
artifact:
  oci: ":{{.Version}}"
install:
  - extract: {}
versions:
  - v2.0.0
  - v1.0.0
`)
	writePkg(t, root, `
schema: 2
name: b
deps:
  - name: a
    version: v2.0.0
artifact:
  oci: ":{{.Version}}"
install:
  - extract: {}
versions:
  - v1.0.0
`)
	tools := []resolve.Tool{
		{Key: project.ToolKey{Name: "a"}, Version: "v1.0.0"},
		{Key: project.ToolKey{Name: "b"}},
	}
	_, err := resolve.Resolve(context.Background(), tools, namedSources(root))
	var pce *resolve.PinConflictError
	if !errors.As(err, &pce) {
		t.Fatalf("want PinConflictError, got %v", err)
	}
	if pce.Name != "a" || pce.Pinned != "v1.0.0" || pce.Required != "v2.0.0" {
		t.Fatalf("PinConflictError: %+v", pce)
	}
}

// TestResolveBareDepDefersToPinnedRoot proves a version-less dep edge is
// floating: it accepts whatever version the resolution already chose, so a
// direct pin wins over it regardless of which side reconciles first.
func TestResolveBareDepDefersToPinnedRoot(t *testing.T) {
	root := t.TempDir()
	writePkg(t, root, `
schema: 2
name: a
artifact:
  oci: ":{{.Version}}"
install:
  - extract: {}
versions:
  - v2.0.0
  - v1.0.0
`)
	writePkg(t, root, `
schema: 2
name: b
deps:
  - name: a
artifact:
  oci: ":{{.Version}}"
install:
  - extract: {}
versions:
  - v1.0.0
`)
	orders := map[string][]resolve.Tool{
		"pin first": {
			{Key: project.ToolKey{Name: "a"}, Version: "v1.0.0"},
			{Key: project.ToolKey{Name: "b"}},
		},
		"dep first": {
			{Key: project.ToolKey{Name: "b"}},
			{Key: project.ToolKey{Name: "a"}, Version: "v1.0.0"},
		},
	}
	for name, tools := range orders {
		t.Run(name, func(t *testing.T) {
			res, err := resolve.Resolve(context.Background(), tools, namedSources(root))
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if a := entry(t, res, "a"); a.Version != "v1.0.0" || !a.Direct {
				t.Fatalf("entry a: %+v, want pinned v1.0.0", a)
			}
		})
	}
}

func TestResolveBareDepAloneFloatsToLatest(t *testing.T) {
	root := t.TempDir()
	writePkg(t, root, `
schema: 2
name: a
artifact:
  oci: ":{{.Version}}"
install:
  - extract: {}
versions:
  - v2.0.0
  - v1.0.0
`)
	writePkg(t, root, `
schema: 2
name: b
deps:
  - name: a
artifact:
  oci: ":{{.Version}}"
install:
  - extract: {}
versions:
  - v1.0.0
`)
	tools := []resolve.Tool{{Key: project.ToolKey{Name: "b"}}}
	res, err := resolve.Resolve(context.Background(), tools, namedSources(root))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if a := entry(t, res, "a"); a.Version != "v2.0.0" {
		t.Fatalf("unconstrained dep alone should float to latest, got %+v", a)
	}
}

// TestResolveBareDepDefersToCompatRange reconciles a compat-ranged link dep
// first and a bare dep second: the bare edge must adopt the range's
// selection instead of raising a soname conflict.
func TestResolveBareDepDefersToCompatRange(t *testing.T) {
	root := t.TempDir()
	writePkg(t, root, `
schema: 2
name: p1
deps: [{name: openssl, kind: link, compat: "3"}]
artifact: {oci: ":{{.Version}}"}
install: [{extract: {}}]
versions: [v1.0.0]
`)
	writePkg(t, root, `
schema: 2
name: p2
deps: [{name: openssl}]
artifact: {oci: ":{{.Version}}"}
install: [{extract: {}}]
versions: [v1.0.0]
`)
	writePkg(t, root, `
schema: 2
name: openssl
libs: [lib]
artifact: {oci: ":{{.Version}}"}
install: [{extract: {}}]
versions: [v4.0.0, v3.5.1]
`)
	tools := []resolve.Tool{{Key: project.ToolKey{Name: "p1"}}, {Key: project.ToolKey{Name: "p2"}}}
	res, err := resolve.Resolve(context.Background(), tools, namedSources(root))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if v := entry(t, res, "openssl").Version; v != "v3.5.1" {
		t.Fatalf("openssl = %s, want the compat range's v3.5.1", v)
	}
}

// TestResolveDepsBareBuildDepDefersToExactDep mirrors floating semantics on
// the build-deps path: a bare build dep must not float a package above
// another edge's exact version requirement.
func TestResolveDepsBareBuildDepDefersToExactDep(t *testing.T) {
	root := t.TempDir()
	writePkg(t, root, `
schema: 2
name: a
artifact: {oci: ":{{.Version}}"}
install: [{extract: {}}]
versions: [v2.0.0, v1.0.0]
`)
	writePkg(t, root, `
schema: 2
name: b
deps:
  - name: a
    version: v1.0.0
artifact: {oci: ":{{.Version}}"}
install: [{extract: {}}]
versions: [v1.0.0]
`)
	pkg := &spec.Package{
		Schema: 2,
		Name:   "tool",
		Build: &spec.Build{
			Deps: []spec.Dep{
				{Name: "a"},
				{Name: "b"},
			},
		},
	}
	res, err := resolve.ResolveDeps(context.Background(), pkg, pkg.Build.Deps, namedSources(root))
	if err != nil {
		t.Fatalf("ResolveDeps: %v", err)
	}
	if a := entry(t, res, "a"); a.Version != "v1.0.0" {
		t.Fatalf("bare build dep floated a to %s over b's exact v1.0.0", a.Version)
	}
}

// TestResolvePinnedRootWithinCompatRangeKeepsPin proves a compat-ranged
// link dep is satisfied by a direct pin inside its range: the pin must win
// over the range's default highest match, regardless of which side
// reconciles first.
func TestResolvePinnedRootWithinCompatRangeKeepsPin(t *testing.T) {
	root := t.TempDir()
	writePkg(t, root, `
schema: 2
name: app
deps: [{name: gpgme, kind: link, compat: "1"}]
artifact: {oci: ":{{.Version}}"}
install: [{extract: {}}]
versions: [v1.0.0]
`)
	writePkg(t, root, `
schema: 2
name: gpgme
libs: [lib]
artifact: {oci: ":{{.Version}}"}
install: [{extract: {}}]
versions: [2.0.0, 1.24.3, 1.24.2]
`)
	orders := map[string][]resolve.Tool{
		"pin first": {
			{Key: project.ToolKey{Name: "gpgme"}, Version: "1.24.2"},
			{Key: project.ToolKey{Name: "app"}},
		},
		"dep first": {
			{Key: project.ToolKey{Name: "app"}},
			{Key: project.ToolKey{Name: "gpgme"}, Version: "1.24.2"},
		},
	}
	for name, tools := range orders {
		t.Run(name, func(t *testing.T) {
			res, err := resolve.Resolve(context.Background(), tools, namedSources(root))
			if err != nil {
				t.Fatalf("in-range pin must satisfy the compat edge, got error: %v", err)
			}
			if g := entry(t, res, "gpgme"); g.Version != "1.24.2" || !g.Direct {
				t.Fatalf("entry gpgme: %+v, want pinned 1.24.2", g)
			}
		})
	}
}

// TestResolveExactDepOutsideCompatRangeConflicts proves an exact dep edge
// the merged range excludes conflicts in either reconcile order. The
// packages support a single platform so nothing is re-reconciled by a
// second platform walk.
func TestResolveExactDepOutsideCompatRangeConflicts(t *testing.T) {
	root := t.TempDir()
	writePkg(t, root, `
schema: 2
name: p1
platforms: [darwin/arm64]
deps: [{name: openssl, version: v1.1.1}]
artifact: {oci: ":{{.Version}}"}
install: [{extract: {}}]
versions: [v1.0.0]
`)
	writePkg(t, root, `
schema: 2
name: p2
platforms: [darwin/arm64]
deps: [{name: openssl, kind: link, compat: "3"}]
artifact: {oci: ":{{.Version}}"}
install: [{extract: {}}]
versions: [v1.0.0]
`)
	writePkg(t, root, `
schema: 2
name: openssl
platforms: [darwin/arm64]
libs: [lib]
artifact: {oci: ":{{.Version}}"}
install: [{extract: {}}]
versions: [v3.5.1, v1.1.1]
`)
	orders := map[string][]resolve.Tool{
		"exact first": {
			{Key: project.ToolKey{Name: "p1"}},
			{Key: project.ToolKey{Name: "p2"}},
		},
		"compat first": {
			{Key: project.ToolKey{Name: "p2"}},
			{Key: project.ToolKey{Name: "p1"}},
		},
	}
	for name, tools := range orders {
		t.Run(name, func(t *testing.T) {
			_, err := resolve.Resolve(context.Background(), tools, namedSources(root))
			var sce *resolve.CompatConflictError
			if !errors.As(err, &sce) {
				t.Fatalf("want CompatConflictError, got %v", err)
			}
			if sce.Name != "openssl" {
				t.Fatalf("conflict should name openssl: %+v", sce)
			}
		})
	}
}

// TestResolveUnpinnedRootOutsideCompatRangeConflicts proves a version-less
// direct tool demands its resolved latest: a dep's compat range that
// excludes latest conflicts in either reconcile order instead of silently
// floating the tool down into the range. Single-platform packages so no
// second platform walk re-reconciles the root.
func TestResolveUnpinnedRootYieldsToCompatRange(t *testing.T) {
	root := t.TempDir()
	writePkg(t, root, `
schema: 2
name: app
platforms: [darwin/arm64]
deps: [{name: openssl, kind: link, compat: "3"}]
artifact: {oci: ":{{.Version}}"}
install: [{extract: {}}]
versions: [v1.0.0]
`)
	writePkg(t, root, `
schema: 2
name: openssl
platforms: [darwin/arm64]
libs: [lib]
artifact: {oci: ":{{.Version}}"}
install: [{extract: {}}]
versions: [v4.0.0, v3.5.1]
`)
	orders := map[string][]resolve.Tool{
		"root first": {
			{Key: project.ToolKey{Name: "openssl"}},
			{Key: project.ToolKey{Name: "app"}},
		},
		"dep first": {
			{Key: project.ToolKey{Name: "app"}},
			{Key: project.ToolKey{Name: "openssl"}},
		},
	}
	for name, tools := range orders {
		t.Run(name, func(t *testing.T) {
			res, err := resolve.Resolve(context.Background(), tools, namedSources(root))
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if e := entry(t, res, "openssl"); e.Version != "v3.5.1" {
				t.Fatalf("the unpinned root must settle on the range's highest: %+v", e)
			}
		})
	}
}

// TestResolveOrderIndependentConflict proves resolution's outcome is the
// same for every tools permutation when two exact dep versions and a
// compat range meet: the exact divergence conflicts first, canonically.
func TestResolveOrderIndependentConflict(t *testing.T) {
	root := t.TempDir()
	writePkg(t, root, `
schema: 2
name: p1
platforms: [darwin/arm64]
deps: [{name: openssl, version: v1.8.0}]
artifact: {oci: ":{{.Version}}"}
install: [{extract: {}}]
versions: [v1.0.0]
`)
	writePkg(t, root, `
schema: 2
name: p2
platforms: [darwin/arm64]
deps: [{name: openssl, version: v1.9.0}]
artifact: {oci: ":{{.Version}}"}
install: [{extract: {}}]
versions: [v1.0.0]
`)
	writePkg(t, root, `
schema: 2
name: p3
platforms: [darwin/arm64]
deps: [{name: openssl, kind: link, compat: "1.9"}]
artifact: {oci: ":{{.Version}}"}
install: [{extract: {}}]
versions: [v1.0.0]
`)
	writePkg(t, root, `
schema: 2
name: openssl
platforms: [darwin/arm64]
libs: [lib]
artifact: {oci: ":{{.Version}}"}
install: [{extract: {}}]
versions: [v1.9.0, v1.8.0]
`)
	perms := [][]string{
		{"p1", "p2", "p3"}, {"p1", "p3", "p2"}, {"p2", "p1", "p3"},
		{"p2", "p3", "p1"}, {"p3", "p1", "p2"}, {"p3", "p2", "p1"},
	}
	for _, order := range perms {
		t.Run(strings.Join(order, ","), func(t *testing.T) {
			tools := make([]resolve.Tool, len(order))
			for i, n := range order {
				tools[i] = resolve.Tool{Key: project.ToolKey{Name: n}}
			}
			_, err := resolve.Resolve(context.Background(), tools, namedSources(root))
			var sce *resolve.CompatConflictError
			if !errors.As(err, &sce) {
				t.Fatalf("want CompatConflictError, got %v", err)
			}
			if sce.Name != "openssl" || strings.Join(sce.Compats, ",") != "v1.8.0,1.9,v1.9.0" {
				t.Fatalf("conflict should list every requirement canonically: %+v", sce)
			}
		})
	}
}

// TestResolvePinnedRootOutsideCompatRangeConflicts guards the counterpart:
// a pin the range excludes still fails resolution.
func TestResolvePinnedRootOutsideCompatRangeConflicts(t *testing.T) {
	root := t.TempDir()
	writePkg(t, root, `
schema: 2
name: app
deps: [{name: openssl, kind: link, compat: "3"}]
artifact: {oci: ":{{.Version}}"}
install: [{extract: {}}]
versions: [v1.0.0]
`)
	writePkg(t, root, `
schema: 2
name: openssl
libs: [lib]
artifact: {oci: ":{{.Version}}"}
install: [{extract: {}}]
versions: [v3.5.1, v1.1.1]
`)
	tools := []resolve.Tool{
		{Key: project.ToolKey{Name: "openssl"}, Version: "v1.1.1"},
		{Key: project.ToolKey{Name: "app"}},
	}
	_, err := resolve.Resolve(context.Background(), tools, namedSources(root))
	var pce *resolve.PinConflictError
	if !errors.As(err, &pce) {
		t.Fatalf("want PinConflictError, got %v", err)
	}
	if pce.Name != "openssl" || pce.Pinned != "v1.1.1" || pce.Required != "v3.5.1" {
		t.Fatalf("PinConflictError: %+v", pce)
	}
}

func TestResolvePinnedRootDepAgreementResolves(t *testing.T) {
	root := t.TempDir()
	writePkg(t, root, `
schema: 2
name: a
artifact:
  oci: ":{{.Version}}"
install:
  - extract: {}
versions:
  - v2.0.0
  - v1.0.0
`)
	writePkg(t, root, `
schema: 2
name: b
deps:
  - name: a
    version: v2.0.0
artifact:
  oci: ":{{.Version}}"
install:
  - extract: {}
versions:
  - v1.0.0
`)
	tools := []resolve.Tool{
		{Key: project.ToolKey{Name: "a"}, Version: "v2.0.0"},
		{Key: project.ToolKey{Name: "b"}},
	}
	res, err := resolve.Resolve(context.Background(), tools, namedSources(root))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if a := entry(t, res, "a"); a.Version != "v2.0.0" || !a.Direct {
		t.Fatalf("entry a: %+v", a)
	}
}

func TestResolveDepsWalksTheGivenList(t *testing.T) {
	root := t.TempDir()
	writePkg(t, root, `
schema: 2
name: alpha
artifact: {oci: ":{{.Version}}"}
install: [{extract: {}}]
versions: [v1.0.0]
`)
	writePkg(t, root, `
schema: 2
name: beta
artifact: {oci: ":{{.Version}}"}
install: [{extract: {}}]
versions: [v2.0.0]
`)
	pkg := &spec.Package{
		Schema: 2,
		Name:   "tool",
		Deps:   []spec.Dep{{Name: "alpha"}},
		Build:  &spec.Build{Deps: []spec.Dep{{Name: "beta"}}},
	}

	res, err := resolve.ResolveDeps(context.Background(), pkg, pkg.Deps, namedSources(root))
	if err != nil {
		t.Fatalf("ResolveDeps: %v", err)
	}
	if len(res.Entries) != 1 {
		t.Fatalf("want exactly the runtime dep, got %+v", res.Entries)
	}
	if got := entry(t, res, "alpha"); got.Version != "v1.0.0" {
		t.Fatalf("alpha version = %q", got.Version)
	}
}
