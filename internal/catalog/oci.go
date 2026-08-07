package catalog

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/vi-dev/nem-cli/internal/ocix"
	"github.com/vi-dev/nem-cli/internal/spec"
)

// OCI is a registry-backed catalog served from its local synced mirror.
type OCI struct {
	name      string
	storePath string

	mu   sync.Mutex
	memo map[string]*spec.Package // by pkg manifest digest
}

var _ Source = (*OCI)(nil)

func NewOCI(name, storePath string) *OCI {
	return &OCI{name: name, storePath: storePath, memo: map[string]*spec.Package{}}
}

func (o *OCI) Summaries(ctx context.Context) ([]Summary, error) {
	idx, err := ocix.LoadIndex(ctx, o.storePath)
	if err != nil {
		return nil, err
	}
	var out []Summary
	for _, m := range idx.Manifests {
		name := m.Annotations[ocix.AnnotationTitle]
		if name == "" {
			continue
		}
		out = append(out, Summary{
			Name:        name,
			Description: m.Annotations[ocix.AnnotationDescription],
			Latest:      m.Annotations[ocix.AnnotationVersion],
		})
	}
	return out, nil
}

func (o *OCI) Load(ctx context.Context, name string) (*spec.Package, string, error) {
	data, dig, err := ocix.LoadPkgBytes(ctx, o.storePath, name)
	var notIn *ocix.PkgNotInIndexError
	if errors.As(err, &notIn) {
		return nil, "", &PackageNotFoundError{Name: name}
	}
	if err != nil {
		return nil, "", err
	}
	o.mu.Lock()
	pkg, ok := o.memo[dig]
	o.mu.Unlock()
	if ok {
		return pkg, dig, nil
	}
	pkg, err = spec.Parse(data)
	if err != nil {
		return nil, "", fmt.Errorf("catalog %s: %w", o.name, err)
	}
	if err := pkg.Validate(); err != nil {
		return nil, "", fmt.Errorf("catalog %s: package %s: %w", o.name, name, err)
	}
	if pkg.Name != name {
		return nil, "", fmt.Errorf("catalog %s: package %s: manifest declares name %q, want %q", o.name, name, pkg.Name, name)
	}
	o.mu.Lock()
	o.memo[dig] = pkg
	o.mu.Unlock()
	return pkg, dig, nil
}

func (o *OCI) Versions(ctx context.Context, name string) ([]string, error) {
	pkg, _, err := o.Load(ctx, name)
	if err != nil {
		return nil, err
	}
	return versionsOf(pkg), nil
}
