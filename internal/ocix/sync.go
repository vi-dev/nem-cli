package ocix

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"

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

// ValidateBaseRef checks that ref names a bare repository — no tag or
// digest. Publish writes the repository and moves the tags named via
// --tag, so a tag on the ref would be dead syntax at best and misleading
// at worst (":v2-staging" would not move v2-staging).
func ValidateBaseRef(ref string) error {
	parsed, err := registry.ParseReference(ref)
	if err != nil {
		return fmt.Errorf("parse oci ref %q: %w", ref, err)
	}
	if parsed.Reference != "" {
		return fmt.Errorf("oci ref %q must be a bare repository ref", ref)
	}
	return nil
}

// loopbackRegistry reports whether host (the registry part of a parsed
// reference, possibly with a port) names the local machine: "localhost"
// or a loopback IP (127.0.0.0/8, ::1).
func loopbackRegistry(host string) bool {
	h := host
	if hostOnly, _, err := net.SplitHostPort(host); err == nil {
		h = hostOnly
	}
	h = strings.Trim(h, "[]")
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// NewRemoteRepository opens ref as a *remote.Repository with docker-config
// credentials, consulted only for registries the user has a stored docker
// login for — every other registry is accessed anonymously, so a configured
// credential helper is never exec'd for public pulls. Loopback registries
// (localhost, 127.0.0.0/8, ::1) are contacted over plain HTTP so local
// development registries work without TLS; every other host is HTTPS-only.
func NewRemoteRepository(ref string) (*remote.Repository, error) {
	repo, err := remote.NewRepository(ref)
	if err != nil {
		return nil, fmt.Errorf("parse oci ref %q: %w", ref, err)
	}
	repo.PlainHTTP = loopbackRegistry(repo.Reference.Registry)
	credStore, err := credentials.NewStoreFromDocker(credentials.StoreOptions{})
	if err != nil {
		return nil, fmt.Errorf("open docker credentials: %w", err)
	}
	configPath, err := dockerConfigPath()
	if err != nil {
		return nil, err
	}
	stored, err := storedAuthHosts(configPath)
	if err != nil {
		return nil, fmt.Errorf("read docker config: %w", err)
	}
	repo.Client = &auth.Client{
		Credential: gatedCredential(stored, credentials.Credential(credStore)),
		Cache:      auth.NewCache(),
	}
	return repo, nil
}

// RemoteCatalog opens ref (e.g. "ghcr.io/org/cat:v2") as a read-only oras
// target with docker-config credentials, returning the target and the
// reference (tag or digest) to copy from.
func RemoteCatalog(ref string) (oras.ReadOnlyTarget, string, error) {
	repo, err := NewRemoteRepository(ref)
	if err != nil {
		return nil, "", err
	}
	return repo, repo.Reference.Reference, nil
}

// RemoteCatalogRW opens ref as a writable oras target with the same
// docker-config credentials as RemoteCatalog, for publishing a catalog.
func RemoteCatalogRW(ref string) (oras.Target, string, error) {
	repo, err := NewRemoteRepository(ref)
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

// FetchIndex resolves srcRef on src, fetches and parses the catalog
// index, and checks its schema annotation. It reads only the index blob —
// nothing else from the catalog closure.
func FetchIndex(ctx context.Context, src oras.ReadOnlyTarget, srcRef string) (ocispec.Index, ocispec.Descriptor, error) {
	var idx ocispec.Index
	desc, err := src.Resolve(ctx, srcRef)
	if err != nil {
		return idx, ocispec.Descriptor{}, fmt.Errorf("resolve catalog ref %s: %w", srcRef, err)
	}
	data, err := content.FetchAll(ctx, src, desc)
	if err != nil {
		return idx, ocispec.Descriptor{}, fmt.Errorf("read catalog index: %w", err)
	}
	if err := json.Unmarshal(data, &idx); err != nil {
		return idx, ocispec.Descriptor{}, fmt.Errorf("parse catalog index: %w", err)
	}
	if err := validateSchema(idx); err != nil {
		return idx, ocispec.Descriptor{}, err
	}
	return idx, desc, nil
}

// validateSrcSchema resolves srcRef on src, fetches the index bytes, and
// checks the catalog schema version, without touching any local state. It
// returns the resolved descriptor so the caller can verify the digest
// actually copied still matches what was validated.
func validateSrcSchema(ctx context.Context, src oras.ReadOnlyTarget, srcRef string) (ocispec.Descriptor, error) {
	_, desc, err := FetchIndex(ctx, src, srcRef)
	return desc, err
}

// validateSchema checks idx carries the catalog schema version this build
// understands. Shared by SyncFrom (pre-copy, against the remote source)
// and OpenStore (post-sync, against the local mirror).
func validateSchema(idx ocispec.Index) error {
	if got := idx.Annotations[AnnotationSchemaVersion]; got != SchemaVersion {
		return fmt.Errorf("unsupported catalog schema %q (want %s; a newer nem may be required)", got, SchemaVersion)
	}
	return nil
}

// Store is a locally synced catalog mirror opened for reading. Opening
// scans the oras layout and parses the index once; every package load
// shares that work instead of repeating it. A Store is a point-in-time
// snapshot: a mirror resynced after opening is not observed.
type Store struct {
	store  *oci.Store
	idx    ocispec.Index
	byName map[string]ocispec.Descriptor
}

// OpenStore opens the synced mirror at storePath and loads its validated
// index. ErrNotSynced reports a mirror that was never synced.
func OpenStore(ctx context.Context, storePath string) (*Store, error) {
	if _, err := os.Stat(storePath); os.IsNotExist(err) {
		return nil, ErrNotSynced
	}
	store, err := oci.New(storePath)
	if err != nil {
		return nil, fmt.Errorf("open catalog store %s: %w", storePath, err)
	}
	desc, err := store.Resolve(ctx, LocalTag)
	if err != nil {
		if errors.Is(err, errdef.ErrNotFound) {
			return nil, ErrNotSynced
		}
		return nil, fmt.Errorf("resolve catalog index: %w", err)
	}
	data, err := content.FetchAll(ctx, store, desc)
	if err != nil {
		return nil, fmt.Errorf("read catalog index: %w", err)
	}
	var idx ocispec.Index
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, fmt.Errorf("parse catalog index: %w", err)
	}
	if err := validateSchema(idx); err != nil {
		return nil, err
	}
	byName := make(map[string]ocispec.Descriptor, len(idx.Manifests))
	for _, m := range idx.Manifests {
		name := m.Annotations[AnnotationTitle]
		if name == "" {
			continue
		}
		if _, ok := byName[name]; !ok {
			byName[name] = m
		}
	}
	return &Store{store: store, idx: idx, byName: byName}, nil
}

// Index returns the mirror's catalog index.
func (s *Store) Index() ocispec.Index { return s.idx }

// PkgBytes returns the raw pkg.yaml bytes and manifest digest for name.
func (s *Store) PkgBytes(ctx context.Context, name string) ([]byte, string, error) {
	m, ok := s.byName[name]
	if !ok {
		return nil, "", &PkgNotInIndexError{Name: name}
	}
	data, err := FetchPkgBytes(ctx, s.store, m)
	if err != nil {
		return nil, "", fmt.Errorf("load pkg.yaml for %s: %w", name, err)
	}
	return data, m.Digest.String(), nil
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
