package fill

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/memory"

	"github.com/vi-dev/nem-cli/internal/home"
	"github.com/vi-dev/nem-cli/internal/publish"
	"github.com/vi-dev/nem-cli/internal/report"
)

// fakeReporter records every task, Info, and Warn call; all its methods are
// safe for concurrent use across the package goroutines Run spawns.
type fakeReporter struct {
	mu    sync.Mutex
	tasks []*fakeTask
	infos []string
	warns []string
}

func (r *fakeReporter) Info(format string, a ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.infos = append(r.infos, fmt.Sprintf(format, a...))
}

func (r *fakeReporter) Warn(format string, a ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.warns = append(r.warns, fmt.Sprintf(format, a...))
}

func (r *fakeReporter) Debug(string, ...any) {}

func (r *fakeReporter) Task(label string) report.Task {
	t := &fakeTask{label: label}
	r.mu.Lock()
	r.tasks = append(r.tasks, t)
	r.mu.Unlock()
	return t
}

func (r *fakeReporter) taskFor(label string) *fakeTask {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, t := range r.tasks {
		if t.label == label {
			return t
		}
	}
	return nil
}

func (r *fakeReporter) snapshotWarns() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.warns...)
}

func (r *fakeReporter) snapshotInfos() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.infos...)
}

// countCall records one Count(done, total) call.
type countCall struct{ done, total int }

type fakeTask struct {
	label string

	mu        sync.Mutex
	statuses  []string
	counts    []countCall
	done      bool
	failed    bool
	discarded bool
	outcome   string
}

func (t *fakeTask) Status(segment string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.statuses = append(t.statuses, segment)
}
func (t *fakeTask) Progress(int64, int64) {}

func (t *fakeTask) Count(done, total int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.counts = append(t.counts, countCall{done, total})
}

func (t *fakeTask) Done(outcome string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.done = true
	t.outcome = outcome
}

func (t *fakeTask) Fail(outcome string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.failed = true
	t.outcome = outcome
}

func (t *fakeTask) Discard() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.discarded = true
}

func (t *fakeTask) snapshot() (done, failed bool, outcome string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.done, t.failed, t.outcome
}

func (t *fakeTask) wasDiscarded() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.discarded
}

func (t *fakeTask) snapshotCounts() []countCall {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]countCall(nil), t.counts...)
}

func (t *fakeTask) snapshotStatuses() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.statuses...)
}

// countingTarget wraps an oras.ReadOnlyTarget, counting Resolve and Fetch
// calls so a test can prove a read path touched the wrapped target a
// bounded number of times regardless of how much work happens downstream
// of it.
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

// indexPushCountingTarget wraps an oras.Target, counting Push calls whose
// descriptor is an image index (an archive tag's own manifest, as
// opposed to the per-platform layer/manifest blobs PublishArchiveLayerFile
// pushes) and Tag calls — the two operations a batched index commit
// performs at most once per (package, version), regardless of how many
// platforms that version needed.
type indexPushCountingTarget struct {
	oras.Target
	indexPushes atomic.Int64
	tags        atomic.Int64
}

func (c *indexPushCountingTarget) Push(ctx context.Context, d ocispec.Descriptor, r io.Reader) error {
	if d.MediaType == ocispec.MediaTypeImageIndex {
		c.indexPushes.Add(1)
	}
	return c.Target.Push(ctx, d, r)
}

func (c *indexPushCountingTarget) Tag(ctx context.Context, d ocispec.Descriptor, ref string) error {
	c.tags.Add(1)
	return c.Target.Tag(ctx, d, ref)
}

// newCatalog publishes pkgs (name -> pkg.yaml content) into a fresh
// in-memory target via the real Publish path and returns it alongside the
// tag its default "v2" moving tag resolves to.
func newCatalog(t *testing.T, pkgs map[string]string) (oras.Target, string) {
	t.Helper()
	target := memory.New()
	publish.PublishCatalogForTest(t, target, "example.com/cat", pkgs)
	return target, "v2"
}

