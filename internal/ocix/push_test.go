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

	entries := []CatalogIndexEntry{
		{Manifest: kubectlDesc, Title: "kubectl", Version: "v1.31.0"},
		{Manifest: goDesc, Title: "go", Description: "The Go language", Version: "v1.26.5"},
	}
	idxBytes, idxDesc := AssembleCatalogIndex(entries)

	tags := []string{"v2", "v2.20260101T000000Z"}
	if err := PushBlobAndTag(ctx, s, idxBytes, idxDesc, tags); err != nil {
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
	if err := validateCatalogIndexSchema(loaded); err != nil {
		t.Fatalf("validateSchema on pushed index: %v", err)
	}
	if loaded.Annotations[AnnotationSchemaVersion] != SchemaVersion {
		t.Fatalf("loaded index schemaVersion annotation = %q", loaded.Annotations[AnnotationSchemaVersion])
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
