package ocix

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/errdef"
)

// FakeEntry describes one package to seed into a fake catalog for tests.
type FakeEntry struct {
	Name, Description, Latest string
	YAML                      []byte
}

// PushFakeCatalogForTest builds a minimal nem catalog (one image manifest
// per entry, each wrapping a pkg.yaml layer) and pushes it into store,
// tagging the resulting index "v2". It is the fake registry used by ocix
// and catalog tests: an oci.New layout store stands in for the remote, and
// SyncFrom copies layout-to-layout with no network involved.
func PushFakeCatalogForTest(t testing.TB, store oras.Target, entries []FakeEntry, schemaVersion string) ocispec.Descriptor {
	t.Helper()
	ctx := context.Background()

	emptyConfig := ocispec.DescriptorEmptyJSON
	pushFakeBlob(t, ctx, store, emptyConfig, []byte("{}"))

	manifests := make([]ocispec.Descriptor, 0, len(entries))
	for _, e := range entries {
		layerDesc := content.NewDescriptorFromBytes(MediaTypePkg, e.YAML)
		pushFakeBlob(t, ctx, store, layerDesc, e.YAML)

		manifest := ocispec.Manifest{
			Versioned:    specs.Versioned{SchemaVersion: 2},
			MediaType:    ocispec.MediaTypeImageManifest,
			ArtifactType: MediaTypePkg,
			Config:       emptyConfig,
			Layers:       []ocispec.Descriptor{layerDesc},
		}
		manifestBytes, err := json.Marshal(manifest)
		if err != nil {
			t.Fatalf("marshal manifest for %s: %v", e.Name, err)
		}
		manifestDesc := content.NewDescriptorFromBytes(ocispec.MediaTypeImageManifest, manifestBytes)
		pushFakeBlob(t, ctx, store, manifestDesc, manifestBytes)

		manifestDesc.ArtifactType = MediaTypePkg
		manifestDesc.Annotations = map[string]string{
			AnnotationTitle:       e.Name,
			AnnotationDescription: e.Description,
			AnnotationVersion:     e.Latest,
		}
		manifests = append(manifests, manifestDesc)
	}

	idx := ocispec.Index{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispec.MediaTypeImageIndex,
		Manifests: manifests,
		Annotations: map[string]string{
			AnnotationSchemaVersion: schemaVersion,
		},
	}
	idxBytes, err := json.Marshal(idx)
	if err != nil {
		t.Fatalf("marshal index: %v", err)
	}
	idxDesc := content.NewDescriptorFromBytes(ocispec.MediaTypeImageIndex, idxBytes)
	pushFakeBlob(t, ctx, store, idxDesc, idxBytes)

	if err := store.Tag(ctx, idxDesc, "v2"); err != nil {
		t.Fatalf("tag fake catalog index: %v", err)
	}
	return idxDesc
}

// PushFakeArchive builds a multi-platform archive image (one image manifest
// per platform, each wrapping a single MediaTypeArchive layer, referenced
// from an image index carrying a platform descriptor per manifest) and
// pushes it into store, tagging the resulting index tag. platforms keys are
// "os/arch" strings; values are the per-platform archive payload.
func PushFakeArchive(t *testing.T, store oras.Target, tag string, platforms map[string][]byte) {
	t.Helper()
	ctx := context.Background()

	emptyConfig := ocispec.DescriptorEmptyJSON
	pushFakeBlob(t, ctx, store, emptyConfig, []byte("{}"))

	keys := make([]string, 0, len(platforms))
	for k := range platforms {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	manifests := make([]ocispec.Descriptor, 0, len(keys))
	for _, k := range keys {
		osName, arch, _ := strings.Cut(k, "/")
		payload := platforms[k]

		layerDesc := content.NewDescriptorFromBytes(MediaTypeArchive, payload)
		pushFakeBlob(t, ctx, store, layerDesc, payload)

		manifest := ocispec.Manifest{
			Versioned: specs.Versioned{SchemaVersion: 2},
			MediaType: ocispec.MediaTypeImageManifest,
			Config:    emptyConfig,
			Layers:    []ocispec.Descriptor{layerDesc},
		}
		manifestBytes, err := json.Marshal(manifest)
		if err != nil {
			t.Fatalf("marshal archive manifest for %s: %v", k, err)
		}
		manifestDesc := content.NewDescriptorFromBytes(ocispec.MediaTypeImageManifest, manifestBytes)
		pushFakeBlob(t, ctx, store, manifestDesc, manifestBytes)

		manifestDesc.Platform = &ocispec.Platform{OS: osName, Architecture: arch}
		manifests = append(manifests, manifestDesc)
	}

	idx := ocispec.Index{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispec.MediaTypeImageIndex,
		Manifests: manifests,
	}
	idxBytes, err := json.Marshal(idx)
	if err != nil {
		t.Fatalf("marshal archive index: %v", err)
	}
	idxDesc := content.NewDescriptorFromBytes(ocispec.MediaTypeImageIndex, idxBytes)
	pushFakeBlob(t, ctx, store, idxDesc, idxBytes)

	if err := store.Tag(ctx, idxDesc, tag); err != nil {
		t.Fatalf("tag fake archive index: %v", err)
	}
}

func pushFakeBlob(t testing.TB, ctx context.Context, store oras.Target, desc ocispec.Descriptor, data []byte) {
	t.Helper()
	if err := store.Push(ctx, desc, bytes.NewReader(data)); err != nil && !errors.Is(err, errdef.ErrAlreadyExists) {
		t.Fatalf("push fake blob %s: %v", desc.Digest, err)
	}
}
