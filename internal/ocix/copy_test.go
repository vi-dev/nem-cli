package ocix

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/memory"
	"oras.land/oras-go/v2/content/oci"
	"oras.land/oras-go/v2/registry"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/vi-dev/nem-cli/internal/spec"
)

// TestCopyIndexClosureWithProgressArithmetic proves the callback contract
// against a fixture of known size: total is the index plus its manifest
// count, known from the very first call (before any node is done — proving
// a done tick can never arrive before total, since a node can't be found
// until the index that names it has been fetched); done then covers every
// integer from 1 through total exactly once, regardless of the concurrent
// copy's delivery order.
func TestCopyIndexClosureWithProgressArithmetic(t *testing.T) {
	ctx := context.Background()
	store, err := oci.New(t.TempDir())
	if err != nil {
		t.Fatalf("oci.New: %v", err)
	}
	const n = 5
	PushFakeCatalogForTest(t, store, fakeCatalogEntries(n), SchemaVersion)

	rec := &progressRecorder{}
	if _, err := CopyIndexClosureWithProgress(ctx, store, "v2", memory.New(), "v2", rec.record); err != nil {
		t.Fatalf("CopyIndexClosureWithProgress: %v", err)
	}

	wantTotal := int64(n + 1) // the index itself, plus one per package manifest
	seen := map[int64]int{}
	for _, c := range rec.snapshot() {
		if c.total != wantTotal {
			t.Fatalf("call %+v: total != %d (total must be known from the first call onward)", c, wantTotal)
		}
		seen[c.done]++
	}
	if seen[0] != 1 {
		t.Fatalf("done=0 (the total-known announcement) seen %d times, want exactly 1", seen[0])
	}
	for d := int64(1); d <= wantTotal; d++ {
		if seen[d] != 1 {
			t.Fatalf("done=%d seen %d times, want exactly 1", d, seen[d])
		}
	}
	if len(seen) != int(wantTotal)+1 {
		t.Fatalf("saw %d distinct done values, want %d (0..%d)", len(seen), wantTotal+1, wantTotal)
	}
}

// TestCopyIndexClosureWithProgressCountsSkippedManifestsToo proves the
// OnCopySkipped wiring drives the same counting PostCopy does: a
// destination pre-populated with every manifest's raw content, but not the
// index itself, makes oras find each manifest already present (skipped)
// while the index is still copied fresh — total still comes from the
// index fetch (its own successors are found regardless of what's already
// at dst; only the recursive walk into an already-present node's own
// successors is skipped), and done still ticks through every node once.
//
// (A destination pre-populated with the index itself was considered and
// rejected as a fixture: oras.Copy checks dst for the root before finding
// its successors at all, so an already-present root short-circuits the
// whole walk — no fetch, no successors found, nothing to report. That
// can't happen in production: staging always copies into a fresh,
// just-created store.)
func TestCopyIndexClosureWithProgressCountsSkippedManifestsToo(t *testing.T) {
	ctx := context.Background()
	store, err := oci.New(t.TempDir())
	if err != nil {
		t.Fatalf("oci.New: %v", err)
	}
	const n = 3
	PushFakeCatalogForTest(t, store, fakeCatalogEntries(n), SchemaVersion)

	idx, _, err := FetchCatalogIndex(ctx, store, "v2")
	if err != nil {
		t.Fatalf("FetchIndex: %v", err)
	}

	dst := memory.New()
	for _, m := range idx.Manifests {
		data, err := content.FetchAll(ctx, store, m)
		if err != nil {
			t.Fatalf("fetch manifest %s: %v", m.Digest, err)
		}
		if err := dst.Push(ctx, m, bytes.NewReader(data)); err != nil {
			t.Fatalf("pre-populate dst with manifest %s: %v", m.Digest, err)
		}
	}

	rec := &progressRecorder{}
	if _, err := CopyIndexClosureWithProgress(ctx, store, "v2", dst, "v2", rec.record); err != nil {
		t.Fatalf("CopyIndexClosureWithProgress: %v", err)
	}

	wantTotal := int64(n + 1)
	seen := map[int64]int{}
	for _, c := range rec.snapshot() {
		if c.total != wantTotal {
			t.Fatalf("call %+v: total != %d", c, wantTotal)
		}
		seen[c.done]++
	}
	for d := int64(0); d <= wantTotal; d++ {
		if seen[d] != 1 {
			t.Fatalf("done=%d seen %d times, want exactly 1 (pre-existing manifests must still tick, via OnCopySkipped)", d, seen[d])
		}
	}
}

