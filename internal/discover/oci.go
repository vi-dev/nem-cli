package discover

import (
	"context"
	"fmt"

	"github.com/vi-dev/nem-cli/internal/ocix"
)

// ociTags lists the tags of an OCI repository ref.
func ociTags(ctx context.Context, repo string) ([]string, error) {
	r, err := ocix.NewRemoteRepository(repo)
	if err != nil {
		return nil, err
	}
	var out []string
	err = r.Tags(ctx, "", func(tags []string) error {
		out = append(out, tags...)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list tags for %s: %w", repo, err)
	}
	return out, nil
}
