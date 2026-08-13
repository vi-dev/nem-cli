package ocix

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"

	"github.com/vi-dev/nem-cli/internal/spec"
)

// RemoteArchivesRW opens the archives repo for a package as a writable oras
// target — the writable sibling of the read-only RemoteArchives.
func RemoteArchivesRW(catalogRef, name string) (oras.Target, error) {
	archivesRef, err := ArchivesRef(catalogRef, name)
	if err != nil {
		return nil, err
	}
	return NewRemoteRepository(archivesRef)
}

// PushArchive publishes archiveBytes as plat's entry of the multi-platform
// archive index tagged tag, merging into any existing index so other
// platforms are preserved. It returns the platform manifest descriptor and
// whether a push happened — false means plat was already published
// byte-identical and force was not set.
func PushArchive(ctx context.Context, target oras.Target, tag string, plat spec.Platform, archiveBytes []byte, force bool) (ocispec.Descriptor, bool, error) {
	layerDesc := content.NewDescriptorFromBytes(MediaTypeArchive, archiveBytes)
	manBytes, err := json.Marshal(ocispec.Manifest{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispec.MediaTypeImageManifest,
		Config:    ocispec.DescriptorEmptyJSON,
		Layers:    []ocispec.Descriptor{layerDesc},
	})
	if err != nil {
		return ocispec.Descriptor{}, false, fmt.Errorf("marshal archive manifest: %w", err)
	}
	manDesc := content.NewDescriptorFromBytes(ocispec.MediaTypeImageManifest, manBytes)

	idx, err := loadIndex(ctx, target, tag)
	if err != nil {
		return ocispec.Descriptor{}, false, err
	}
	if cur, ok := manifestForPlatform(idx, plat); ok && cur.Digest == manDesc.Digest && !force {
		return withPlatform(manDesc, plat), false, nil
	}

	if err := PushEmptyConfig(ctx, target); err != nil {
		return ocispec.Descriptor{}, false, err
	}
	if err := pushBlobIfAbsent(ctx, target, layerDesc, archiveBytes); err != nil {
		return ocispec.Descriptor{}, false, err
	}
	if err := pushBlobIfAbsent(ctx, target, manDesc, manBytes); err != nil {
		return ocispec.Descriptor{}, false, err
	}

	entry := withPlatform(manDesc, plat)
	merged := mergeManifests(idx.Manifests, entry, plat)
	idxBytes, err := json.Marshal(ocispec.Index{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispec.MediaTypeImageIndex,
		Manifests: merged,
	})
	if err != nil {
		return ocispec.Descriptor{}, false, fmt.Errorf("marshal archive index: %w", err)
	}
	idxDesc := content.NewDescriptorFromBytes(ocispec.MediaTypeImageIndex, idxBytes)
	if err := PushIndex(ctx, target, idxBytes, idxDesc, []string{tag}); err != nil {
		return ocispec.Descriptor{}, false, err
	}
	return entry, true, nil
}

// withPlatform copies d and stamps plat on it (index-entry metadata; the
// referenced manifest blob is unaffected, since Platform is not part of the
// digested bytes).
func withPlatform(d ocispec.Descriptor, plat spec.Platform) ocispec.Descriptor {
	d.Platform = &ocispec.Platform{OS: plat.OS, Architecture: plat.Arch}
	return d
}

// mergeManifests drops any existing entry for plat, appends entry, and keeps
// the list sorted by os/arch so a fixed platform set serializes identically.
func mergeManifests(mans []ocispec.Descriptor, entry ocispec.Descriptor, plat spec.Platform) []ocispec.Descriptor {
	out := make([]ocispec.Descriptor, 0, len(mans)+1)
	for _, m := range mans {
		if m.Platform != nil && m.Platform.OS == plat.OS && m.Platform.Architecture == plat.Arch {
			continue
		}
		out = append(out, m)
	}
	out = append(out, entry)
	sort.Slice(out, func(i, j int) bool { return platKey(out[i]) < platKey(out[j]) })
	return out
}

func platKey(d ocispec.Descriptor) string {
	if d.Platform == nil {
		return ""
	}
	return d.Platform.OS + "/" + d.Platform.Architecture
}

// loadIndex reads the archive index at tag, or returns an empty index and a
// nil error when the tag or repo is absent.
func loadIndex(ctx context.Context, target oras.Target, tag string) (ocispec.Index, error) {
	desc, err := target.Resolve(ctx, tag)
	if err != nil {
		if archiveAbsent(err) {
			return ocispec.Index{}, nil
		}
		return ocispec.Index{}, fmt.Errorf("resolve archive index %s: %w", tag, err)
	}
	data, err := content.FetchAll(ctx, target, desc)
	if err != nil {
		return ocispec.Index{}, fmt.Errorf("read archive index %s: %w", tag, err)
	}
	var idx ocispec.Index
	if err := json.Unmarshal(data, &idx); err != nil {
		return ocispec.Index{}, fmt.Errorf("parse archive index %s: %w", tag, err)
	}
	return idx, nil
}
