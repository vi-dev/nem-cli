package clean

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vi-dev/nem-cli/internal/home"
)

// buildStagingGlob matches the name internal/build gives a staging
// directory under tmp/: "<pkg>"+home.BuildStagingInfix+"<random>".
const buildStagingGlob = "*" + home.BuildStagingInfix + "*"

// Scan gathers everything reclaimable under $NEM_HOME. A directory that is
// absent is not an error: a fresh install has no tmp/ and no packages/.
// includeVersions controls whether an installed version's tree is stat'd
// and reported in Store.Versions: bare clean never consults Versions, so
// its caller passes false and skips the recursive stat entirely — a
// partial install's .tmp directory is always stat'd regardless, since
// tier 0 always needs it.
func Scan(h home.Home, includeVersions bool) (Store, error) {
	var s Store

	tmp, err := os.ReadDir(h.Tmp())
	if err != nil && !os.IsNotExist(err) {
		return s, err
	}
	for _, e := range tmp {
		path := filepath.Join(h.Tmp(), e.Name())
		switch {
		case e.IsDir():
			// Only the shape internal/build creates. Anything else in tmp/
			// belongs to code this sweep knows nothing about.
			if ok, _ := filepath.Match(buildStagingGlob, e.Name()); !ok {
				continue
			}
			newest, size, err := treeStat(path)
			if err != nil {
				continue // vanished mid-scan, or unreadable; leave it alone
			}
			s.Staging = append(s.Staging, Item{Path: path, Newest: newest, Size: size})
		case e.Type().IsRegular() && strings.HasSuffix(e.Name(), home.TmpSuffix):
			// An artifact download whose process died before it could
			// remove its own temp file. These are whole release tarballs.
			info, err := e.Info()
			if err != nil {
				continue
			}
			s.Downloads = append(s.Downloads, Item{Path: path, Newest: info.ModTime(), Size: info.Size()})
		}
	}

	pkgRoot := h.Packages()
	names, err := os.ReadDir(pkgRoot)
	if err != nil && !os.IsNotExist(err) {
		return s, err
	}
	for _, n := range names {
		if !n.IsDir() {
			continue
		}
		versions, err := os.ReadDir(filepath.Join(pkgRoot, n.Name()))
		if err != nil {
			continue
		}
		for _, v := range versions {
			if !v.IsDir() {
				continue
			}
			path, err := h.PackageDir(n.Name(), v.Name())
			if err != nil {
				continue // a name nem never generated; not ours to delete
			}
			if strings.HasSuffix(v.Name(), home.TmpSuffix) {
				newest, size, err := treeStat(path)
				if err != nil {
					continue
				}
				s.Partials = append(s.Partials, Item{Path: path, Newest: newest, Size: size})
				continue
			}
			if !includeVersions {
				continue
			}
			_, size, err := treeStat(path)
			if err != nil {
				continue
			}
			s.Versions = append(s.Versions, Version{
				Name: n.Name(), Version: v.Name(), Path: path, Size: size,
			})
		}
	}

	return s, nil
}

// treeStat reports the newest modification time anywhere under root and the
// total size of its regular files.
func treeStat(root string) (time.Time, int64, error) {
	var newest time.Time
	var size int64
	err := filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		if d.Type().IsRegular() {
			size += info.Size()
		}
		return nil
	})
	return newest, size, err
}
