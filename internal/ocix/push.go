package ocix

import (
	"bytes"
	"context"
	"errors"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/errdef"
)

// PushEmptyConfig pushes the empty JSON config blob into target, if it is not already present.
func PushEmptyConfig(ctx context.Context, target oras.Target) error {
	return pushBlobIfAbsent(ctx, target, ocispec.DescriptorEmptyJSON, ocispec.DescriptorEmptyJSON.Data)
}

// PushBlobAndTag pushes bytes under desc into target, if it is not already present, and tags the blob with each tag in tags.
func PushBlobAndTag(ctx context.Context, target oras.Target, bytes []byte, desc ocispec.Descriptor, tags []string) error {
	if err := pushBlobIfAbsent(ctx, target, desc, bytes); err != nil {
		return err
	}
	for _, tag := range tags {
		if err := target.Tag(ctx, desc, tag); err != nil {
			return err
		}
	}
	return nil
}

// pushBlobIfAbsent pushes data under desc into target only if target.Exists reports it is absent.
// It returns any error from Exists or Push, except that ErrAlreadyExists from Push is ignored
// (the blob is already present, so the push is effectively a no-op).
func pushBlobIfAbsent(ctx context.Context, target oras.Target, desc ocispec.Descriptor, data []byte) error {
	ok, err := target.Exists(ctx, desc)
	if err != nil {
		return err
	}
	if ok {
		return nil
	}
	if err := target.Push(ctx, desc, bytes.NewReader(data)); err != nil && !errors.Is(err, errdef.ErrAlreadyExists) {
		return err
	}
	return nil
}
