package install

import (
	"os"

	"github.com/vi-dev/nem-cli/internal/archive"
	"github.com/vi-dev/nem-cli/internal/spec"
)

// extract streams artifactPath into root, dropping strip leading path
// components from every archive entry. When the artifact is a compressed
// single file rather than an archive, its decompressed content lands as
// singleName instead.
func extract(artifactPath string, root *os.Root, strip int, singleName string) error {
	_, err := archive.Extract(artifactPath, root, archive.Options{Strip: strip, SingleName: singleName})
	return err
}

// singleFileName derives the staged output name a compressed single-file
// artifact extracts to: the artifact's URL (or GitHub asset) basename
// minus its compression extension. It returns "" when the package's
// artifact reference can't be resolved — e.g. a catalog-archive install,
// which is always a tarball and never needs the name.
func singleFileName(pkg *spec.Package, version string, plat spec.Platform) string {
	if pkg.Artifact.GitHub != nil {
		name, err := pkg.AssetName(version, plat)
		if err != nil {
			return ""
		}
		return archive.SingleNameFromRef(name)
	}
	if pkg.Artifact.URL == "" {
		return ""
	}
	raw, err := pkg.ArtifactURL(version, plat)
	if err != nil {
		return ""
	}
	return archive.SingleNameFromRef(raw)
}
