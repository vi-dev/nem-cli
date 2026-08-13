package build

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// tarGzDir writes the contents of dir as a gzip'd tar to w. Each entry's name
// is its path relative to dir. Regular files (mode preserved), directories,
// and in-tree symlinks are emitted; any other file type is an error. No
// hardlinks are produced, so the archive extracts identically anywhere.
// Timestamps, uid/gid, and owner names are zeroed in each header, so the
// output bytes are a pure function of the tree's contents, structure, and
// modes — unchanged source rebuilds to an identical archive.
func tarGzDir(w io.Writer, dir string) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)

	walkErr := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		link := ""
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			if link, err = os.Readlink(path); err != nil {
				return err
			}
		case d.IsDir(), info.Mode().IsRegular():
		default:
			return fmt.Errorf("unsupported file type for %s", rel)
		}
		hdr, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(rel)
		if d.IsDir() {
			hdr.Name += "/"
		}
		hdr.ModTime = time.Time{}
		hdr.AccessTime = time.Time{}
		hdr.ChangeTime = time.Time{}
		hdr.Uid, hdr.Gid = 0, 0
		hdr.Uname, hdr.Gname = "", ""
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			_, cErr := io.Copy(tw, f)
			f.Close()
			if cErr != nil {
				return cErr
			}
		}
		return nil
	})
	if walkErr != nil {
		return walkErr
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}
