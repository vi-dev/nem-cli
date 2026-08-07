package ocix

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/oci"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/registry"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/credentials"
)

// ValidateRef checks that ref names a specific oci artifact via a tag or
// digest. A bare repository reference (e.g. "ghcr.io/org/cat") resolves to
// whatever the registry currently calls "latest", which is not something
// nem should pin a catalog sync to implicitly.
func ValidateRef(ref string) error {
	parsed, err := registry.ParseReference(ref)
	if err != nil {
		return fmt.Errorf("parse oci ref %q: %w", ref, err)
	}
	if parsed.Reference == "" {
		return fmt.Errorf("oci ref %q has no tag or digest", ref)
	}
	return nil
}

// newRepository opens ref as a *remote.Repository with docker-config
// credentials, shared by RemoteCatalog and RemoteCatalogRW.
func newRepository(ref string) (*remote.Repository, error) {
	repo, err := remote.NewRepository(ref)
	if err != nil {
		return nil, fmt.Errorf("parse catalog ref %q: %w", ref, err)
	}
	credStore, err := credentials.NewStoreFromDocker(credentials.StoreOptions{})
	if err != nil {
		return nil, fmt.Errorf("open docker credentials: %w", err)
	}
	repo.Client = &auth.Client{
		Credential: credentials.Credential(credStore),
		Cache:      auth.NewCache(),
	}
	return repo, nil
}

// RemoteCatalog opens ref (e.g. "ghcr.io/org/cat:v2") as a read-only oras
// target with docker-config credentials, returning the target and the
// reference (tag or digest) to copy from.
func RemoteCatalog(ref string) (oras.ReadOnlyTarget, string, error) {
	repo, err := newRepository(ref)
	if err != nil {
		return nil, "", err
	}
	return repo, repo.Reference.Reference, nil
}

// RemoteCatalogRW opens ref as a writable oras target with the same
// docker-config credentials as RemoteCatalog, for publishing a catalog.
func RemoteCatalogRW(ref string) (oras.Target, string, error) {
	repo, err := newRepository(ref)
	if err != nil {
		return nil, "", err
	}
	return repo, repo.Reference.Reference, nil
}

// SyncFrom copies the catalog index closure from src into an OCI layout
// store at storePath and tags it LocalTag. Returns the index digest.
//
// The source index is fetched and schema-validated before anything is
// written to storePath, so a bad-schema remote can never move the local
// tag and clobber a previously-synced good mirror. After the copy, the
// copied digest is checked against the validated one: srcRef could resolve
// to different content between validation and oras.Copy's own re-resolve
// (e.g. a moving tag updated mid-sync), and copying that drift in silently
// would leave storePath tagged with content nem never schema-checked.
func SyncFrom(ctx context.Context, src oras.ReadOnlyTarget, srcRef, storePath string) (string, error) {
	validated, err := validateSrcSchema(ctx, src, srcRef)
	if err != nil {
		return "", err
	}

	dst, err := oci.New(storePath)
	if err != nil {
		return "", fmt.Errorf("open catalog store %s: %w", storePath, err)
	}
	opts := oras.DefaultCopyOptions
	opts.Concurrency = syncConcurrency
	copied, err := oras.Copy(ctx, src, srcRef, dst, LocalTag, opts)
	if err != nil {
		return "", fmt.Errorf("sync catalog: %w", err)
	}
	if copied.Digest != validated.Digest {
		return "", errors.New("catalog changed during sync; retry")
	}
	return copied.Digest.String(), nil
}

// validateSrcSchema resolves srcRef on src, fetches the index bytes, and
// checks the catalog schema version, without touching any local state. It
// returns the resolved descriptor so the caller can verify the digest
// actually copied still matches what was validated.
func validateSrcSchema(ctx context.Context, src oras.ReadOnlyTarget, srcRef string) (ocispec.Descriptor, error) {
	desc, err := src.Resolve(ctx, srcRef)
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("resolve catalog ref %s: %w", srcRef, err)
	}
	data, err := content.FetchAll(ctx, src, desc)
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("read catalog index: %w", err)
	}
	var idx ocispec.Index
	if err := json.Unmarshal(data, &idx); err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("parse catalog index: %w", err)
	}
	if err := validateSchema(idx); err != nil {
		return ocispec.Descriptor{}, err
	}
	return desc, nil
}

// validateSchema checks idx carries the catalog schema version this build
// understands. Shared by SyncFrom (pre-copy, against the remote source)
// and LoadIndex (post-sync, against the local mirror).
func validateSchema(idx ocispec.Index) error {
	if got := idx.Annotations[AnnotationSchemaVersion]; got != SchemaVersion {
		return fmt.Errorf("unsupported catalog schema %q (want %s; a newer nem may be required)", got, SchemaVersion)
	}
	return nil
}

// LoadIndex reads and validates the locally synced catalog index.
func LoadIndex(ctx context.Context, storePath string) (ocispec.Index, error) {
	var idx ocispec.Index
	if _, err := os.Stat(storePath); os.IsNotExist(err) {
		return idx, ErrNotSynced
	}
	store, err := oci.New(storePath)
	if err != nil {
		return idx, fmt.Errorf("open catalog store %s: %w", storePath, err)
	}
	desc, err := store.Resolve(ctx, LocalTag)
	if err != nil {
		if errors.Is(err, errdef.ErrNotFound) {
			return idx, ErrNotSynced
		}
		return idx, fmt.Errorf("resolve catalog index: %w", err)
	}
	data, err := content.FetchAll(ctx, store, desc)
	if err != nil {
		return idx, fmt.Errorf("read catalog index: %w", err)
	}
	if err := json.Unmarshal(data, &idx); err != nil {
		return idx, fmt.Errorf("parse catalog index: %w", err)
	}
	if err := validateSchema(idx); err != nil {
		return idx, err
	}
	return idx, nil
}

// LoadPkgBytes returns the raw pkg.yaml bytes and manifest digest for name
// from the locally synced catalog.
func LoadPkgBytes(ctx context.Context, storePath, name string) ([]byte, string, error) {
	idx, err := LoadIndex(ctx, storePath)
	if err != nil {
		return nil, "", err
	}
	for _, m := range idx.Manifests {
		if m.Annotations[AnnotationTitle] != name {
			continue
		}
		store, err := oci.New(storePath)
		if err != nil {
			return nil, "", fmt.Errorf("open catalog store %s: %w", storePath, err)
		}
		manData, err := content.FetchAll(ctx, store, m)
		if err != nil {
			return nil, "", fmt.Errorf("read manifest for %s: %w", name, err)
		}
		var man ocispec.Manifest
		if err := json.Unmarshal(manData, &man); err != nil {
			return nil, "", fmt.Errorf("parse manifest for %s: %w", name, err)
		}
		for _, layer := range man.Layers {
			if layer.MediaType == MediaTypePkg {
				data, err := content.FetchAll(ctx, store, layer)
				if err != nil {
					return nil, "", fmt.Errorf("read pkg.yaml for %s: %w", name, err)
				}
				return data, m.Digest.String(), nil
			}
		}
		return nil, "", fmt.Errorf("manifest for %s has no pkg.yaml layer", name)
	}
	return nil, "", &PkgNotInIndexError{Name: name}
}
