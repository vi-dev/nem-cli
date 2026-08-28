package publish

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/oci"
	"oras.land/oras-go/v2/errdef"

	"github.com/vi-dev/nem-cli/internal/ocix"
	"github.com/vi-dev/nem-cli/internal/report"
)

// swapOpenTarget swaps the target opener Publish uses for f, returning a
// closure that restores the previous one.
func swapOpenTarget(f func(context.Context, string) (oras.Target, error)) (restore func()) {
	return SetTargetOpener(f)
}

// swapNow swaps the clock Publish uses for the release tag for f,
// returning a closure that restores the previous one.
func swapNow(f func() time.Time) (restore func()) {
	prev := nowFunc
	nowFunc = f
	return func() { nowFunc = prev }
}

// newStore opens a fresh OCI layout store in a temp dir, returning both
// the store and its backing directory so tests can inspect the layout
// directly.
func newStore(t *testing.T) (*oci.Store, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := oci.New(dir)
	if err != nil {
		t.Fatalf("oci.New: %v", err)
	}
	return s, dir
}

func assertTagResolves(t *testing.T, store oras.ReadOnlyTarget, tag string) {
	t.Helper()
	if _, err := store.Resolve(context.Background(), tag); err != nil {
		t.Fatalf("resolve tag %s: %v", tag, err)
	}
}

// countBlobs counts content-addressed blob files under storeDir's OCI
// layout, giving a store-agnostic signal of how many distinct pieces of
// content have ever been pushed.
func countBlobs(t *testing.T, storeDir string) int {
	t.Helper()
	root := filepath.Join(storeDir, "blobs")
	count := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if !d.IsDir() {
			count++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return count
}

// countingTarget wraps an oras.Target, counting Exists calls per digest
// so tests can distinguish the skip-unchanged short-circuit from a real
// push attempt without inspecting store internals.
type countingTarget struct {
	oras.Target
	mu     sync.Mutex
	exists map[string]int
}

func newCountingTarget(t oras.Target) *countingTarget {
	return &countingTarget{Target: t, exists: map[string]int{}}
}

func (c *countingTarget) Exists(ctx context.Context, d ocispec.Descriptor) (bool, error) {
	c.mu.Lock()
	c.exists[d.Digest.String()]++
	c.mu.Unlock()
	return c.Target.Exists(ctx, d)
}

func (c *countingTarget) existsCount(digest string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.exists[digest]
}

func TestPublishHappyPathAndIdempotency(t *testing.T) {
	ctx := context.Background()
	dir := writeCatalog(t, map[string]string{"go": validGoPkg, "kubectl": pkgNamed("kubectl")})
	store, storeDir := newStore(t)

	defer swapOpenTarget(func(context.Context, string) (oras.Target, error) { return store, nil })()
	defer swapNow(func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) })()

	if err := Publish(ctx, dir, "example.com/cat", Options{Tags: []string{"v2"}}, report.Discard()); err != nil {
		t.Fatalf("publish: %v", err)
	}
	// Index resolvable under both the moving tag and the immutable release tag.
	assertTagResolves(t, store, "v2")
	assertTagResolves(t, store, "v2.20260102T030405Z")

	blobsAfterFirst := countBlobs(t, storeDir)

	// Re-publish unchanged: manifests skipped, tag still moves, no error.
	if err := Publish(ctx, dir, "example.com/cat", Options{Tags: []string{"v2"}}, report.Discard()); err != nil {
		t.Fatal(err)
	}
	// Same content ⇒ store blob set unchanged.
	if got := countBlobs(t, storeDir); got != blobsAfterFirst {
		t.Fatalf("re-publish pushed new blobs: %d → %d", blobsAfterFirst, got)
	}
	assertTagResolves(t, store, "v2")
	assertTagResolves(t, store, "v2.20260102T030405Z")
}