// wireCatalog injects src as the catalog opener Run uses, ignoring
// whatever ref string a caller passes — tests address the fixed fixture
// directly instead of through ref parsing.
func wireCatalog(t *testing.T, src oras.ReadOnlyTarget, srcTag string) {
	t.Helper()
	t.Cleanup(SetCatalogOpener(func(string) (oras.ReadOnlyTarget, string, error) {
		return src, srcTag, nil
	}))
}

// archiveFixtures holds per-package archive stores, created lazily so a
// package absent from the map still resolves to an empty
// (archive-not-found) store rather than a nil pointer.
type archiveFixtures struct {
	mu     sync.Mutex
	stores map[string]oras.Target
	opened []string
}

func newArchiveFixtures() *archiveFixtures {
	return &archiveFixtures{stores: map[string]oras.Target{}}
}

func (f *archiveFixtures) set(name string, target oras.Target) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stores[name] = target
}

func (f *archiveFixtures) open(name string) oras.Target {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.opened = append(f.opened, name)
	if s, ok := f.stores[name]; ok {
		return s
	}
	s := memory.New()
	f.stores[name] = s
	return s
}

func (f *archiveFixtures) openedNames() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.opened...)
}

// wireArchives injects fixtures as the per-package archives opener Run
// uses.
func wireArchives(t *testing.T, fixtures *archiveFixtures) {
	t.Helper()
	t.Cleanup(SetArchivesOpener(func(_, name string) (oras.Target, error) {
		return fixtures.open(name), nil
	}))
}

// upstream is a hermetic stand-in for a package's upstream artifact host:
// an httptest server serving fixed bodies by path, counting every request
// it receives so a dry-run test can prove zero upstream contact.
type upstream struct {
	*httptest.Server
	mu    sync.Mutex
	files map[string][]byte
	hits  atomic.Int64
}

func newUpstream(t *testing.T) *upstream {
	t.Helper()
	u := &upstream{files: map[string][]byte{}}
	u.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.hits.Add(1)
		u.mu.Lock()
		body, ok := u.files[r.URL.Path]
		u.mu.Unlock()
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write(body)
	}))
	t.Cleanup(u.Close)
	return u
}

func (u *upstream) set(path string, body []byte) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.files[path] = body
}

// wireUpstream sets u's client as fill's upstream HTTP client for the
// duration of the test.
func wireUpstream(t *testing.T, u *upstream) {
	t.Helper()
	t.Cleanup(SetHTTPClient(u.Client()))
}

// sha256Hex hexes b's sha256 digest, for pinning a fixture payload's
// checksum in a pkg.yaml.
func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// urlPkg builds a minimal schema-2 pkg.yaml for a url-fetched package whose
// every supported platform shares one url template and one pinned sha256 —
// convenient for a fixture where every platform serves byte-identical
// content.
func urlPkg(name, version, urlTemplate, digestHex string) string {
	return string(publish.URLPkgYAML(name, version, urlTemplate, publish.UniformSha256(digestHex)))
}

// ociPkg builds a minimal schema-2 pkg.yaml for a prebuilt/source-built
// (oci-artifact) package: fill must report it not-fillable.
func ociPkg(name, version string) string {
	return string(publish.OCIPkgYAML(name, version))
}

// newHome returns a home.Home rooted at a fresh temp directory, without
// going through NEM_HOME. Its tmp dir is pre-created: Run's own MkdirAll
// only runs for a real (non-dry-run) invocation, but package.go-level unit
// tests call fillItem/doFill directly and still need somewhere to stage a
// download.
func newHome(t *testing.T) home.Home {
	t.Helper()
	h := bareHome(t)
	if err := os.MkdirAll(h.Tmp(), 0o755); err != nil {
		t.Fatalf("mkdir tmp: %v", err)
	}
	return h
}

// bareHome returns a home.Home rooted at a fresh temp directory whose tmp
// dir does not exist yet — for tests proving dry-run never creates it.
func bareHome(t *testing.T) home.Home {
	t.Helper()
	dir := t.TempDir()
	return home.Resolve(func(string) string { return dir })
}
