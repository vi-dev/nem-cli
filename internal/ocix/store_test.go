package ocix

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/oci"
)

func TestSyncAndLoad(t *testing.T) {
	ctx := context.Background()
	srcDir := t.TempDir()
	src, err := oci.New(srcDir)
	if err != nil {
		t.Fatalf("oci.New: %v", err)
	}
	goYAML := []byte("schema: 2\nname: go\n") // content is opaque to ocix; not parsed here
	kubectlYAML := []byte("schema: 2\nname: kubectl\n")
	PushFakeCatalogForTest(t, src, []FakeEntry{
		{Name: "go", Description: "The Go programming language", Latest: "v1.26.5", YAML: goYAML},
		{Name: "kubectl", Description: "Kubernetes CLI", Latest: "v1.34.1", YAML: kubectlYAML},
	}, "2")

	storePath := filepath.Join(t.TempDir(), "store")
	s, err := SyncLocalCatalog(ctx, src, "v2", storePath, nil)
	if err != nil {
		t.Fatalf("SyncFrom: %v", err)
	}
	if !strings.HasPrefix(s.Digest(), "sha256:") {
		t.Fatalf("index digest: %q", s.Digest())
	}
	if len(s.Index().Manifests) != 2 {
		t.Fatalf("Index: %+v", s.Index())
	}

	s, err = OpenLocalStore(ctx, storePath)
	if err != nil || len(s.Index().Manifests) != 2 {
		t.Fatalf("OpenStore: %v", err)
	}

	data, mdig, err := s.PkgBytes(ctx, "go")
	if err != nil || string(data) != string(goYAML) || !strings.HasPrefix(mdig, "sha256:") {
		t.Fatalf("PkgBytes: %q, %q, %v", data, mdig, err)
	}

	var nf *PkgNotInIndexError
	if _, _, err := s.PkgBytes(ctx, "absent"); !errors.As(err, &nf) {
		t.Fatalf("want PkgNotInIndexError, got %v", err)
	}
}

func TestSyncRejectsWrongSchema(t *testing.T) {
	ctx := context.Background()
	storePath := filepath.Join(t.TempDir(), "store")

	good, _ := oci.New(t.TempDir())
	PushFakeCatalogForTest(t, good, []FakeEntry{{Name: "go", YAML: []byte("x")}}, "2")
	if _, err := SyncLocalCatalog(ctx, good, "v2", storePath, nil); err != nil {
		t.Fatalf("seed sync: %v", err)
	}

	bad, _ := oci.New(t.TempDir())
	PushFakeCatalogForTest(t, bad, []FakeEntry{{Name: "go", YAML: []byte("x")}}, "9")
	_, err := SyncLocalCatalog(ctx, bad, "v2", storePath, nil)
	if err == nil || !strings.Contains(err.Error(), "schema") {
		t.Fatalf("want schema error, got %v", err)
	}

	s, err := OpenLocalStore(ctx, storePath)
	if err != nil || len(s.Index().Manifests) != 1 {
		t.Fatalf("good mirror clobbered by bad-schema sync attempt: %v", err)
	}
}

func TestOpenStoreUnsynced(t *testing.T) {
	_, err := OpenLocalStore(context.Background(), filepath.Join(t.TempDir(), "nope"))
	if !errors.Is(err, ErrNotSynced) {
		t.Fatalf("want ErrNotSynced, got %v", err)
	}
}

// movingResolveTarget wraps a real oras.ReadOnlyTarget, answering Resolve
// for ref with first on the first call and rest on every call after —
// simulating a tag that moves between SyncLocalCatalog's pre-copy schema
// validation and its own internal copy re-resolve.
type movingResolveTarget struct {
	oras.ReadOnlyTarget
	ref         string
	first, rest ocispec.Descriptor
	calls       int
}

func (m *movingResolveTarget) Resolve(ctx context.Context, ref string) (ocispec.Descriptor, error) {
	if ref != m.ref {
		return m.ReadOnlyTarget.Resolve(ctx, ref)
	}
	m.calls++
	if m.calls == 1 {
		return m.first, nil
	}
	return m.rest, nil
}