func TestPublishLintGateBlocksAllWrites(t *testing.T) {
	ctx := context.Background()
	dir := writeCatalog(t, map[string]string{"go": pkgWithEnv("PATH", "x")}) // reserved env → lint fails
	defer swapOpenTarget(func(context.Context, string) (oras.Target, error) {
		t.Fatal("openTarget must not be called when the lint gate blocks a publish")
		return nil, nil
	})()

	err := Publish(ctx, dir, "example.com/cat", Options{}, report.Discard())
	if err == nil {
		t.Fatal("expected lint gate to fail publish")
	}
}

func TestPublishDryRunWritesNothing(t *testing.T) {
	ctx := context.Background()
	dir := writeCatalog(t, map[string]string{"go": validGoPkg})
	defer swapOpenTarget(func(context.Context, string) (oras.Target, error) {
		t.Fatal("openTarget must not be called during a dry run")
		return nil, nil
	})()

	if err := Publish(ctx, dir, "example.com/cat", Options{DryRun: true}, report.Discard()); err != nil {
		t.Fatal(err)
	}
}

func TestPublishForceDisablesSkip(t *testing.T) {
	ctx := context.Background()
	dir := writeCatalog(t, map[string]string{"go": validGoPkg})
	store, _ := newStore(t)
	ct := newCountingTarget(store)

	defer swapOpenTarget(func(context.Context, string) (oras.Target, error) { return ct, nil })()
	defer swapNow(func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) })()

	if err := Publish(ctx, dir, "example.com/cat", Options{}, report.Discard()); err != nil {
		t.Fatalf("initial publish: %v", err)
	}

	layerDigest := content.NewDescriptorFromBytes(ocix.MediaTypePkg, []byte(validGoPkg)).Digest.String()

	beforeSkip := ct.existsCount(layerDigest)
	if err := Publish(ctx, dir, "example.com/cat", Options{}, report.Discard()); err != nil {
		t.Fatalf("skip-unchanged publish: %v", err)
	}
	if got := ct.existsCount(layerDigest) - beforeSkip; got != 0 {
		t.Fatalf("skip-unchanged path must never check the layer blob, got %d new Exists calls", got)
	}

	beforeForce := ct.existsCount(layerDigest)
	if err := Publish(ctx, dir, "example.com/cat", Options{Force: true}, report.Discard()); err != nil {
		t.Fatalf("force publish: %v", err)
	}
	if got := ct.existsCount(layerDigest) - beforeForce; got != 1 {
		t.Fatalf("force must bypass the skip decision and re-attempt the push, got %d new layer Exists calls", got)
	}
}

func TestPublishDefaultsTagsToV2(t *testing.T) {
	ctx := context.Background()
	dir := writeCatalog(t, map[string]string{"go": validGoPkg})
	store, _ := newStore(t)

	defer swapOpenTarget(func(context.Context, string) (oras.Target, error) { return store, nil })()
	defer swapNow(func() time.Time { return time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC) })()

	if err := Publish(ctx, dir, "example.com/cat", Options{}, report.Discard()); err != nil {
		t.Fatal(err)
	}
	assertTagResolves(t, store, "v2")
	assertTagResolves(t, store, "v2.20260304T050607Z")
}

func TestPublishRejectsTaggedRef(t *testing.T) {
	ctx := context.Background()
	dir := writeCatalog(t, map[string]string{"go": validGoPkg})
	defer swapOpenTarget(func(context.Context, string) (oras.Target, error) {
		t.Fatal("openTarget must not be called for an invalid ref")
		return nil, nil
	})()

	err := Publish(ctx, dir, "example.com/cat:v2", Options{}, report.Discard())
	if err == nil {
		t.Fatal("expected ValidateBaseRef to reject a tagged ref")
	}
	if !strings.Contains(err.Error(), "bare repository ref") {
		t.Fatalf("error should name the bare-ref requirement, got: %v", err)
	}
}

func TestPublishSingleFile(t *testing.T) {
	ctx := context.Background()
	dir := writeCatalog(t, map[string]string{"go": validGoPkg})
	store, _ := newStore(t)

	defer swapOpenTarget(func(context.Context, string) (oras.Target, error) { return store, nil })()
	defer swapNow(func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) })()

	if err := Publish(ctx, filepath.Join(dir, "pkgs", "go", "pkg.yaml"), "example.com/cat", Options{}, report.Discard()); err != nil {
		t.Fatal(err)
	}
	assertTagResolves(t, store, "v2")
}

