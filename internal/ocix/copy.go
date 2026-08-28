package ocix

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync/atomic"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
)

// CopyIndexClosure copies the index closure from src:srcRef to dst:dstRef, returning the copied root descriptor.
func CopyIndexClosure(ctx context.Context, src oras.ReadOnlyTarget, srcRef string, dst oras.Target, dstRef string) (ocispec.Descriptor, error) {
	opts := oras.DefaultCopyOptions
	opts.Concurrency = syncConcurrency
	return oras.Copy(ctx, src, srcRef, dst, dstRef, opts)
}

// CopyIndexClosureWithProgress copies the index closure from src:srcRef to dst:dstRef,
// additionally reporting progress through fn: done/total track the catalog's own package count, not raw blob count.
func CopyIndexClosureWithProgress(ctx context.Context, src oras.ReadOnlyTarget, srcRef string, dst oras.Target, dstRef string, fn ProgressFunc) (ocispec.Descriptor, error) {
	if fn == nil {
		return CopyIndexClosure(ctx, src, srcRef, dst, dstRef)
	}

	var total, done atomic.Int64
	observed := &observedTarget{
		ReadOnlyTarget: src,
		onFirstFetch: func(data []byte) {
			var idx ocispec.Index
			if err := json.Unmarshal(data, &idx); err != nil {
				return // not a parseable index; total stays unknown (0)
			}
			total.Store(int64(len(idx.Manifests)) + 1)
			fn(done.Load(), total.Load())
		},
	}

	opts := oras.DefaultCopyOptions
	opts.Concurrency = syncConcurrency
	tick := func(_ context.Context, desc ocispec.Descriptor) error {
		if desc.MediaType != ocispec.MediaTypeImageIndex && desc.MediaType != ocispec.MediaTypeImageManifest {
			return nil
		}
		fn(done.Add(1), total.Load())
		return nil
	}
	opts.PostCopy = tick
	opts.OnCopySkipped = tick
	opts.OnMounted = tick

	return oras.Copy(ctx, observed, srcRef, dst, dstRef, opts)
}

// observedTarget calls onFirstFetch once with the root's fetched bytes —
// oras.Copy always fetches the root first to find its successors. Every
// Fetch, including the first, still returns the real content unchanged.
type observedTarget struct {
	oras.ReadOnlyTarget
	observed     atomic.Bool
	onFirstFetch func(data []byte)
}

func (f *observedTarget) Fetch(ctx context.Context, desc ocispec.Descriptor) (io.ReadCloser, error) {
	rc, err := f.ReadOnlyTarget.Fetch(ctx, desc)
	if err != nil {
		return nil, err
	}
	if !f.observed.CompareAndSwap(false, true) {
		return rc, nil
	}
	data, readErr := io.ReadAll(rc)
	closeErr := rc.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	f.onFirstFetch(data)
	return io.NopCloser(bytes.NewReader(data)), nil
}

// CopyTag copies the index closure from src:tag to dst:tag, returning the copied root descriptor.
// The copy is retried on transient errors.
func CopyTag(ctx context.Context, src oras.ReadOnlyTarget, dst oras.Target, tag string) (ocispec.Descriptor, error) {
	var result ocispec.Descriptor
	err := withRetry(ctx, func(ctx context.Context) error {
		copied, err := CopyIndexClosure(ctx, src, tag, dst, tag)
		if err != nil {
			return fmt.Errorf("copy tag %s: %w", tag, err)
		}
		got, err := dst.Resolve(ctx, tag)
		if err != nil {
			return fmt.Errorf("verify copied tag %s: %w", tag, err)
		}
		if got.Digest != copied.Digest {
			return fmt.Errorf("tag %s digest mismatch after copy: copied %s, resolved %s", tag, copied.Digest, got.Digest)
		}
		result = copied
		return nil
	})
	return result, err
}
