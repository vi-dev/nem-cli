package install

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/goccy/go-yaml"

	"github.com/vi-dev/nem-cli/internal/home"
	"github.com/vi-dev/nem-cli/internal/spec"
)

// metaFileName is the sidecar Install writes into every install dir.
const metaFileName = ".nem-meta.yaml"

// Meta is the sidecar recorded alongside an installed package's files,
// letting store queries answer without re-parsing pkg.yaml.
type Meta struct {
	Package     string           `yaml:"package"`
	Version     string           `yaml:"version"`
	Catalog     string           `yaml:"catalog"`
	Bins        []string         `yaml:"bins"`
	Libs        []string         `yaml:"libs,omitempty"`
	Env         []spec.EnvExport `yaml:"env,omitempty"`
	InstalledAt time.Time        `yaml:"installed_at"`
}

// Install stages pkg's install actions into a temp directory beside its
// final install dir, writes the .nem-meta.yaml sidecar into staging, then
// commits by renaming staging onto the install dir. On any failure staging
// is removed, so a failed install leaves neither a partial install dir nor
// a stray staging directory.
//
// os.Rename is the commit primitive rather than a recursive copy precisely
// because it moves the staged tree as-is without resolving symlinks inside
// it: a staged symlink whose target would resolve outside staging is moved
// inert, not followed.
func Install(ctx context.Context, h home.Home, pkg *spec.Package, version, catalog, artifactPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	installDir, err := h.PackageDir(pkg.Name, version)
	if err != nil {
		return fmt.Errorf("install %s@%s: %w", pkg.Name, version, err)
	}
	if _, err := os.Lstat(installDir); err == nil {
		return commitExistsErr(pkg.Name, version)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat install dir: %w", err)
	}

	parent := filepath.Dir(installDir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("create packages dir: %w", err)
	}
	staging, err := os.MkdirTemp(parent, version+"-*.tmp")
	if err != nil {
		return fmt.Errorf("create staging dir: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			// Best-effort cleanup: staging is already scratch space at this
			// point, so a failure here has nothing further to report to.
			os.RemoveAll(staging)
		}
	}()

	if err := RunActions(pkg, staging, artifactPath); err != nil {
		return fmt.Errorf("install %s@%s: %w", pkg.Name, version, err)
	}
	if err := writeMeta(staging, pkg, version, catalog); err != nil {
		return fmt.Errorf("install %s@%s: %w", pkg.Name, version, err)
	}

	if err := os.Rename(staging, installDir); err != nil {
		if errors.Is(err, os.ErrExist) {
			return commitExistsErr(pkg.Name, version)
		}
		return fmt.Errorf("commit %s@%s: %w", pkg.Name, version, err)
	}
	committed = true
	return nil
}

// commitExistsErr is the race error from both the fast pre-staging check
// and the os.Rename-onto-existing-target commit failure.
func commitExistsErr(name, version string) error {
	return fmt.Errorf("commit %s@%s: install dir already exists", name, version)
}

// writeMeta renders pkg's install metadata and writes it into staging as
// metaFileName.
func writeMeta(staging string, pkg *spec.Package, version, catalog string) error {
	meta := Meta{
		Package:     pkg.Name,
		Version:     version,
		Catalog:     catalog,
		Bins:        pkg.Bins,
		Libs:        pkg.Libs,
		Env:         pkg.Env,
		InstalledAt: time.Now().UTC(),
	}
	data, err := yaml.Marshal(meta)
	if err != nil {
		return fmt.Errorf("render sidecar: %w", err)
	}
	if err := os.WriteFile(filepath.Join(staging, metaFileName), data, 0o644); err != nil {
		return fmt.Errorf("write sidecar: %w", err)
	}
	return nil
}

// IsInstalled reports whether name@version's install dir exists.
func IsInstalled(h home.Home, name, version string) bool {
	installDir, err := h.PackageDir(name, version)
	if err != nil {
		return false
	}
	_, err = os.Lstat(installDir)
	return err == nil
}

// ReadMeta reads and parses name@version's .nem-meta.yaml sidecar.
func ReadMeta(h home.Home, name, version string) (*Meta, error) {
	installDir, err := h.PackageDir(name, version)
	if err != nil {
		return nil, fmt.Errorf("read meta %s@%s: %w", name, version, err)
	}
	data, err := os.ReadFile(filepath.Join(installDir, metaFileName))
	if err != nil {
		return nil, fmt.Errorf("read meta %s@%s: %w", name, version, err)
	}
	var meta Meta
	if err := yaml.UnmarshalWithOptions(data, &meta, yaml.Strict()); err != nil {
		return nil, fmt.Errorf("parse meta %s@%s: %w", name, version, err)
	}
	return &meta, nil
}
