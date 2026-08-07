// Package home derives every path under $NEM_HOME in one place.
package home

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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

// segmentRE matches a single path segment safe to join under root: no
// separator, and no leading dot (so ".." and hidden-file tricks are rejected
// too, since a hidden segment is never a name nem itself generates).
var segmentRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]*$`)

// safeSegment rejects a path segment that could escape its parent directory
// or otherwise fall outside the names nem itself generates.
func safeSegment(s string) error {
	if !segmentRE.MatchString(s) {
		return fmt.Errorf("invalid path segment %q", s)
	}
	return nil
}

// PackageDir returns the install directory for name at version, rejecting
// either segment if it could escape $NEM_HOME/packages.
func (h Home) PackageDir(name, version string) (string, error) {
	if err := safeSegment(name); err != nil {
		return "", err
	}
	if err := safeSegment(version); err != nil {
		return "", err
	}
	return filepath.Join(h.root, "packages", name, version), nil
}

// CatalogStore returns the local OCI-layout mirror path for the named
// catalog, rejecting a name that could escape $NEM_HOME/catalogs.
func (h Home) CatalogStore(name string) (string, error) {
	dir, err := h.CatalogDir(name)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "store"), nil
}

// CatalogDir returns $NEM_HOME/catalogs/<name>, rejecting a name that could
// escape it.
func (h Home) CatalogDir(name string) (string, error) {
	if err := safeSegment(name); err != nil {
		return "", err
	}
	return filepath.Join(h.root, "catalogs", name), nil
}
