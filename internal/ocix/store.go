package ocix

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/memory"
	"oras.land/oras-go/v2/content/oci"
	"oras.land/oras-go/v2/errdef"
)

type TitledManifest struct {
	Title string
	Desc  ocispec.Descriptor
}

// enumerateTitledManifests lists idx's manifests by title, deduplicated (first occurrence wins) and sorted.
func enumerateTitledManifests(idx ocispec.Index) []TitledManifest {
	seen := make(map[string]bool, len(idx.Manifests))
	out := make([]TitledManifest, 0, len(idx.Manifests))
	for _, m := range idx.Manifests {
		title := m.Annotations[AnnotationTitle]
		if title == "" || seen[title] {
			continue
		}
		seen[title] = true
		out = append(out, TitledManifest{Title: title, Desc: m})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Title < out[j].Title })
	return out
}

// Store is a validated, point-in-time local copy of a catalog: an oras
// target holding the index closure under its own tag, plus the parsed
// index and a by-title lookup built once at open time.
type Store struct {
	target oras.ReadOnlyTarget
	tag    string
	idx    ocispec.Index
	digest string
	byName map[string]ocispec.Descriptor
}

// newStore builds a Store with the given target, tag, index, and digest.
func newStore(target oras.ReadOnlyTarget, tag string, idx ocispec.Index, digest string) *Store {
	byName := make(map[string]ocispec.Descriptor, len(idx.Manifests))
	for _, tm := range enumerateTitledManifests(idx) {
		byName[tm.Title] = tm.Desc
	}
	return &Store{target: target, tag: tag, idx: idx, digest: digest, byName: byName}
}

// Index returns the store's root index.
func (s *Store) Index() ocispec.Index { return s.idx }

// Digest returns the store's root index digest.
func (s *Store) Digest() string { return s.digest }

// Packages lists the store's packages by title, deduplicated and sorted.
func (s *Store) Packages() []TitledManifest { return enumerateTitledManifests(s.idx) }

// PkgBytes returns the raw pkg.yaml bytes and manifest digest for name.
func (s *Store) PkgBytes(ctx context.Context, name string) ([]byte, string, error) {
	m, ok := s.byName[name]
	if !ok {
		return nil, "", &PkgNotInIndexError{Name: name}
	}
	data, err := FetchPkgBytes(ctx, s.target, m)
	if err != nil {
		return nil, "", fmt.Errorf("load pkg.yaml for %s: %w", name, err)
	}
	return data, m.Digest.String(), nil
}

// CopyTo copies the store's own index closure to dst under dstRef.
func (s *Store) CopyTo(ctx context.Context, dst oras.Target, dstRef string, progress ProgressFunc) (ocispec.Descriptor, error) {
	return CopyIndexClosureWithProgress(ctx, s.target, s.tag, dst, dstRef, progress)
}

// OpenLocalStore opens a Store at storePath. ErrNotSynced reports a store that was never synced.
func OpenLocalStore(ctx context.Context, storePath string) (*Store, error) {
	if _, err := os.Stat(storePath); os.IsNotExist(err) {
		return nil, ErrNotSynced
	}
	store, err := oci.New(storePath)
	if err != nil {
		return nil, fmt.Errorf("open catalog store %s: %w", storePath, err)
	}
	idx, desc, err := FetchCatalogIndex(ctx, store, LocalTag)
	if err != nil {
		if errors.Is(err, errdef.ErrNotFound) {
			return nil, ErrNotSynced
		}
		return nil, err
	}
	return newStore(store, LocalTag, idx, desc.Digest.String()), nil
}

func OpenStore(ctx context.Context, src oras.ReadOnlyTarget, ref string) (*Store, error) {
	idx, desc, err := FetchCatalogIndex(ctx, src, ref)
	if err != nil {
		return nil, err
	}
	return newStore(src, ref, idx, desc.Digest.String()), nil
}

// OpenStoreInMemory opens a Store by copying the index closure from src:ref into a fresh in-memory store.
func OpenStoreInMemory(ctx context.Context, src oras.ReadOnlyTarget, ref string, progress ProgressFunc) (*Store, error) {
	mem := memory.New()
	if _, err := CopyIndexClosureWithProgress(ctx, src, ref, mem, LocalTag, progress); err != nil {
		return nil, err
	}
	return OpenStore(ctx, mem, LocalTag)
}

// SyncLocalCatalog copies the catalog index closure from src into an OCI layout
// store at storePath, tags it LocalTag, and opens it as a Store.
//
// The source index is fetched and schema-validated before anything is
// written to storePath, so a bad-schema remote can never move the local
// tag and clobber a previously-synced good mirror. After the copy, the
// copied digest is checked against the validated one: srcRef could resolve
// to different content between validation and oras.Copy's own re-resolve
// (e.g. a moving tag updated mid-sync), and copying that drift in silently
// would leave storePath tagged with content nem never schema-checked.
func SyncLocalCatalog(ctx context.Context, src oras.ReadOnlyTarget, srcRef, storePath string, progress ProgressFunc) (*Store, error) {
	_, srcDesc, err := FetchCatalogIndex(ctx, src, srcRef)
	if err != nil {
		return nil, err
	}

	dst, err := oci.New(storePath)
	if err != nil {
		return nil, fmt.Errorf("open catalog store %s: %w", storePath, err)
	}
	destDesc, err := CopyIndexClosureWithProgress(ctx, src, srcRef, dst, LocalTag, progress)
	if err != nil {
		return nil, fmt.Errorf("sync catalog: %w", err)
	}
	if destDesc.Digest != srcDesc.Digest {
		return nil, errors.New("catalog changed during sync; retry")
	}
	return OpenLocalStore(ctx, storePath)
}

// FetchPkgBytes fetches the image manifest at man from src and returns
// the raw pkg.yaml bytes of its MediaTypePkg layer.
func FetchPkgBytes(ctx context.Context, src oras.ReadOnlyTarget, man ocispec.Descriptor) ([]byte, error) {
	manData, err := content.FetchAll(ctx, src, man)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	var m ocispec.Manifest
	if err := json.Unmarshal(manData, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	for _, layer := range m.Layers {
		if layer.MediaType == MediaTypePkg {
			data, err := content.FetchAll(ctx, src, layer)
			if err != nil {
				return nil, fmt.Errorf("read pkg.yaml layer: %w", err)
			}
			return data, nil
		}
	}
	return nil, errors.New("manifest has no pkg.yaml layer")
}
