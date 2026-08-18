package project

import (
	"bytes"
	"fmt"
	"os"
	"sort"

	"github.com/pelletier/go-toml/v2"

	"github.com/vi-dev/nem-cli/internal/fsx"
)

const lockHeader = "# machine-written by nem — do not edit\n"

type LockEntry struct {
	Name      string   `toml:"name"`
	Version   string   `toml:"version"`
	Catalog   string   `toml:"catalog"`
	Direct    bool     `toml:"direct"`
	Platforms []string `toml:"platforms"`
	Digest    string   `toml:"digest,omitempty"`

	OnPath       bool `toml:"on_path"`
	OnLoaderPath bool `toml:"on_loader_path,omitempty"`
}

type Lockfile struct {
	Path     string
	Packages []LockEntry
}

type rawLock struct {
	Version  int         `toml:"version"`
	Packages []LockEntry `toml:"package"`
}

// LoadLock reads a nem.lock. A missing file is an empty lockfile.
func LoadLock(path string) (*Lockfile, error) {
	lf := &Lockfile{Path: path}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return lf, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var raw rawLock
	d := toml.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&raw); err != nil {
		return nil, wrapTOMLError(path, err)
	}
	if raw.Version != 1 && raw.Version != 2 {
		return nil, fmt.Errorf("parse %s: unsupported lock version %d", path, raw.Version)
	}
	if raw.Version == 1 {
		for i := range raw.Packages {
			raw.Packages[i].OnPath = true
		}
	}
	lf.Packages = raw.Packages
	return lf, nil
}

// Covers reports whether a direct lock entry accounts for tool exactly:
// same name and version, and — when tool names a catalog — the same
// catalog (an unqualified tool matches any catalog, mirroring
// resolution's search across all of them). Dependency-only entries never
// cover a direct declaration, since unusing their parent would drop them.
func (lf *Lockfile) Covers(tool ToolEntry) bool {
	for _, e := range lf.Packages {
		if e.Direct && e.Name == tool.Key.Name && e.Version == tool.Version &&
			(tool.Key.Catalog == "" || tool.Key.Catalog == e.Catalog) {
			return true
		}
	}
	return false
}

// WriteLock writes the lockfile sorted and atomically; no-op when unchanged.
func WriteLock(lf *Lockfile) error {
	pkgs := append([]LockEntry(nil), lf.Packages...)
	sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].Name < pkgs[j].Name })
	body, err := toml.Marshal(rawLock{Version: 2, Packages: pkgs})
	if err != nil {
		return fmt.Errorf("render %s: %w", lf.Path, err)
	}
	rendered := append([]byte(lockHeader), body...)
	if existing, err := os.ReadFile(lf.Path); err == nil && bytes.Equal(existing, rendered) {
		return nil
	}
	return fsx.WriteAtomic(lf.Path, rendered, 0o644)
}
