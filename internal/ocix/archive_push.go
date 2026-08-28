package ocix

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"

	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/errdef"

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

// PushArchive merges archiveBytes into tag's index as plat's entry.
// pushed is false when plat was already published byte-identical and
// force wasn't set.
func PushArchive(ctx context.Context, target oras.Target, tag string, plat spec.Platform, archiveBytes []byte, force bool) (ocispec.Descriptor, bool, error) {
	layerDesc := content.NewDescriptorFromBytes(MediaTypeArchive, archiveBytes)
	manBytes, manDesc, err := archiveManifest(layerDesc)
	if err != nil {
		return ocispec.Descriptor{}, false, err
	}

	idx, err := loadArchiveIndex(ctx, target, tag)
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
	if _, err := CommitArchiveManifests(ctx, target, tag, []ocispec.Descriptor{entry}); err != nil {
		return ocispec.Descriptor{}, false, err
	}
	return entry, true, nil
}

// PublishArchiveLayerFile pushes path's contents as plat's layer and
// manifest into target — never touching tag's index; the caller batches
// several platforms into one CommitArchiveManifests call.
func PublishArchiveLayerFile(ctx context.Context, target oras.Target, plat spec.Platform, path, sha256Hex string, size int64) (ocispec.Descriptor, error) {
	layerDesc := ocispec.Descriptor{
		MediaType: MediaTypeArchive,
		Digest:    digest.NewDigestFromEncoded(digest.SHA256, sha256Hex),
		Size:      size,
	}
	manBytes, manDesc, err := archiveManifest(layerDesc)
	if err != nil {
		return ocispec.Descriptor{}, err
	}

	err = withRetry(ctx, func(ctx context.Context) error {
		if err := PushEmptyConfig(ctx, target); err != nil {
			return err
		}
		if err := pushArchiveLayerFile(ctx, target, layerDesc, path); err != nil {
			return err
		}
		return pushBlobIfAbsent(ctx, target, manDesc, manBytes)
	})
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	return withPlatform(manDesc, plat), nil
}

// pushArchiveLayerFile streams path in; path is reopened fresh on every
// call, so a retry never gets a partially-consumed reader.
func pushArchiveLayerFile(ctx context.Context, target oras.Target, layerDesc ocispec.Descriptor, path string) error {
	exists, err := target.Exists(ctx, layerDesc)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	if err := target.Push(ctx, layerDesc, f); err != nil && !errors.Is(err, errdef.ErrAlreadyExists) {
		return err
	}
	return nil
}

func archiveManifest(layerDesc ocispec.Descriptor) ([]byte, ocispec.Descriptor, error) {
	manBytes, err := json.Marshal(ocispec.Manifest{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispec.MediaTypeImageManifest,
		Config:    ocispec.DescriptorEmptyJSON,
		Layers:    []ocispec.Descriptor{layerDesc},
	})
	if err != nil {
		return nil, ocispec.Descriptor{}, fmt.Errorf("marshal archive manifest: %w", err)
	}
	return manBytes, content.NewDescriptorFromBytes(ocispec.MediaTypeImageManifest, manBytes), nil
}

// CommitArchiveManifests merges entries into tag's index with one
// push+retag, regardless of batch size. A no-op merge pushes nothing;
// called with no entries, it's a no-op entirely.
func CommitArchiveManifests(ctx context.Context, target oras.Target, tag string, entries []ocispec.Descriptor) (ocispec.Descriptor, error) {
	if len(entries) == 0 {
		return ocispec.Descriptor{}, nil
	}

	var result ocispec.Descriptor
	err := withRetry(ctx, func(ctx context.Context) error {
		cur, curErr := target.Resolve(ctx, tag)
		var idx ocispec.Index
		switch {
		case curErr == nil:
			data, err := content.FetchAll(ctx, target, cur)
			if err != nil {
				return fmt.Errorf("read archive index %s: %w", tag, err)
			}
			if err := json.Unmarshal(data, &idx); err != nil {
				return fmt.Errorf("parse archive index %s: %w", tag, err)
			}
		case archiveAbsent(curErr):
			// No current index: every entry is new.
		default:
			return fmt.Errorf("resolve archive index %s: %w", tag, curErr)
		}

		merged := idx.Manifests
		for _, entry := range entries {
			merged = mergeManifests(merged, entry)
		}
		idxBytes, err := json.Marshal(ocispec.Index{
			Versioned: specs.Versioned{SchemaVersion: 2},
			MediaType: ocispec.MediaTypeImageIndex,
			Manifests: merged,
		})
		if err != nil {
			return fmt.Errorf("marshal archive index: %w", err)
		}
		idxDesc := content.NewDescriptorFromBytes(ocispec.MediaTypeImageIndex, idxBytes)

		if curErr == nil && cur.Digest == idxDesc.Digest {
			result = idxDesc
			return nil
		}
		if err := PushBlobAndTag(ctx, target, idxBytes, idxDesc, []string{tag}); err != nil {
			return err
		}
		result = idxDesc
		return nil
	})
	return result, err
}

// withPlatform copies d and stamps plat on it (index-entry metadata; the
// referenced manifest blob is unaffected, since Platform is not part of the
// digested bytes).
func withPlatform(d ocispec.Descriptor, plat spec.Platform) ocispec.Descriptor {
	d.Platform = &ocispec.Platform{OS: plat.OS, Architecture: plat.Arch}
	return d
}

// mergeManifests drops any existing entry sharing entry's platform and
// keeps the result sorted, so a fixed platform set serializes identically.
func mergeManifests(mans []ocispec.Descriptor, entry ocispec.Descriptor) []ocispec.Descriptor {
	out := make([]ocispec.Descriptor, 0, len(mans)+1)
	for _, m := range mans {
		if m.Platform != nil && platKey(m) == platKey(entry) {
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

// loadArchiveIndex reads the archive index at tag, or returns an empty index and a
// nil error when the tag or repo is absent.
func loadArchiveIndex(ctx context.Context, target oras.Target, tag string) (ocispec.Index, error) {
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
