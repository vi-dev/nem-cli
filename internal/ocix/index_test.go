package ocix

import (
	"context"
	"encoding/json"
	"io"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/oci"
)

func TestAssembleIndexAnnotationsAndOrder(t *testing.T) {
	mk := func(name string) ocispec.Descriptor {
		return content.NewDescriptorFromBytes(ocispec.MediaTypeImageManifest, []byte(name))
	}
	entries := []CatalogIndexEntry{
		{Manifest: mk("kubectl"), Title: "kubectl", Version: "v1.31.0"},
		{Manifest: mk("go"), Title: "go", Description: "The Go language", Version: "v1.26.5"},
	}
	idxBytes, idxDesc := AssembleCatalogIndex(entries)

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
	_, computed, err := PackageManifest(pkgBytes)
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
	ok, err := s.Exists(ctx, d)
	if err != nil || !ok {
		t.Fatalf("want exists=true err=nil, got %v %v", ok, err)
	}
	absent := content.NewDescriptorFromBytes(ocispec.MediaTypeImageManifest, []byte("nope"))
	ok, err = s.Exists(ctx, absent)
	if err != nil || ok {
		t.Fatalf("want exists=false err=nil, got %v %v", ok, err)
	}
}

func TestFetchIndexReturnsParsedIndex(t *testing.T) {
	store, err := oci.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	PushFakeCatalogForTest(t, store, []FakeEntry{
		{Name: "atool", Description: "A tool", Latest: "1.0.0", YAML: []byte("name: atool")},
	}, SchemaVersion)

	idx, desc, err := FetchCatalogIndex(context.Background(), store, "v2")
	if err != nil {
		t.Fatalf("FetchIndex: %v", err)
	}
	if len(idx.Manifests) != 1 {
		t.Fatalf("manifests = %d, want 1", len(idx.Manifests))
	}
	if got := idx.Manifests[0].Annotations[AnnotationTitle]; got != "atool" {
		t.Fatalf("title = %q, want atool", got)
	}
	if desc.Digest == "" {
		t.Fatal("descriptor digest is empty")
	}
}

func TestFetchIndexRejectsWrongSchema(t *testing.T) {
	store, err := oci.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	PushFakeCatalogForTest(t, store, nil, "999")

	if _, _, err := FetchCatalogIndex(context.Background(), store, "v2"); err == nil {
		t.Fatal("FetchIndex accepted schema 999, want error")
	}
}