// countingTarget wraps an oras.ReadOnlyTarget, counting Resolve and Fetch
// calls so a test can prove the progress-observing wrapper adds none.
type countingTarget struct {
	oras.ReadOnlyTarget
	resolves atomic.Int64
	fetches  atomic.Int64
}

func (c *countingTarget) Resolve(ctx context.Context, ref string) (ocispec.Descriptor, error) {
	c.resolves.Add(1)
	return c.ReadOnlyTarget.Resolve(ctx, ref)
}

func (c *countingTarget) Fetch(ctx context.Context, d ocispec.Descriptor) (io.ReadCloser, error) {
	c.fetches.Add(1)
	return c.ReadOnlyTarget.Fetch(ctx, d)
}

// TestCopyIndexClosureWithProgressNoExtraRequests proves the total comes
// from observing the copy's own first fetch — the index content oras.Copy
// already reads to discover its successors — rather than any request of
// this package's own: a progress-wired copy makes exactly the same
// Resolve/Fetch count against the source as the plain, progress-less copy
// does over an identical fixture.
func TestCopyIndexClosureWithProgressNoExtraRequests(t *testing.T) {
	ctx := context.Background()
	newFixture := func(t *testing.T) *oci.Store {
		t.Helper()
		store, err := oci.New(t.TempDir())
		if err != nil {
			t.Fatalf("oci.New: %v", err)
		}
		PushFakeCatalogForTest(t, store, fakeCatalogEntries(5), SchemaVersion)
		return store
	}

	baseline := &countingTarget{ReadOnlyTarget: newFixture(t)}
	if _, err := CopyIndexClosure(ctx, baseline, "v2", memory.New(), "v2"); err != nil {
		t.Fatalf("CopyIndexClosure: %v", err)
	}

	withProgress := &countingTarget{ReadOnlyTarget: newFixture(t)}
	if _, err := CopyIndexClosureWithProgress(ctx, withProgress, "v2", memory.New(), "v2", func(int64, int64) {}); err != nil {
		t.Fatalf("CopyIndexClosureWithProgress: %v", err)
	}

	if got, want := withProgress.resolves.Load(), baseline.resolves.Load(); got != want {
		t.Fatalf("resolves = %d, want exactly the no-progress baseline %d", got, want)
	}
	if got, want := withProgress.fetches.Load(), baseline.fetches.Load(); got != want {
		t.Fatalf("fetches = %d, want exactly the no-progress baseline %d", got, want)
	}
}

// closureContent is one fake HTTP registry's full readable state: every
// descriptor in a closure, by digest, plus the tags naming its root.
type closureContent struct {
	blobs map[string][]byte // digest string -> raw content
	types map[string]string // digest string -> media type
	tags  map[string]string // tag -> digest string
}

