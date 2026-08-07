// Package home derives every path under $NEM_HOME in one place.
package home

import (
	"os"
	"path/filepath"
)

// Home is nem's state directory. Resolve it once at the edge (cmd) and pass
// it down as a value.
type Home struct{ root string }

// Resolve reads NEM_HOME via getenv, defaulting to ~/.nem.
func Resolve(getenv func(string) string) Home {
	if root := getenv("NEM_HOME"); root != "" {
		return Home{root: root}
	}
	dir, err := os.UserHomeDir()
	if err != nil {
		dir = "."
	}
	return Home{root: filepath.Join(dir, ".nem")}
}

func (h Home) Root() string           { return h.root }
func (h Home) Config() string         { return filepath.Join(h.root, "config.yaml") }
func (h Home) GlobalManifest() string { return filepath.Join(h.root, "nem.toml") }
func (h Home) GlobalLock() string     { return filepath.Join(h.root, "nem.lock") }
func (h Home) LockFile() string       { return filepath.Join(h.root, "lock") }
func (h Home) Tmp() string            { return filepath.Join(h.root, "tmp") }

func (h Home) PackageDir(name, version string) string {
	return filepath.Join(h.root, "packages", name, version)
}

func (h Home) CatalogStore(name string) string {
	return filepath.Join(h.root, "catalogs", name, "store")
}