// TestSyncFromDetectsMovingTag proves SyncLocalCatalog catches a tag that moves
// between the descriptor its pre-copy schema validation resolved and the
// descriptor its own closure copy actually resolves: it must report the
// mismatch rather than silently syncing unvalidated content over a
// previously-synced good local mirror.
func TestSyncFromDetectsMovingTag(t *testing.T) {
	ctx := context.Background()
	store, err := oci.New(t.TempDir())
	if err != nil {
		t.Fatalf("oci.New: %v", err)
	}
	oldDesc := PushFakeCatalogForTest(t, store, []FakeEntry{{Name: "go", YAML: []byte("old")}}, SchemaVersion)
	newDesc := PushFakeCatalogForTest(t, store, []FakeEntry{{Name: "go", YAML: []byte("new")}}, SchemaVersion)
	if oldDesc.Digest == newDesc.Digest {
		t.Fatal("fixture bug: old and new catalog content must digest differently")
	}

	moving := &movingResolveTarget{ReadOnlyTarget: store, ref: "v2", first: oldDesc, rest: newDesc}
	storePath := filepath.Join(t.TempDir(), "store")
	_, err = SyncLocalCatalog(ctx, moving, "v2", storePath, nil)
	if err == nil || !strings.Contains(err.Error(), "catalog changed during sync; retry") {
		t.Fatalf("SyncFrom error = %v, want a catalog-changed-during-sync error", err)
	}
}

func TestSyncIsIdempotent(t *testing.T) {
	ctx := context.Background()
	src, _ := oci.New(t.TempDir())
	PushFakeCatalogForTest(t, src, []FakeEntry{{Name: "go", YAML: []byte("y")}}, "2")
	storePath := filepath.Join(t.TempDir(), "store")
	s1, err := SyncLocalCatalog(ctx, src, "v2", storePath, nil)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := SyncLocalCatalog(ctx, src, "v2", storePath, nil)
	if err != nil || s1.Digest() != s2.Digest() {
		t.Fatalf("resync: %q vs %q, %v", s1.Digest(), s2.Digest(), err)
	}
}

func TestFetchPkgBytesReturnsLayer(t *testing.T) {
	store, err := oci.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	yaml := []byte("name: atool\n")
	PushFakeCatalogForTest(t, store, []FakeEntry{
		{Name: "atool", Latest: "1.0.0", YAML: yaml},
	}, SchemaVersion)
	idx, _, err := FetchCatalogIndex(context.Background(), store, "v2")
	if err != nil {
		t.Fatal(err)
	}

	got, err := FetchPkgBytes(context.Background(), store, idx.Manifests[0])
	if err != nil {
		t.Fatalf("FetchPkgBytes: %v", err)
	}
	if !bytes.Equal(got, yaml) {
		t.Fatalf("bytes = %q, want %q", got, yaml)
	}
}

