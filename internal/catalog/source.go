// Package catalog resolves configured sources and looks up packages in
// them; config.yaml's document model lives in internal/config.
package catalog

import (
	"context"

	"github.com/vi-dev/nem-cli/internal/spec"
)

// Summary is one package's listing entry: enough for search and lists
// without loading the full manifest.
type Summary struct{ Name, Description, Latest string }

// Source is a catalog: an ordered, named source of package manifests.
type Source interface {
	Summaries(ctx context.Context) ([]Summary, error)
	Versions(ctx context.Context, name string) ([]string, error)
	Load(ctx context.Context, name string) (*spec.Package, string, error)
}

// Named pairs a Source with its configured catalog name.
type Named struct {
	Name   string
	Source Source
}

// versionsOf extracts the version strings from a package's version list, in
// the order they appear.
func versionsOf(pkg *spec.Package) []string {
	out := make([]string, len(pkg.Versions))
	for i, v := range pkg.Versions {
		out[i] = v.Version
	}
	return out
}
