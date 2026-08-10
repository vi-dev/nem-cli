package ocix

import (
	"context"
	"encoding/json"
	"io"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/oci"
	"oras.land/oras-go/v2/errdef"
)

func TestAssembleIndexAnnotationsAndOrder(t *testing.T) {
	mk := func(name string) ocispec.Descriptor {
		return content.NewDescriptorFromBytes(ocispec.MediaTypeImageManifest, []byte(name))
	}
	entries := []IndexEntry{
		{Manifest: mk("kubectl"), Title: "kubectl", Version: "v1.31.0"},
		{Manifest: mk("go"), Title: "go", Description: "The Go language", Version: "v1.26.5"},
	}
	idxBytes, idxDesc := AssembleIndex(entries)

	var idx ocispec.Index
	if err := json.Unmarshal(idxBytes, &idx); err != nil {
		t.Fatal(err)
	}
	if idx.MediaType != ocispec.MediaTypeImageIndex {
		t.Fatalf("index mediaType = %q", idx.MediaType)
	}
	if idx.Annotations[AnnotationSchemaVersion] != SchemaVersion {
		t.Fatalf("schemaVersion annotation = %q", idx.Annotations[AnnotationSchemaVersion])
	}
	// Sorted by title: go before kubectl.
	if idx.Manifests[0].Annotations[AnnotationTitle] != "go" ||
		idx.Manifests[1].Annotations[AnnotationTitle] != "kubectl" {
		t.Fatalf("entries not sorted by title: %v", idx.Manifests)
	}
	if idx.Manifests[0].Annotations[AnnotationVersion] != "v1.26.5" {
		t.Fatalf("version annotation missing on go")
	}
	if idx.Manifests[0].Annotations[AnnotationDescription] != "The Go language" {
		t.Fatalf("description annotation missing on go")
	}
	// kubectl has no description → key absent, not empty.
	if _, ok := idx.Manifests[1].Annotations[AnnotationDescription]; ok {
		t.Fatalf("empty description must be omitted")
	}
	if idxDesc.MediaType != ocispec.MediaTypeImageIndex {
		t.Fatalf("index desc mediaType = %q", idxDesc.MediaType)
	}
}

func TestPushIndexRoundTrip(t *testing.T) {
	ctx := context.Background()
	storePath := t.TempDir()
	s, err := oci.New(storePath)
	if err != nil {
		t.Fatal(err)
	}

	goDesc, err := PushPackageManifest(ctx, s, []byte("schema: 2\nname: go\n"))
	if err != nil {
		t.Fatalf("PushPackageManifest go: %v", err)
	}
	kubectlDesc, err := PushPackageManifest(ctx, s, []byte("schema: 2\nname: kubectl\n"))
	if err != nil {
		t.Fatalf("PushPackageManifest kubectl: %v", err)
	}

	entries := []IndexEntry{
		{Manifest: kubectlDesc, Title: "kubectl", Version: "v1.31.0"},
		{Manifest: goDesc, Title: "go", Description: "The Go language", Version: "v1.26.5"},
	}
	idxBytes, idxDesc := AssembleIndex(entries)

	tags := []string{"v2", "v2.20260101T000000Z"}
	if err := PushIndex(ctx, s, idxBytes, idxDesc, tags); err != nil {
		t.Fatalf("PushIndex: %v", err)
	}

	for _, tag := range tags {
		resolved, err := s.Resolve(ctx, tag)
		if err != nil {
			t.Fatalf("resolve tag %s: %v", tag, err)
		}
		if resolved.Digest != idxDesc.Digest {
			t.Fatalf("tag %s resolved to %s, want %s", tag, resolved.Digest, idxDesc.Digest)
		}
	}

	loadedBytes, err := content.FetchAll(ctx, s, idxDesc)
	if err != nil {
		t.Fatalf("fetch pushed index: %v", err)
	}
	var loaded ocispec.Index
	if err := json.Unmarshal(loadedBytes, &loaded); err != nil {
		t.Fatalf("unmarshal pushed index: %v", err)
	}
	if err := validateSchema(loaded); err != nil {
		t.Fatalf("validateSchema on pushed index: %v", err)
	}
	if loaded.Annotations[AnnotationSchemaVersion] != SchemaVersion {
		t.Fatalf("loaded index schemaVersion annotation = %q", loaded.Annotations[AnnotationSchemaVersion])
	}
}

func decodeJSON(r io.Reader, v any) error {
	return json.NewDecoder(r).Decode(v)
}

