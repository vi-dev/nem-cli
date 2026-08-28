package ocix

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/registry"
	"oras.land/oras-go/v2/registry/remote/errcode"

	"github.com/vi-dev/nem-cli/internal/spec"
)

// ArchivesRef derives the archives repo for a package from a catalog ref:
// "ghcr.io/org/cat:v2" becomes "ghcr.io/org/cat/archives/go" for name "go"
// (any tag or digest on catalogRef is stripped).
func ArchivesRef(catalogRef, name string) (string, error) {
	parsed, err := registry.ParseReference(catalogRef)
	if err != nil {
		return "", fmt.Errorf("parse catalog ref %q: %w", catalogRef, err)
	}
	return fmt.Sprintf("%s/%s/archives/%s", parsed.Registry, parsed.Repository, name), nil
}

// RemoteArchives opens the archives repo for a package (ArchivesRef(catalogRef,
// name)) as a read-only oras target with docker-config credentials.
func RemoteArchives(catalogRef, name string) (oras.ReadOnlyTarget, error) {
	archivesRef, err := ArchivesRef(catalogRef, name)
	if err != nil {
		return nil, err
	}
	return NewRemoteRepository(archivesRef)
}

// PullArchiveFrom resolves srcRef (a tag) on src, expecting a multi-platform
// image index; picks the manifest whose platform matches plat, fetches its
// single MediaTypeArchive layer, and writes it to a fresh temp file under
// dir. The caller owns the returned file (move or consume, then clean up).
//
// A missing archives repo, a missing srcRef tag, or an index with no
// manifest for plat all report ErrArchiveNotFound (wrapped with context, so
// callers can errors.Is against it). Any other resolve/fetch error (auth,
// network, digest mismatch) is wrapped and passed through unchanged.
func PullArchiveFrom(ctx context.Context, src oras.ReadOnlyTarget, srcRef string, plat spec.Platform, dir string) (string, error) {
	layerDesc, err := resolveArchiveLayer(ctx, src, srcRef, plat)
	if err != nil {
		return "", err
	}
	data, err := content.FetchAll(ctx, src, layerDesc)
	if err != nil {
		return "", fmt.Errorf("read archive layer: %w", err)
	}
	return writeTempArchive(dir, data)
}

// ResolveArchiveTag fetches no blobs — a Resolve only, for mirror's
// digest-equality presence checks. Missing reports ErrArchiveNotFound.
func ResolveArchiveTag(ctx context.Context, target oras.ReadOnlyTarget, tag string) (ocispec.Descriptor, error) {
	desc, err := target.Resolve(ctx, tag)
	if err != nil {
		if archiveAbsent(err) {
			return ocispec.Descriptor{}, fmt.Errorf("resolve archive ref %s: %w", tag, ErrArchiveNotFound)
		}
		return ocispec.Descriptor{}, fmt.Errorf("resolve archive ref %s: %w", tag, err)
	}
	return desc, nil
}

// ArchiveLayerDigest returns plat's archive layer digest without fetching
// the layer itself, so fill's heal check can compare against the pinned
// sha256 without downloading the archive back.
func ArchiveLayerDigest(ctx context.Context, src oras.ReadOnlyTarget, tag string, plat spec.Platform) (digest.Digest, error) {
	layerDesc, err := resolveArchiveLayer(ctx, src, tag, plat)
	if err != nil {
		return "", err
	}
	return layerDesc.Digest, nil
}

// resolveArchiveLayer fetches only the index and platform manifest, never
// the layer itself.
func resolveArchiveLayer(ctx context.Context, src oras.ReadOnlyTarget, tag string, plat spec.Platform) (ocispec.Descriptor, error) {
	idx, err := fetchArchiveIndex(ctx, src, tag)
	if err != nil {
		return ocispec.Descriptor{}, err
	}

	manifestDesc, ok := manifestForPlatform(idx, plat)
	if !ok {
		return ocispec.Descriptor{}, fmt.Errorf("platform %s: %w", plat, ErrArchiveNotFound)
	}

	manData, err := content.FetchAll(ctx, src, manifestDesc)
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("read archive manifest: %w", err)
	}
	var man ocispec.Manifest
	if err := json.Unmarshal(manData, &man); err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("parse archive manifest: %w", err)
	}

	layerDesc, ok := archiveLayer(man)
	if !ok {
		return ocispec.Descriptor{}, fmt.Errorf("archive manifest for %s has no %s layer", plat, MediaTypeArchive)
	}
	return layerDesc, nil
}

