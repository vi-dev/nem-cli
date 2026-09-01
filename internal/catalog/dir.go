package catalog

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/vi-dev/nem-cli/internal/spec"
)

// Dir is a local directory catalog laid out as pkgs/<name>/pkg.yaml.
type Dir struct{ root string }

var _ Source = (*Dir)(nil)
var _ NameLister = (*Dir)(nil)

func NewDir(root string) *Dir { return &Dir{root: root} }

func (d *Dir) pkgPath(name string) string {
	return filepath.Join(d.root, "pkgs", name, "pkg.yaml")
}

func (d *Dir) Load(_ context.Context, name string) (*spec.Package, string, error) {
	data, err := os.ReadFile(d.pkgPath(name))
	if os.IsNotExist(err) {
		return nil, "", &PackageNotFoundError{Name: name}
	}
	if err != nil {
		return nil, "", fmt.Errorf("read %s: %w", d.pkgPath(name), err)
	}
	pkg, err := spec.Parse(data)
	if err != nil {
		return nil, "", fmt.Errorf("load %s: %w", d.pkgPath(name), err)
	}
	if err := pkg.Validate(); err != nil {
		return nil, "", fmt.Errorf("load %s: %w", d.pkgPath(name), err)
	}
	if pkg.Name != name {
		return nil, "", fmt.Errorf("load %s: manifest declares name %q, want %q", d.pkgPath(name), pkg.Name, name)
	}
	return pkg, "", nil
}

func (d *Dir) Versions(ctx context.Context, name string) ([]string, error) {
	pkg, _, err := d.Load(ctx, name)
	if err != nil {
		return nil, err
	}
	return versionsOf(pkg), nil
}

func (d *Dir) isPackage(e os.DirEntry) bool {
	if !e.IsDir() || !spec.NameRE.MatchString(e.Name()) {
		return false
	}
	_, err := os.Stat(d.pkgPath(e.Name()))
	return err == nil
}

func (d *Dir) Summaries(ctx context.Context) ([]Summary, error) {
	entries, err := os.ReadDir(filepath.Join(d.root, "pkgs"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read catalog dir %s: %w", d.root, err)
	}
	var out []Summary
	for _, e := range entries {
		if !d.isPackage(e) {
			continue
		}
		pkg, _, err := d.Load(ctx, e.Name())
		if err != nil {
			continue
		}
		latest := ""
		if len(pkg.Versions) > 0 {
			latest = pkg.Versions[0].Version
		}
		out = append(out, Summary{Name: pkg.Name, Description: pkg.Description, Latest: latest})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// PackageNames lists package directory names without parsing manifests, so
// completion stays fast on large catalogs.
func (d *Dir) PackageNames(_ context.Context) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(d.root, "pkgs"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read catalog dir %s: %w", d.root, err)
	}
	var out []string
	for _, e := range entries {
		if !d.isPackage(e) {
			continue
		}
		out = append(out, e.Name())
	}
	return out, nil
}