func TestPushPackageManifestDeterministicAndShaped(t *testing.T) {
	ctx := context.Background()
	pkgBytes := []byte("schema: 2\nname: go\n")

	newStore := func() *oci.Store {
		s, err := oci.New(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		return s
	}

	s1 := newStore()
	if err := PushEmptyConfig(ctx, s1); err != nil {
		t.Fatalf("PushEmptyConfig: %v", err)
	}
	d1, err := PushPackageManifest(ctx, s1, pkgBytes)
	if err != nil {
		t.Fatalf("PushPackageManifest: %v", err)
	}

	// Same bytes into a fresh store ⇒ identical manifest digest.
	s2 := newStore()
	if err := PushEmptyConfig(ctx, s2); err != nil {
		t.Fatal(err)
	}
	d2, err := PushPackageManifest(ctx, s2, pkgBytes)
	if err != nil {
		t.Fatal(err)
	}
	if d1.Digest != d2.Digest {
		t.Fatalf("manifest digest not deterministic: %s vs %s", d1.Digest, d2.Digest)
	}
	if d1.MediaType != ocispec.MediaTypeImageManifest {
		t.Fatalf("manifest mediaType = %q", d1.MediaType)
	}
	if len(d1.Annotations) != 0 {
		t.Fatalf("manifest descriptor should carry no annotations, got %v", d1.Annotations)
	}

	// Manifest JSON itself must be annotation-free and carry the pkg layer.
	rc, err := s1.Fetch(ctx, d1)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	var m ocispec.Manifest
	if err := decodeJSON(rc, &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Annotations) != 0 {
		t.Fatalf("manifest JSON must be annotation-free, got %v", m.Annotations)
	}
	if m.SchemaVersion != 2 {
		t.Fatalf("manifest schemaVersion = %d, want 2", m.SchemaVersion)
	}
	if m.ArtifactType != MediaTypePkg {
		t.Fatalf("artifactType = %q, want %q", m.ArtifactType, MediaTypePkg)
	}
	if len(m.Layers) != 1 || m.Layers[0].MediaType != MediaTypePkg {
		t.Fatalf("layers = %+v", m.Layers)
	}
}

func TestPackageManifestDescriptorMatchesPush(t *testing.T) {
	ctx := context.Background()
	pkgBytes := []byte("schema: 2\nname: helm\n")
	s, err := oci.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := PushEmptyConfig(ctx, s); err != nil {
		t.Fatal(err)
	}
	pushed, err := PushPackageManifest(ctx, s, pkgBytes)
	if err != nil {
		t.Fatal(err)
	}
	computed, err := PackageManifestDescriptor(pkgBytes)
	if err != nil {
		t.Fatal(err)
	}
	if pushed.Digest != computed.Digest {
		t.Fatalf("PackageManifestDescriptor digest = %s, want %s", computed.Digest, pushed.Digest)
	}
}

func TestManifestExists(t *testing.T) {
	ctx := context.Background()
	s, err := oci.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pkgBytes := []byte("schema: 2\nname: kubectl\n")
	d, err := PushPackageManifest(ctx, s, pkgBytes)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := ManifestExists(ctx, s, d)
	if err != nil || !ok {
		t.Fatalf("want exists=true err=nil, got %v %v", ok, err)
	}
	absent := content.NewDescriptorFromBytes(ocispec.MediaTypeImageManifest, []byte("nope"))
	ok, err = ManifestExists(ctx, s, absent)
	if err != nil || ok {
		t.Fatalf("want exists=false err=nil, got %v %v", ok, err)
	}
}

// alreadyExistsTarget is an oras.Target stub that always reports a blob as
// absent on Exists, then rejects the resulting Push with
// errdef.ErrAlreadyExists — reproducing what a concurrent pusher of the
// same digest sees when it loses the race after both sides pass the
// Exists check.
type alreadyExistsTarget struct{}

func (alreadyExistsTarget) Fetch(ctx context.Context, d ocispec.Descriptor) (io.ReadCloser, error) {
	return nil, errdef.ErrNotFound
}

func (alreadyExistsTarget) Exists(ctx context.Context, d ocispec.Descriptor) (bool, error) {
	return false, nil
}

func (alreadyExistsTarget) Push(ctx context.Context, d ocispec.Descriptor, r io.Reader) error {
	return errdef.ErrAlreadyExists
}

func (alreadyExistsTarget) Tag(ctx context.Context, d ocispec.Descriptor, reference string) error {
	return errdef.ErrUnsupported
}

func (alreadyExistsTarget) Resolve(ctx context.Context, reference string) (ocispec.Descriptor, error) {
	return ocispec.Descriptor{}, errdef.ErrNotFound
}

func TestPushBlobIfAbsentSwallowsAlreadyExists(t *testing.T) {
	if err := PushEmptyConfig(context.Background(), alreadyExistsTarget{}); err != nil {
		t.Fatalf("PushEmptyConfig against a target that races to already-exists: %v", err)
	}
}
