package ocix

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
)

// CatalogIndexEntry represents a single entry in a catalog index, with its manifest descriptor and optional metadata.
type CatalogIndexEntry struct {
	Manifest    ocispec.Descriptor
	Title       string
	Description string
	Version     string
}

// AssembleCatalogIndex takes a slice of CatalogIndexEntry and constructs an OCI image index from them.
// It returns the serialized index bytes and its descriptor.
func AssembleCatalogIndex(entries []CatalogIndexEntry) ([]byte, ocispec.Descriptor) {
	sorted := append([]CatalogIndexEntry(nil), entries...)
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
		Versioned: specs.Versioned{SchemaVersion: SchemaVersionInt},
		MediaType: ocispec.MediaTypeImageIndex,
		Manifests: mans,
		Annotations: map[string]string{
			AnnotationSchemaVersion: SchemaVersion,
		},
	}
	b, _ := json.Marshal(idx)
	return b, content.NewDescriptorFromBytes(ocispec.MediaTypeImageIndex, b)
}

// FetchCatalogIndex fetches the catalog index at ref from src, validates its schema, and returns the index and its descriptor.
func FetchCatalogIndex(ctx context.Context, src oras.ReadOnlyTarget, ref string) (ocispec.Index, ocispec.Descriptor, error) {
	var idx ocispec.Index
	desc, err := src.Resolve(ctx, ref)
	if err != nil {
		return idx, ocispec.Descriptor{}, fmt.Errorf("resolve catalog ref %s: %w", ref, err)
	}
	data, err := content.FetchAll(ctx, src, desc)
	if err != nil {
		return idx, ocispec.Descriptor{}, fmt.Errorf("read catalog index: %w", err)
	}
	if err := json.Unmarshal(data, &idx); err != nil {
		return idx, ocispec.Descriptor{}, fmt.Errorf("parse catalog index: %w", err)
	}
	if err := validateCatalogIndexSchema(idx); err != nil {
		return idx, ocispec.Descriptor{}, err
	}
	return idx, desc, nil
}

// validateCatalogIndexSchema checks idx carries the catalog schema version this build
// understands. Shared by SyncLocalCatalog (pre-copy, against the remote source)
// and OpenLocalStore (post-sync, against the local mirror).
func validateCatalogIndexSchema(idx ocispec.Index) error {
	if got := idx.Annotations[AnnotationSchemaVersion]; got != SchemaVersion {
		return fmt.Errorf("unsupported catalog schema %q (want %s; a newer nem may be required)", got, SchemaVersion)
	}
	return nil
}

// PackageManifest creates an OCI manifest for a package blob, returning the serialized manifest bytes and its descriptor.
func PackageManifest(pkgBytes []byte) ([]byte, ocispec.Descriptor, error) {
	layer := content.NewDescriptorFromBytes(MediaTypePkg, pkgBytes)
	man := ocispec.Manifest{
		Versioned:    specs.Versioned{SchemaVersion: SchemaVersionInt},
		MediaType:    ocispec.MediaTypeImageManifest,
		ArtifactType: MediaTypePkg,
		Config:       ocispec.DescriptorEmptyJSON,
		Layers:       []ocispec.Descriptor{layer},
	}
	manBytes, err := json.Marshal(man)
	if err != nil {
		return nil, ocispec.Descriptor{}, err
	}
	desc := content.NewDescriptorFromBytes(ocispec.MediaTypeImageManifest, manBytes)
	return manBytes, desc, nil
}

// PushPackageManifest pushes pkgBytes as a package manifest into target, if it is not already present.
// It returns the descriptor of the pushed package manifest.
func PushPackageManifest(ctx context.Context, target oras.Target, pkgBytes []byte) (ocispec.Descriptor, error) {
	layer := content.NewDescriptorFromBytes(MediaTypePkg, pkgBytes)
	if err := pushBlobIfAbsent(ctx, target, layer, pkgBytes); err != nil {
		return ocispec.Descriptor{}, err
	}
	if err := PushEmptyConfig(ctx, target); err != nil {
		return ocispec.Descriptor{}, err
	}
	manBytes, manDesc, err := PackageManifest(pkgBytes)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	if err := pushBlobIfAbsent(ctx, target, manDesc, manBytes); err != nil {
		return ocispec.Descriptor{}, err
	}
	return manDesc, nil
}
