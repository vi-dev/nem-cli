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
func (h Home) Usage() string          { return filepath.Join(h.root, "usage.json") }

// Packages returns $NEM_HOME/packages, the parent of every install dir
// PackageDir names.
func (h Home) Packages() string { return filepath.Join(h.root, "packages") }

// TmpSuffix marks a path as not yet ready to use: internal/fetch names a
// download's temp file with it, and internal/install names an install's
// staging directory with it, both via os.CreateTemp/os.MkdirTemp patterns
// ending in "-*"+TmpSuffix.
const TmpSuffix = ".tmp"

// BuildStagingInfix marks nem's own scratch paths under Tmp(), so
// internal/clean can reclaim one a killed run left behind. internal/build
// names a build's staging directory with it and internal/pkgtest a test
// run's scratch directory, both via
// os.MkdirTemp(h.Tmp(), pkg.Name+BuildStagingInfix+"*"). internal/build also
// names the temp archive file it hands to a build's test hook with it, via
// os.CreateTemp(h.Tmp(), name+BuildStagingInfix+"archive-*"+TmpSuffix).
const BuildStagingInfix = "-build-"

// TestInstallInfix names the throwaway installation a package test run makes
// under packages/: os.MkdirTemp(h.Packages(), pkg.Name+TestInstallInfix+"*").
// A directory carrying it is never a long-lived install, so clean removes it
// outright rather than treating its contents as installed versions.
//
// Uppercase is deliberate: spec.NameRE restricts package names to
// lowercase, so no real package directory can ever contain this infix and
// be mistaken for an alias. Lowering it would let a legitimately named
// package (e.g. "tool-nemtest-cli") collide with the glob and be swept
// whole.
const TestInstallInfix = "-NEMTEST-"

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
	return filepath.Join(h.Packages(), name, version), nil
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