func TestPublishTargetOpenFailureLeavesTagsUntouched(t *testing.T) {
	ctx := context.Background()
	dir := writeCatalog(t, map[string]string{"go": validGoPkg})
	wantErr := errFailingOpen{}
	defer swapOpenTarget(func(context.Context, string) (oras.Target, error) { return nil, wantErr })()

	if err := Publish(ctx, dir, "example.com/cat", Options{}, report.Discard()); err == nil {
		t.Fatal("expected the target-open failure to surface")
	}
}

type errFailingOpen struct{}

func (errFailingOpen) Error() string { return "open failed" }

// failOnManifestPushTarget wraps an oras.Target, rejecting every push of
// an image-manifest blob while letting every other push (layers, the
// shared config, the catalog index) through unchanged. It reproduces a
// failure partway through a multi-package publish: some blobs may have
// already landed in the target, but the manifest push itself — and
// everything after it, including the index and tag moves — never
// happens.
type failOnManifestPushTarget struct {
	oras.Target
}

func (f failOnManifestPushTarget) Push(ctx context.Context, d ocispec.Descriptor, r io.Reader) error {
	if d.MediaType == ocispec.MediaTypeImageManifest {
		return errors.New("simulated manifest push failure")
	}
	return f.Target.Push(ctx, d, r)
}

// TestPublishMidPushFailureLeavesTagsUntouched proves the tag-move-last
// guarantee against an actual push-path failure, not just a failure to
// open the target: a second publish attempt whose one changed package
// fails its manifest push must leave the "v2" tag pointing at the index
// from the prior successful publish, and must never create the release
// tag the failed attempt would have applied.
func TestPublishMidPushFailureLeavesTagsUntouched(t *testing.T) {
	ctx := context.Background()
	store, _ := newStore(t)

	dirA := writeCatalog(t, map[string]string{"go": validGoPkg, "kubectl": pkgNamed("kubectl")})
	func() {
		defer swapOpenTarget(func(context.Context, string) (oras.Target, error) { return store, nil })()
		defer swapNow(func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) })()
		if err := Publish(ctx, dirA, "example.com/cat", Options{}, report.Discard()); err != nil {
			t.Fatalf("initial publish: %v", err)
		}
	}()

	indexA, err := store.Resolve(ctx, "v2")
	if err != nil {
		t.Fatalf("resolve v2 after initial publish: %v", err)
	}

	// "go" changes so it needs a real manifest push (skip-unchanged would
	// otherwise leave nothing for failOnManifestPushTarget to reject);
	// "kubectl" is unchanged and stays on the skip-unchanged path.
	changedGoPkg := strings.Replace(validGoPkg,
		"description: The Go programming language",
		"description: The Go programming language, updated",
		1)
	dirB := writeCatalog(t, map[string]string{"go": changedGoPkg, "kubectl": pkgNamed("kubectl")})

	defer swapOpenTarget(func(context.Context, string) (oras.Target, error) {
		return failOnManifestPushTarget{Target: store}, nil
	})()
	// A distinct release timestamp from the first publish so the failed
	// attempt's would-be release tag is independently checkable below.
	defer swapNow(func() time.Time { return time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC) })()

	if err := Publish(ctx, dirB, "example.com/cat", Options{}, report.Discard()); err == nil {
		t.Fatal("expected the mid-push manifest failure to surface")
	}

	indexAfter, err := store.Resolve(ctx, "v2")
	if err != nil {
		t.Fatalf("resolve v2 after failed publish: %v", err)
	}
	if indexAfter.Digest != indexA.Digest {
		t.Fatalf("tag v2 moved despite a mid-push failure: %s -> %s", indexA.Digest, indexAfter.Digest)
	}

	if _, err := store.Resolve(ctx, "v2.20260203T040506Z"); !errors.Is(err, errdef.ErrNotFound) {
		t.Fatalf("the failed publish's release tag must never be created, got %v", err)
	}
}
