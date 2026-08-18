package resolve_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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

func TestResolveVersionConflictHighestWins(t *testing.T) {
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
	tools := []resolve.Tool{{Key: project.ToolKey{Name: "p1"}}, {Key: project.ToolKey{Name: "p2"}}}
	res, err := resolve.Resolve(context.Background(), tools, namedSources(root))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	d := entry(t, res, "d")
	if d.Version != "v2.0.0" {
		t.Fatalf("want highest version v2.0.0, got %s", d.Version)
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

// TestResolveEqualVersionTieBreakFirstProcessedWins proves the deterministic
// tie-break documented on reconcile: when a catalog-pinned direct tool and
// an unprefixed dep both resolve "shared" to the same version, the direct
// tool's attribution wins because roots are reconciled before their own
// dependency subtree is walked. Swapping which tool is listed first flips
// the winner, showing the rule tracks processing order rather than
// favoring "direct" or a particular catalog.
func TestResolveEqualVersionTieBreakFirstProcessedWins(t *testing.T) {
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
	if sharedSwapped.Catalog != "a" {
		t.Fatalf("swapping tool order should flip the tie's winner to catalog a, got %+v", sharedSwapped)
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

// TestResolveBuildRolesRunVsLink mirrors TestResolveRolesRunVsLink but for
// build.deps: ResolveBuild must assign roles via the same edge path
// (edgeContribution), not the direct-tool path.
func TestResolveBuildRolesRunVsLink(t *testing.T) {
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
	res, err := resolve.ResolveBuild(context.Background(), pkg, namedSources(root))
	if err != nil {
		t.Fatalf("ResolveBuild: %v", err)
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
	var sce *resolve.SonameConflictError
	if !errors.As(err, &sce) {
		t.Fatalf("want SonameConflictError, got %v", err)
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