// ArchivePlatforms resolves tag on src, expecting a multi-platform archive
// index, and returns the platforms its manifests carry. A missing archives
// repo or tag reports ErrArchiveNotFound (wrapped with context, so callers
// can errors.Is against it).
func ArchivePlatforms(ctx context.Context, src oras.ReadOnlyTarget, tag string) ([]spec.Platform, error) {
	idx, err := fetchArchiveIndex(ctx, src, tag)
	if err != nil {
		return nil, err
	}
	var out []spec.Platform
	for _, m := range idx.Manifests {
		if m.Platform != nil {
			out = append(out, spec.Platform{OS: m.Platform.OS, Arch: m.Platform.Architecture})
		}
	}
	return out, nil
}

// fetchArchiveIndex resolves srcRef (a tag) on src and unmarshals the
// multi-platform archive index it names. A missing archives repo or tag
// reports ErrArchiveNotFound (wrapped with context).
func fetchArchiveIndex(ctx context.Context, src oras.ReadOnlyTarget, srcRef string) (ocispec.Index, error) {
	idxDesc, err := src.Resolve(ctx, srcRef)
	if err != nil {
		if archiveAbsent(err) {
			return ocispec.Index{}, fmt.Errorf("resolve archive ref %s: %w", srcRef, ErrArchiveNotFound)
		}
		return ocispec.Index{}, fmt.Errorf("resolve archive ref %s: %w", srcRef, err)
	}
	idxData, err := content.FetchAll(ctx, src, idxDesc)
	if err != nil {
		return ocispec.Index{}, fmt.Errorf("read archive index: %w", err)
	}
	var idx ocispec.Index
	if err := json.Unmarshal(idxData, &idx); err != nil {
		return ocispec.Index{}, fmt.Errorf("parse archive index: %w", err)
	}
	return idx, nil
}

// archiveAbsent reports whether err from an archives-repo read means the
// archive is simply not there (a plain not-found, or a registry 403/404 for
// an absent-or-inaccessible repo — GHCR answers 403 for a missing repo), as
// opposed to a genuine failure worth surfacing.
func archiveAbsent(err error) bool {
	if errors.Is(err, errdef.ErrNotFound) {
		return true
	}
	if resp, ok := errors.AsType[*errcode.ErrorResponse](err); ok {
		return resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound
	}
	return false
}

// manifestForPlatform returns the manifest descriptor in idx whose platform
// matches plat exactly.
func manifestForPlatform(idx ocispec.Index, plat spec.Platform) (ocispec.Descriptor, bool) {
	for _, m := range idx.Manifests {
		if m.Platform != nil && m.Platform.OS == plat.OS && m.Platform.Architecture == plat.Arch {
			return m, true
		}
	}
	return ocispec.Descriptor{}, false
}

// archiveLayer returns man's single MediaTypeArchive layer.
func archiveLayer(man ocispec.Manifest) (ocispec.Descriptor, bool) {
	for _, l := range man.Layers {
		if l.MediaType == MediaTypeArchive {
			return l, true
		}
	}
	return ocispec.Descriptor{}, false
}

// writeTempArchive writes data to a fresh temp file under dir, returning its
// path. The file is removed if it cannot be fully written.
func writeTempArchive(dir string, data []byte) (string, error) {
	f, err := os.CreateTemp(dir, "archive-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create temp file in %s: %w", dir, err)
	}
	tmpPath := f.Name()
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return "", fmt.Errorf("write archive to %s: %w", tmpPath, err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("close temp file %s: %w", tmpPath, err)
	}
	return tmpPath, nil
}
