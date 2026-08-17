package publish

import (
	"fmt"
	"os"
	"path/filepath"
)

// Manifest locates one package's manifest inside a catalog directory,
// identified by its containing directory name.
type Manifest struct {
	Pkg, Path string
}

// Manifests lists a catalog directory's package manifests — one
// pkgs/<name>/pkg.yaml per subdirectory, existence unchecked, so callers
// decide how a missing file is reported. A missing pkgs directory is
// detectable with errors.Is(err, fs.ErrNotExist).
func Manifests(dir string) ([]Manifest, error) {
	pkgsDir := filepath.Join(dir, "pkgs")
	entries, err := os.ReadDir(pkgsDir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", pkgsDir, err)
	}
	var out []Manifest
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		out = append(out, Manifest{Pkg: e.Name(), Path: filepath.Join(pkgsDir, e.Name(), "pkg.yaml")})
	}
	return out, nil
}
