package project

import (
	"errors"
	"os"
	"path/filepath"
)

// ErrNoManifest reports that no nem.toml exists here or in any parent.
var ErrNoManifest = errors.New("no nem.toml found")

// Discover walks up from startDir to the filesystem root looking for
// nem.toml, returning the directory that holds it.
func Discover(startDir string) (string, error) {
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "nem.toml")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", ErrNoManifest
		}
		dir = parent
	}
}