func TestFetchPkgBytesRejectsManifestWithoutPkgLayer(t *testing.T) {
	store, err := oci.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// An archive image manifest has no MediaTypePkg layer.
	PushFakeArchive(t, store, "1.0.0", map[string][]byte{"linux/amd64": []byte("a")})
	desc, err := store.Resolve(context.Background(), "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	idxData, err := content.FetchAll(context.Background(), store, desc)
	if err != nil {
		t.Fatal(err)
	}
	var archIdx ocispec.Index
	if err := json.Unmarshal(idxData, &archIdx); err != nil {
		t.Fatal(err)
	}

	if _, err := FetchPkgBytes(context.Background(), store, archIdx.Manifests[0]); err == nil {
		t.Fatal("FetchPkgBytes accepted a manifest without a pkg.yaml layer, want error")
	}
}

// namedDesc builds a manifest descriptor carrying only the AnnotationTitle
// enumerateTitledManifests reads, tagged via ArtifactType so two same-named
// entries are still distinguishable in a test's assertions.
func namedDesc(name, id string) ocispec.Descriptor {
	d := ocispec.Descriptor{ArtifactType: id}
	if name != "" {
		d.Annotations = map[string]string{AnnotationTitle: name}
	}
	return d
}

func TestEnumeratePackagesSortsByName(t *testing.T) {
	idx := ocispec.Index{Manifests: []ocispec.Descriptor{
		namedDesc("kubectl", "1"),
		namedDesc("go", "2"),
		namedDesc("erlang", "3"),
	}}

	got := enumerateTitledManifests(idx)

	var names []string
	for _, nm := range got {
		names = append(names, nm.Title)
	}
	want := []string{"erlang", "go", "kubectl"}
	if !slices.Equal(names, want) {
		t.Fatalf("names = %v, want %v", names, want)
	}
}

func TestEnumeratePackagesDedupesFirstOccurrenceWins(t *testing.T) {
	idx := ocispec.Index{Manifests: []ocispec.Descriptor{
		namedDesc("go", "first"),
		namedDesc("go", "second"),
	}}

	got := enumerateTitledManifests(idx)

	if len(got) != 1 {
		t.Fatalf("got %d entries, want exactly 1 for a duplicated name", len(got))
	}
	if got[0].Desc.ArtifactType != "first" {
		t.Fatalf("got entry %q, want the first occurrence", got[0].Desc.ArtifactType)
	}
}

func TestEnumeratePackagesSkipsManifestsWithoutTitle(t *testing.T) {
	idx := ocispec.Index{Manifests: []ocispec.Descriptor{
		namedDesc("", "untitled"),
		namedDesc("go", "go"),
	}}

	got := enumerateTitledManifests(idx)

	if len(got) != 1 || got[0].Title != "go" {
		t.Fatalf("got %+v, want exactly one entry for go", got)
	}
}

// TestStageIndexClosureStable proves the ordinary case stages cleanly:
// the returned Store's index and digest both come back usable.
func TestStageIndexClosureStable(t *testing.T) {
	ctx := context.Background()
	store, err := oci.New(t.TempDir())
	if err != nil {
		t.Fatalf("oci.New: %v", err)
	}
	desc := PushFakeCatalogForTest(t, store, []FakeEntry{{Name: "go", YAML: []byte("y")}}, SchemaVersion)

	staged, err := OpenStoreInMemory(ctx, store, "v2", nil)
	if err != nil {
		t.Fatalf("StageInMemory: %v", err)
	}
	if len(staged.Index().Manifests) != 1 {
		t.Fatalf("manifests = %d, want 1", len(staged.Index().Manifests))
	}
	if staged.Digest() != desc.Digest.String() {
		t.Fatalf("staged digest = %s, want %s", staged.Digest(), desc.Digest)
	}
}

// TestStageIndexClosureRejectsWrongSchema proves a bad-schema catalog
// still fails OpenStoreInMemory — schema validation happens against the
// staged copy rather than before it, but the caller-facing contract
// (mirror and fill both abort on a bad-schema source) is unchanged.
func TestStageIndexClosureRejectsWrongSchema(t *testing.T) {
	ctx := context.Background()
	store, err := oci.New(t.TempDir())
	if err != nil {
		t.Fatalf("oci.New: %v", err)
	}
	PushFakeCatalogForTest(t, store, []FakeEntry{{Name: "go", YAML: []byte("x")}}, "9")

	_, err = OpenStoreInMemory(ctx, store, "v2", nil)
	if err == nil || !strings.Contains(err.Error(), "schema") {
		t.Fatalf("StageInMemory error = %v, want a schema error", err)
	}
}

// TestStageIndexClosureReportsProgress proves OpenStoreInMemory itself — not
// just the lower-level copy helper — wires the callback through.
func TestStageIndexClosureReportsProgress(t *testing.T) {
	ctx := context.Background()
	store, err := oci.New(t.TempDir())
	if err != nil {
		t.Fatalf("oci.New: %v", err)
	}
	const n = 4
	PushFakeCatalogForTest(t, store, fakeCatalogEntries(n), SchemaVersion)

	rec := &progressRecorder{}
	if _, err := OpenStoreInMemory(ctx, store, "v2", rec.record); err != nil {
		t.Fatalf("StageInMemory: %v", err)
	}

	calls := rec.snapshot()
	if len(calls) == 0 {
		t.Fatal("no progress calls recorded")
	}
	last := calls[len(calls)-1]
	wantTotal := int64(n + 1)
	if last.done != wantTotal || last.total != wantTotal {
		t.Fatalf("final call = %+v, want done=total=%d", last, wantTotal)
	}
}
