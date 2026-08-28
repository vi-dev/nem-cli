package ocix

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/memory"
	"oras.land/oras-go/v2/content/oci"
	"oras.land/oras-go/v2/errdef"

	"github.com/vi-dev/nem-cli/internal/spec"
)

func TestPushArchiveMergesPlatformsAndRoundTrips(t *testing.T) {
	ctx := context.Background()
	store, err := oci.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	darwin := spec.Platform{OS: "darwin", Arch: "arm64"}
	linux := spec.Platform{OS: "linux", Arch: "amd64"}
	dArc := []byte("darwin-archive-bytes")
	lArc := []byte("linux-archive-bytes")

	if _, pushed, err := PushArchive(ctx, store, "v1", darwin, dArc, false); err != nil || !pushed {
		t.Fatalf("push darwin: pushed=%v err=%v", pushed, err)
	}
	if _, pushed, err := PushArchive(ctx, store, "v1", linux, lArc, false); err != nil || !pushed {
		t.Fatalf("push linux: pushed=%v err=%v", pushed, err)
	}
	// unchanged re-push of darwin is a no-op
	if _, pushed, err := PushArchive(ctx, store, "v1", darwin, dArc, false); err != nil || pushed {
		t.Fatalf("re-push darwin unchanged: pushed=%v (want false) err=%v", pushed, err)
	}
	// force re-pushes
	if _, pushed, err := PushArchive(ctx, store, "v1", darwin, dArc, true); err != nil || !pushed {
		t.Fatalf("force re-push: pushed=%v (want true) err=%v", pushed, err)
	}

	// both platforms are readable via the consumer read path
	for plat, want := range map[spec.Platform][]byte{darwin: dArc, linux: lArc} {
		p, err := PullArchiveFrom(ctx, store, "v1", plat, t.TempDir())
		if err != nil {
			t.Fatalf("pull %s: %v", plat, err)
		}
		got, _ := os.ReadFile(p)
		if !bytes.Equal(got, want) {
			t.Fatalf("pull %s = %q, want %q", plat, got, want)
		}
	}
}

// writeTempArchiveFile writes data to a fresh file under t.TempDir() and
// returns its path and hex sha256, mirroring what fill hands
// PublishArchiveLayerFile after a verified download.
func writeTempArchiveFile(t *testing.T, data []byte) (path, sha256Hex string) {
	t.Helper()
	dir := t.TempDir()
	path = filepath.Join(dir, "archive")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write temp archive: %v", err)
	}
	sum := sha256.Sum256(data)
	return path, hex.EncodeToString(sum[:])
}

func TestPublishArchiveLayerFileAndCommitRoundTrips(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	payload := []byte("streamed-archive-bytes")
	path, sha := writeTempArchiveFile(t, payload)
	plat := spec.Platform{OS: "linux", Arch: "amd64"}

	entry, err := PublishArchiveLayerFile(ctx, store, plat, path, sha, int64(len(payload)))
	if err != nil {
		t.Fatalf("PublishArchiveLayerFile: %v", err)
	}
	if entry.Platform == nil || entry.Platform.OS != "linux" || entry.Platform.Architecture != "amd64" {
		t.Fatalf("entry platform = %+v", entry.Platform)
	}
	if _, err := CommitArchiveManifests(ctx, store, "v1.0.0", []ocispec.Descriptor{entry}); err != nil {
		t.Fatalf("CommitArchiveManifests: %v", err)
	}

	pulled, err := PullArchiveFrom(ctx, store, "v1.0.0", plat, t.TempDir())
	if err != nil {
		t.Fatalf("PullArchiveFrom: %v", err)
	}
	got, err := os.ReadFile(pulled)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("pulled bytes = %q, want %q", got, payload)
	}
}

// TestPublishArchiveLayerFileSkipsOpeningWhenBlobAlreadyExists proves the
// layer push itself stays content-addressed after the split: a re-publish
// of byte-identical content recognizes the existing blob and never opens
// path at all, even though PublishArchiveLayerFile no longer consults the
// tag's index (that decision moved to the caller, which already knows via
// ArchiveLayerDigest whether a platform needs republishing).
func TestPublishArchiveLayerFileSkipsOpeningWhenBlobAlreadyExists(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	payload := []byte("unchanged-payload")
	path, sha := writeTempArchiveFile(t, payload)
	plat := spec.Platform{OS: "linux", Arch: "amd64"}

	if _, err := PublishArchiveLayerFile(ctx, store, plat, path, sha, int64(len(payload))); err != nil {
		t.Fatalf("initial publish: %v", err)
	}

	// The file is gone; a re-publish of the same digest must recognize the
	// existing blob and never try to open it.
	missingPath := filepath.Join(t.TempDir(), "does-not-exist")
	if _, err := PublishArchiveLayerFile(ctx, store, plat, missingPath, sha, int64(len(payload))); err != nil {
		t.Fatalf("re-publish unchanged: %v", err)
	}
}