// snapshotClosure walks root's full successor graph in src (the same walk
// oras.Copy itself performs) and returns every node's content, keyed by
// digest, plus tag pointing at root.
func snapshotClosure(ctx context.Context, src oras.ReadOnlyTarget, root ocispec.Descriptor, tag string) (*closureContent, error) {
	cc := &closureContent{blobs: map[string][]byte{}, types: map[string]string{}, tags: map[string]string{tag: root.Digest.String()}}
	var walk func(desc ocispec.Descriptor) error
	walk = func(desc ocispec.Descriptor) error {
		key := desc.Digest.String()
		if _, ok := cc.blobs[key]; ok {
			return nil
		}
		data, err := content.FetchAll(ctx, src, desc)
		if err != nil {
			return err
		}
		cc.blobs[key] = data
		cc.types[key] = desc.MediaType
		successors, err := content.Successors(ctx, src, desc)
		if err != nil {
			return err
		}
		for _, s := range successors {
			if err := walk(s); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(root); err != nil {
		return nil, err
	}
	return cc, nil
}

// newFakeRegistry serves cc over HTTP well enough for a real
// remote.Repository to resolve, fetch-by-reference, and walk a full
// closure: /v2/<repo>/manifests/<tag-or-digest> and
// /v2/<repo>/blobs/<digest>, both HEAD and GET, from the same
// digest-addressed content map (a real registry's manifest and blob
// endpoints are both just content-addressed reads once resolved).
func newFakeRegistry(t *testing.T, repo string, cc *closureContent) *httptest.Server {
	t.Helper()
	serveByDigest := func(w http.ResponseWriter, r *http.Request, digestOrTag string) {
		key := digestOrTag
		if d, ok := cc.tags[digestOrTag]; ok {
			key = d
		}
		data, ok := cc.blobs[key]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", cc.types[key])
		w.Header().Set("Docker-Content-Digest", key)
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		if r.Method == http.MethodGet {
			w.Write(data)
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/v2/"+repo+"/manifests/", func(w http.ResponseWriter, r *http.Request) {
		serveByDigest(w, r, strings.TrimPrefix(r.URL.Path, "/v2/"+repo+"/manifests/"))
	})
	mux.HandleFunc("/v2/"+repo+"/blobs/", func(w http.ResponseWriter, r *http.Request) {
		serveByDigest(w, r, strings.TrimPrefix(r.URL.Path, "/v2/"+repo+"/blobs/"))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// TestCopyIndexClosureWithProgressAgainstRealRemoteRepository proves
// progress reporting survives against a real *remote.Repository, not just
// the in-memory/local-layout stores every other test in this file uses.
// remote.Repository implements registry.ReferenceFetcher, which
// oras.Copy's resolveRoot type-asserts for and, when present, fetches the
// root by reference directly instead of a plain Resolve+Fetch —
// fetchObserver embeds the ReadOnlyTarget interface, so that assertion
// fails against the wrapper (it doesn't promote FetchReference) and the
// plain path runs instead. This test is the evidence that the plain path
// still reaches fetchObserver.Fetch and still reports correct progress.
func TestCopyIndexClosureWithProgressAgainstRealRemoteRepository(t *testing.T) {
	ctx := context.Background()
	local, err := oci.New(t.TempDir())
	if err != nil {
		t.Fatalf("oci.New: %v", err)
	}
	const n = 6
	root := PushFakeCatalogForTest(t, local, fakeCatalogEntries(n), SchemaVersion)

	cc, err := snapshotClosure(ctx, local, root, "v2")
	if err != nil {
		t.Fatalf("snapshotClosure: %v", err)
	}
	srv := newFakeRegistry(t, "repo", cc)
	host := srv.Listener.Addr().String()

	repo, err := remote.NewRepository(host + "/repo")
	if err != nil {
		t.Fatalf("remote.NewRepository: %v", err)
	}
	repo.PlainHTTP = true
	if _, ok := any(repo).(registry.ReferenceFetcher); !ok {
		t.Fatal("fixture bug: *remote.Repository must implement registry.ReferenceFetcher for this test to mean anything")
	}

	rec := &progressRecorder{}
	if _, err := CopyIndexClosureWithProgress(ctx, repo, "v2", memory.New(), "v2", rec.record); err != nil {
		t.Fatalf("CopyIndexClosureWithProgress against a real remote.Repository: %v", err)
	}

	wantTotal := int64(n + 1)
	seen := map[int64]int{}
	for _, c := range rec.snapshot() {
		if c.total != 0 && c.total != wantTotal {
			t.Fatalf("call %+v: total != %d", c, wantTotal)
		}
		seen[c.done]++
	}
	if seen[0] != 1 {
		t.Fatalf("done=0 (the total-known announcement) seen %d times, want exactly 1 — the remote fast path must not skip fetchObserver.Fetch", seen[0])
	}
	for d := int64(1); d <= wantTotal; d++ {
		if seen[d] != 1 {
			t.Fatalf("done=%d seen %d times, want exactly 1", d, seen[d])
		}
	}
}

func TestCopyTagCopiesClosureAndVerifiesDigest(t *testing.T) {
	ctx := context.Background()
	src, err := oci.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	PushFakeArchive(t, src, "v1.0.0", map[string][]byte{"linux/amd64": []byte("payload")})
	srcDesc, err := src.Resolve(ctx, "v1.0.0")
	if err != nil {
		t.Fatal(err)
	}

	dst := memory.New()
	got, err := CopyTag(ctx, src, dst, "v1.0.0")
	if err != nil {
		t.Fatalf("CopyTag: %v", err)
	}
	if got.Digest != srcDesc.Digest {
		t.Fatalf("copied digest = %s, want %s", got.Digest, srcDesc.Digest)
	}
	dstDesc, err := dst.Resolve(ctx, "v1.0.0")
	if err != nil {
		t.Fatalf("resolve on dst: %v", err)
	}
	if dstDesc.Digest != srcDesc.Digest {
		t.Fatalf("dst tag digest = %s, want %s", dstDesc.Digest, srcDesc.Digest)
	}

	path, err := PullArchiveFrom(ctx, dst, "v1.0.0", spec.Platform{OS: "linux", Arch: "amd64"}, t.TempDir())
	if err != nil {
		t.Fatalf("PullArchiveFrom on copied dst: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "payload" {
		t.Fatalf("copied archive bytes = %q, want %q", data, "payload")
	}
}

// flakyPushTarget fails the very first Push it sees (any blob), then
// delegates every call after that — reproducing one transient registry
// error partway through a copy.
type flakyPushTarget struct {
	oras.Target
	mu     sync.Mutex
	failed bool
}

func (f *flakyPushTarget) Push(ctx context.Context, d ocispec.Descriptor, r io.Reader) error {
	f.mu.Lock()
	shouldFail := !f.failed
	f.failed = true
	f.mu.Unlock()
	if shouldFail {
		io.Copy(io.Discard, r)
		return errors.New("simulated transient push failure")
	}
	return f.Target.Push(ctx, d, r)
}

func TestCopyTagRetriesAfterTransientPushFailure(t *testing.T) {
	ctx := context.Background()
	src, err := oci.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	PushFakeArchive(t, src, "v1.0.0", map[string][]byte{"linux/amd64": []byte("payload")})

	dst := &flakyPushTarget{Target: memory.New()}
	got, err := CopyTag(ctx, src, dst, "v1.0.0")
	if err != nil {
		t.Fatalf("CopyTag: %v", err)
	}
	dstDesc, err := dst.Target.(*memory.Store).Resolve(ctx, "v1.0.0")
	if err != nil {
		t.Fatalf("resolve on dst: %v", err)
	}
	if got.Digest != dstDesc.Digest {
		t.Fatalf("returned digest = %s, want %s", got.Digest, dstDesc.Digest)
	}
}

func TestCopyTagFailsAfterRetriesExhausted(t *testing.T) {
	ctx := context.Background()
	src, err := oci.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	PushFakeArchive(t, src, "v1.0.0", map[string][]byte{"linux/amd64": []byte("payload")})

	dst := &alwaysFailPushTarget{Target: memory.New()}
	if _, err := CopyTag(ctx, src, dst, "v1.0.0"); err == nil {
		t.Fatal("want error once every push attempt fails, got nil")
	}
	if dst.calls < retryAttempts {
		t.Fatalf("push calls = %d, want at least %d (one per attempt)", dst.calls, retryAttempts)
	}
}

type alwaysFailPushTarget struct {
	oras.Target
	mu    sync.Mutex
	calls int
}

func (f *alwaysFailPushTarget) Push(ctx context.Context, d ocispec.Descriptor, r io.Reader) error {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	io.Copy(io.Discard, r)
	return errors.New("simulated permanent push failure")
}

// flakyResolveTarget answers the first Resolve call with a descriptor for
// unrelated content, then delegates — reproducing a concurrent writer that
// moves the tag between CopyTag's copy and its own post-copy verify.
type flakyResolveTarget struct {
	oras.Target
	mu    sync.Mutex
	calls int
	wrong ocispec.Descriptor
}

func (f *flakyResolveTarget) Resolve(ctx context.Context, ref string) (ocispec.Descriptor, error) {
	f.mu.Lock()
	f.calls++
	first := f.calls == 1
	f.mu.Unlock()
	if first {
		return f.wrong, nil
	}
	return f.Target.Resolve(ctx, ref)
}

func TestCopyTagRetriesOnPostCopyDigestMismatch(t *testing.T) {
	ctx := context.Background()
	src, err := oci.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	PushFakeArchive(t, src, "v1.0.0", map[string][]byte{"linux/amd64": []byte("payload")})
	srcDesc, err := src.Resolve(ctx, "v1.0.0")
	if err != nil {
		t.Fatal(err)
	}

	inner := memory.New()
	wrong := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageIndex, Digest: "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", Size: 0}
	dst := &flakyResolveTarget{Target: inner, wrong: wrong}

	got, err := CopyTag(ctx, src, dst, "v1.0.0")
	if err != nil {
		t.Fatalf("CopyTag: %v", err)
	}
	if got.Digest != srcDesc.Digest {
		t.Fatalf("digest = %s, want %s", got.Digest, srcDesc.Digest)
	}
	if dst.calls < 2 {
		t.Fatalf("resolve calls = %d, want at least 2 (mismatch then a retried verify)", dst.calls)
	}
}
