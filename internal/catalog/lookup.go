package catalog

import (
	"context"
	"errors"

	"github.com/vi-dev/nem-cli/internal/home"
	"github.com/vi-dev/nem-cli/internal/project"
	"github.com/vi-dev/nem-cli/internal/spec"
)

// Open builds a Source per configured catalog, in config (precedence) order.
func Open(cfg *Config, h home.Home) []Named {
	out := make([]Named, 0, len(cfg.Catalogs))
	for _, e := range cfg.Catalogs {
		switch e.Type {
		case "dir":
			out = append(out, Named{Name: e.Name, Source: NewDir(e.Path)})
		case "oci":
			out = append(out, Named{Name: e.Name, Source: NewOCI(e.Name, h.CatalogStore(e.Name))})
		}
	}
	return out
}

// Lookup resolves a tool key against the configured catalogs.
func Lookup(ctx context.Context, sources []Named, key project.ToolKey) (*spec.Package, string, string, error) {
	if key.Catalog != "" {
		for _, n := range sources {
			if n.Name != key.Catalog {
				continue
			}
			pkg, dig, err := n.Source.Load(ctx, key.Name)
			var nf *PackageNotFoundError
			if errors.As(err, &nf) {
				nf.Catalogs = []string{n.Name}
				return nil, "", "", nf
			}
			if err != nil {
				return nil, "", "", err
			}
			return pkg, n.Name, dig, nil
		}
		return nil, "", "", &CatalogNotFoundError{Name: key.Catalog}
	}
	searched := make([]string, 0, len(sources))
	for _, n := range sources {
		searched = append(searched, n.Name)
		pkg, dig, err := n.Source.Load(ctx, key.Name)
		var nf *PackageNotFoundError
		if errors.As(err, &nf) {
			continue
		}
		if err != nil {
			return nil, "", "", err
		}
		return pkg, n.Name, dig, nil
	}
	return nil, "", "", &PackageNotFoundError{Name: key.Name, Catalogs: searched}
}
