package ocix

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sort"

	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/errdef"
)

// PushEmptyConfig stores the shared empty config blob in target. It is safe
// to call for every package pushed to the same target: the blob is content
// addressed and pushed only when absent.
func PushEmptyConfig(ctx context.Context, target oras.Target) error {
	return pushBlobIfAbsent(ctx, target, ocispec.DescriptorEmptyJSON, ocispec.DescriptorEmptyJSON.Data)
}

// packageManifest builds the annotation-free image manifest wrapping
// pkgBytes as a single MediaTypePkg layer under the shared empty config.
// The returned bytes are a pure function of pkgBytes: no annotations, no
// timestamps, and json.Marshal of ocispec.Manifest orders fields by struct
// declaration, so the same pkgBytes always serializes identically.
func packageManifest(pkgBytes []byte) ([]byte, error) {
	layer := content.NewDescriptorFromBytes(MediaTypePkg, pkgBytes)
	man := ocispec.Manifest{
		Versioned:    specs.Versioned{SchemaVersion: 2},
		MediaType:    ocispec.MediaTypeImageManifest,
		ArtifactType: MediaTypePkg,
		Config:       ocispec.DescriptorEmptyJSON,
		Layers:       []ocispec.Descriptor{layer},
	}
	return json.Marshal(man)
}

// PackageManifestDescriptor computes the manifest descriptor PushPackageManifest
// would produce for pkgBytes, without touching target. Callers use it to
// check ManifestExists before deciding whether to push at all.
func PackageManifestDescriptor(pkgBytes []byte) (ocispec.Descriptor, error) {
	manBytes, err := packageManifest(pkgBytes)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	return content.NewDescriptorFromBytes(ocispec.MediaTypeImageManifest, manBytes), nil
}

// PushPackageManifest pushes pkgBytes as the sole layer of an image
// manifest (ArtifactType MediaTypePkg, empty config) into target, along
// with the layer and config blobs it references, and returns the manifest
// descriptor.
func PushPackageManifest(ctx context.Context, target oras.Target, pkgBytes []byte) (ocispec.Descriptor, error) {
	layer := content.NewDescriptorFromBytes(MediaTypePkg, pkgBytes)
	if err := pushBlobIfAbsent(ctx, target, layer, pkgBytes); err != nil {
		return ocispec.Descriptor{}, err
	}
	if err := PushEmptyConfig(ctx, target); err != nil {
		return ocispec.Descriptor{}, err
	}
	manBytes, err := packageManifest(pkgBytes)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	manDesc := content.NewDescriptorFromBytes(ocispec.MediaTypeImageManifest, manBytes)
	if err := pushBlobIfAbsent(ctx, target, manDesc, manBytes); err != nil {
		return ocispec.Descriptor{}, err
	}
	return manDesc, nil
}

// ManifestExists reports whether a manifest matching d's digest is already
// present in target.
func ManifestExists(ctx context.Context, target oras.Target, d ocispec.Descriptor) (bool, error) {
	return target.Exists(ctx, d)
}

// IndexEntry describes one package's manifest and catalog-facing metadata
// for assembly into a catalog index.
type IndexEntry struct {
	Manifest    ocispec.Descriptor
	Title       string
	Description string
	Version     string
}

// AssembleIndex builds the catalog index over entries, sorted by Title in
// byte order so the resulting bytes are deterministic for a fixed entry
// set. Each manifest descriptor in the index carries AnnotationTitle
// (always) and AnnotationDescription/AnnotationVersion (only when the
// corresponding field is non-empty); the manifest JSON itself is untouched.
func AssembleIndex(entries []IndexEntry) ([]byte, ocispec.Descriptor) {
	sorted := append([]IndexEntry(nil), entries...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Title < sorted[j].Title })

	mans := make([]ocispec.Descriptor, len(sorted))
	for i, e := range sorted {
		d := e.Manifest
		ann := map[string]string{AnnotationTitle: e.Title}
		if e.Description != "" {
			ann[AnnotationDescription] = e.Description
		}
		if e.Version != "" {
			ann[AnnotationVersion] = e.Version
		}
		d.Annotations = ann
		mans[i] = d
	}
	idx := ocispec.Index{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispec.MediaTypeImageIndex,
		Manifests: mans,
		Annotations: map[string]string{
			AnnotationSchemaVersion: SchemaVersion,
		},
	}
	b, _ := json.Marshal(idx)
	return b, content.NewDescriptorFromBytes(ocispec.MediaTypeImageIndex, b)
}

// PushIndex pushes indexBytes under indexDesc into target, then applies
// every tag in tags to indexDesc. Tagging only happens after the blob push
// succeeds, so a failed push never leaves a tag pointing at missing content.
func PushIndex(ctx context.Context, target oras.Target, indexBytes []byte, indexDesc ocispec.Descriptor, tags []string) error {
	if err := pushBlobIfAbsent(ctx, target, indexDesc, indexBytes); err != nil {
		return err
	}
	for _, tag := range tags {
		if err := target.Tag(ctx, indexDesc, tag); err != nil {
			return err
		}
	}
	return nil
}

// pushBlobIfAbsent pushes data under d, so re-pushing shared blobs (the
// empty config, an unchanged manifest) across packages never errors. The
// Exists check is only a fast path, not the sole guard: two callers can
// race between it and Push, so an errdef.ErrAlreadyExists from Push itself
// is also treated as success rather than surfaced.
func pushBlobIfAbsent(ctx context.Context, target oras.Target, d ocispec.Descriptor, data []byte) error {
	ok, err := target.Exists(ctx, d)
	if err != nil {
		return err
	}
	if ok {
		return nil
	}
	if err := target.Push(ctx, d, bytes.NewReader(data)); err != nil && !errors.Is(err, errdef.ErrAlreadyExists) {
		return err
	}
	return nil
}