func TestPublishArchiveLayerFileAndCommitHealsChangedContent(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	plat := spec.Platform{OS: "linux", Arch: "amd64"}

	oldPayload := []byte("stale-payload")
	oldPath, oldSha := writeTempArchiveFile(t, oldPayload)
	oldEntry, err := PublishArchiveLayerFile(ctx, store, plat, oldPath, oldSha, int64(len(oldPayload)))
	if err != nil {
		t.Fatalf("initial publish: %v", err)
	}
	if _, err := CommitArchiveManifests(ctx, store, "v1.0.0", []ocispec.Descriptor{oldEntry}); err != nil {
		t.Fatalf("initial commit: %v", err)
	}

	newPayload := []byte("healed-payload-with-different-content")
	newPath, newSha := writeTempArchiveFile(t, newPayload)
	newEntry, err := PublishArchiveLayerFile(ctx, store, plat, newPath, newSha, int64(len(newPayload)))
	if err != nil {
		t.Fatalf("heal publish: %v", err)
	}
	if _, err := CommitArchiveManifests(ctx, store, "v1.0.0", []ocispec.Descriptor{newEntry}); err != nil {
		t.Fatalf("heal commit: %v", err)
	}

	pulled, err := PullArchiveFrom(ctx, store, "v1.0.0", plat, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(pulled)
	if !bytes.Equal(got, newPayload) {
		t.Fatalf("healed bytes = %q, want %q", got, newPayload)
	}
}

func TestPublishArchiveLayerFileMergesWithPushArchivePlatforms(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	darwin := spec.Platform{OS: "darwin", Arch: "arm64"}
	linux := spec.Platform{OS: "linux", Arch: "amd64"}
	dArc := []byte("darwin-bytes")

	if _, _, err := PushArchive(ctx, store, "v1", darwin, dArc, false); err != nil {
		t.Fatalf("PushArchive darwin: %v", err)
	}
	lArc := []byte("linux-bytes-from-a-file")
	path, sha := writeTempArchiveFile(t, lArc)
	entry, err := PublishArchiveLayerFile(ctx, store, linux, path, sha, int64(len(lArc)))
	if err != nil {
		t.Fatalf("PublishArchiveLayerFile linux: %v", err)
	}
	if _, err := CommitArchiveManifests(ctx, store, "v1", []ocispec.Descriptor{entry}); err != nil {
		t.Fatalf("CommitArchiveManifests linux: %v", err)
	}

	plats, err := ArchivePlatforms(ctx, store, "v1")
	if err != nil {
		t.Fatalf("ArchivePlatforms: %v", err)
	}
	if len(plats) != 2 {
		t.Fatalf("platforms = %v, want both darwin and linux merged", plats)
	}
	for plat, want := range map[spec.Platform][]byte{darwin: dArc, linux: lArc} {
		p, err := PullArchiveFrom(ctx, store, "v1", plat, t.TempDir())
		if err != nil {
			t.Fatalf("pull %s: %v", plat, err)
		}
		got, _ := os.ReadFile(p)
		if !bytes.Equal(got, want) {
			t.Fatalf("pull %s = %q, want %q", plat, got, want)
		}
	}
}

// indexPushCountingTarget wraps an oras.Target, counting Push calls whose
// descriptor is an image index (the archive tag's own manifest, as
// opposed to per-platform layer/manifest blobs) and Tag calls — the two
// operations CommitArchiveManifests performs, at most once per call
// regardless of how many platform entries it merges.
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

// TestCommitArchiveManifestsPushesIndexOnceForABatch is the direct
// regression proof for the orphaned-index bug: publishing every platform
// in a batch, then committing them all in one CommitArchiveManifests
// call, pushes and tags the index exactly once — never once per
// platform — regardless of how many platforms the batch covers.
func TestCommitArchiveManifestsPushesIndexOnceForABatch(t *testing.T) {
	ctx := context.Background()
	counted := &indexPushCountingTarget{Target: memory.New()}
	plats := []spec.Platform{
		{OS: "darwin", Arch: "arm64"}, {OS: "darwin", Arch: "amd64"},
		{OS: "linux", Arch: "arm64"}, {OS: "linux", Arch: "amd64"},
	}

	var entries []ocispec.Descriptor
	for _, plat := range plats {
		payload := []byte("payload-" + plat.String())
		path, sha := writeTempArchiveFile(t, payload)
		entry, err := PublishArchiveLayerFile(ctx, counted, plat, path, sha, int64(len(payload)))
		if err != nil {
			t.Fatalf("PublishArchiveLayerFile %s: %v", plat, err)
		}
		entries = append(entries, entry)
	}
	if got := counted.indexPushes.Load(); got != 0 {
		t.Fatalf("index pushes after per-platform publish alone = %d, want 0 (no index touched yet)", got)
	}

	if _, err := CommitArchiveManifests(ctx, counted, "v1.0.0", entries); err != nil {
		t.Fatalf("CommitArchiveManifests: %v", err)
	}
	if got := counted.indexPushes.Load(); got != 1 {
		t.Fatalf("index pushes = %d, want exactly 1 for a %d-platform batch", got, len(plats))
	}
	if got := counted.tags.Load(); got != 1 {
		t.Fatalf("tag calls = %d, want exactly 1 for a %d-platform batch", got, len(plats))
	}

	got, err := ArchivePlatforms(ctx, counted, "v1.0.0")
	if err != nil {
		t.Fatalf("ArchivePlatforms: %v", err)
	}
	if len(got) != len(plats) {
		t.Fatalf("platforms in committed index = %v, want all %d", got, len(plats))
	}
}

// TestCommitArchiveManifestsSkipsWhenMergedResultUnchanged proves the
// batch-level skip: committing the identical set of entries a second
// time — the merged index comes out byte-identical to what tag already
// points to — pushes and tags nothing further.
func TestCommitArchiveManifestsSkipsWhenMergedResultUnchanged(t *testing.T) {
	ctx := context.Background()
	counted := &indexPushCountingTarget{Target: memory.New()}
	plat := spec.Platform{OS: "linux", Arch: "amd64"}
	payload := []byte("stable-payload")
	path, sha := writeTempArchiveFile(t, payload)
	entry, err := PublishArchiveLayerFile(ctx, counted, plat, path, sha, int64(len(payload)))
	if err != nil {
		t.Fatalf("PublishArchiveLayerFile: %v", err)
	}

	if _, err := CommitArchiveManifests(ctx, counted, "v1.0.0", []ocispec.Descriptor{entry}); err != nil {
		t.Fatalf("first commit: %v", err)
	}
	if got := counted.indexPushes.Load(); got != 1 {
		t.Fatalf("index pushes after first commit = %d, want 1", got)
	}

	if _, err := CommitArchiveManifests(ctx, counted, "v1.0.0", []ocispec.Descriptor{entry}); err != nil {
		t.Fatalf("second (unchanged) commit: %v", err)
	}
	if got := counted.indexPushes.Load(); got != 1 {
		t.Fatalf("index pushes after unchanged re-commit = %d, want still 1 (skip)", got)
	}
	if got := counted.tags.Load(); got != 1 {
		t.Fatalf("tag calls after unchanged re-commit = %d, want still 1 (skip)", got)
	}
}

// recordingPushTarget records, for every archive-layer Push, whether the
// reader handed to it is the raw *os.File PublishArchiveLayerFile opened —
// proof that the layer is streamed straight into the push rather than
// buffered into a []byte or bytes.Reader first.
type recordingPushTarget struct {
	oras.Target
	archivePushes int
	sawFileReader bool
}

func (r *recordingPushTarget) Push(ctx context.Context, d ocispec.Descriptor, content io.Reader) error {
	if d.MediaType == MediaTypeArchive {
		r.archivePushes++
		if _, ok := content.(*os.File); ok {
			r.sawFileReader = true
		}
	}
	return r.Target.Push(ctx, d, content)
}

func TestPublishArchiveLayerFileStreamsWithoutBuffering(t *testing.T) {
	ctx := context.Background()
	rec := &recordingPushTarget{Target: memory.New()}
	payload := bytes.Repeat([]byte("x"), 5*1024*1024) // large enough that a slurp would be obviously wasteful
	path, sha := writeTempArchiveFile(t, payload)

	if _, err := PublishArchiveLayerFile(ctx, rec, spec.Platform{OS: "linux", Arch: "amd64"}, path, sha, int64(len(payload))); err != nil {
		t.Fatalf("PublishArchiveLayerFile: %v", err)
	}
	if rec.archivePushes != 1 {
		t.Fatalf("archive layer pushes = %d, want 1", rec.archivePushes)
	}
	if !rec.sawFileReader {
		t.Fatal("archive layer was not streamed from an *os.File — it was buffered before Push")
	}
}

// flakyArchiveLayerTarget fails the first archive-layer Push after
// consuming a few bytes (simulating a dropped connection mid-upload), then
// succeeds on every call after that. Exists always reports the layer
// absent, so every attempt actually calls Push rather than short-circuiting.
type flakyArchiveLayerTarget struct {
	oras.Target
	mu     sync.Mutex
	failed bool
	got    []byte
}

func (f *flakyArchiveLayerTarget) Exists(ctx context.Context, d ocispec.Descriptor) (bool, error) {
	if d.MediaType == MediaTypeArchive {
		return false, nil
	}
	return f.Target.Exists(ctx, d)
}

func (f *flakyArchiveLayerTarget) Push(ctx context.Context, d ocispec.Descriptor, r io.Reader) error {
	if d.MediaType != MediaTypeArchive {
		return f.Target.Push(ctx, d, r)
	}
	f.mu.Lock()
	first := !f.failed
	f.failed = true
	f.mu.Unlock()
	if first {
		io.CopyN(io.Discard, r, 4)
		return errors.New("simulated dropped connection")
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	f.got = data
	return f.Target.Push(ctx, d, bytes.NewReader(data))
}

func TestPublishArchiveLayerFileReopensFileOnRetry(t *testing.T) {
	ctx := context.Background()
	flaky := &flakyArchiveLayerTarget{Target: memory.New()}
	payload := bytes.Repeat([]byte("retry-me-"), 1000)
	path, sha := writeTempArchiveFile(t, payload)

	if _, err := PublishArchiveLayerFile(ctx, flaky, spec.Platform{OS: "linux", Arch: "amd64"}, path, sha, int64(len(payload))); err != nil {
		t.Fatalf("PublishArchiveLayerFile: %v", err)
	}
	if !bytes.Equal(flaky.got, payload) {
		t.Fatal("retried push received a partially-consumed reader instead of a freshly reopened file")
	}
}

type alwaysFailArchiveLayerTarget struct {
	oras.Target
}

// Exists reports every non-archive blob already present, so only the
// archive layer push fails — otherwise PushEmptyConfig's own push would
// fail first and mask what this test names.
func (alwaysFailArchiveLayerTarget) Exists(ctx context.Context, d ocispec.Descriptor) (bool, error) {
	return d.MediaType != MediaTypeArchive, nil
}

func (alwaysFailArchiveLayerTarget) Push(ctx context.Context, d ocispec.Descriptor, r io.Reader) error {
	if d.MediaType == MediaTypeArchive {
		io.Copy(io.Discard, r)
		return errors.New("simulated permanent failure")
	}
	return errdef.ErrUnsupported
}

func (alwaysFailArchiveLayerTarget) Tag(ctx context.Context, d ocispec.Descriptor, ref string) error {
	return errdef.ErrUnsupported
}

func (alwaysFailArchiveLayerTarget) Resolve(ctx context.Context, ref string) (ocispec.Descriptor, error) {
	return ocispec.Descriptor{}, errdef.ErrNotFound
}

func (alwaysFailArchiveLayerTarget) Fetch(ctx context.Context, d ocispec.Descriptor) (io.ReadCloser, error) {
	return nil, errdef.ErrNotFound
}

func TestPublishArchiveLayerFileFailsAfterRetriesExhausted(t *testing.T) {
	ctx := context.Background()
	payload := []byte("x")
	path, sha := writeTempArchiveFile(t, payload)

	entry, err := PublishArchiveLayerFile(ctx, alwaysFailArchiveLayerTarget{}, spec.Platform{OS: "linux", Arch: "amd64"}, path, sha, int64(len(payload)))
	if err == nil {
		t.Fatal("want error once every attempt fails, got nil")
	}
	if !strings.Contains(err.Error(), "simulated permanent failure") {
		t.Fatalf("err = %v, want it to surface the archive layer push's own failure", err)
	}
	if entry.Digest != "" {
		t.Fatalf("entry = %+v, want a zero-value descriptor on failure", entry)
	}
}
