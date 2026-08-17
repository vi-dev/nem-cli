// Package discover lists a package's upstream versions from its
// versionDiscovery source and orders them.
package discover

import (
	"context"
	"fmt"
	"net/http"

	"github.com/vi-dev/nem-cli/internal/spec"
)

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// List returns pkg's discovered upstream versions, unordered.
func List(ctx context.Context, pkg *spec.Package) ([]string, error) {
	d := pkg.VersionDiscovery
	switch {
	case d == nil:
		return nil, fmt.Errorf("%s has no versionDiscovery", pkg.Name)
	case d.GitHub != nil:
		return githubVersions(ctx, http.DefaultClient, d.GitHub)
	case d.OCI != "":
		return ociTags(ctx, d.OCI)
	}
	return nil, fmt.Errorf("%s has no versionDiscovery source", pkg.Name)
}

// Latest returns the newest discovered version by spec.CompareVersions.
func Latest(ctx context.Context, pkg *spec.Package) (string, error) {
	vs, err := List(ctx, pkg)
	if err != nil {
		return "", err
	}
	if len(vs) == 0 {
		return "", fmt.Errorf("%s: no versions discovered", pkg.Name)
	}
	best := vs[0]
	for _, v := range vs[1:] {
		if spec.CompareVersions(v, best) > 0 {
			best = v
		}
	}
	return best, nil
}
