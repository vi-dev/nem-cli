package archive

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// extractTar walks a tar stream, writing dirs, files, and symlinks into
// root under strip and containment rules. Hardlinks are rejected. Every
// write goes through root so an entry that lexically looks contained but
// really resolves outside (via a symlink planted by an earlier entry) is
// still refused by the OS-level root check, not just the lexical one.
func extractTar(tr *tar.Reader, root *os.Root, opts Options) (Result, error) {
	var prefix prefixTracker
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return Result{CommonPrefix: prefix.value()}, nil
		}
		if err != nil {
			return Result{}, fmt.Errorf("read tar entry: %w", err)
		}
		// PAX extended headers apply to the next entry and are not
		// themselves filesystem objects.
		if hdr.Typeflag == tar.TypeXHeader || hdr.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		parts, relPath, ok := splitEntryPath(hdr.Name, opts.Strip)
		if len(parts) > 0 {
			prefix.add(parts[0])
		}
		if !ok {
			continue
		}
		if !filepath.IsLocal(relPath) {
			return Result{}, fmt.Errorf("entry %q escapes extraction root", hdr.Name)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := root.MkdirAll(relPath, 0o755); err != nil {
				return Result{}, fmt.Errorf("create dir %s: %w", relPath, err)
			}
		case tar.TypeReg:
			if err := mkdirParent(root, relPath); err != nil {
				return Result{}, err
			}
			if err := writeFile(root, relPath, tr, os.FileMode(hdr.Mode)&0o777); err != nil {
				return Result{}, err
			}
		case tar.TypeSymlink:
			if err := checkSymlinkContainment(relPath, hdr.Linkname); err != nil {
				return Result{}, err
			}
			if err := mkdirParent(root, relPath); err != nil {
				return Result{}, err
			}
			if err := root.Symlink(hdr.Linkname, relPath); err != nil {
				return Result{}, fmt.Errorf("create symlink %s: %w", relPath, err)
			}
		case tar.TypeLink:
			return Result{}, errors.New("hardlinks are not supported")
		default:
			return Result{}, fmt.Errorf("entry %q: unsupported tar type %v", hdr.Name, hdr.Typeflag)
		}
	}
}

// splitEntryPath splits a tar/zip entry's "/"-separated name into its
// non-empty path components and the OS-native relative path left after
// dropping strip leading components. ok is false when nothing remains to
// extract; parts is returned regardless, for common-prefix tracking. A
// negative strip (never validated upstream) is treated as zero rather
// than panicking on the slice below.
func splitEntryPath(name string, strip int) (parts []string, relPath string, ok bool) {
	if strip < 0 {
		strip = 0
	}
	for _, p := range strings.Split(name, "/") {
		if p != "" {
			parts = append(parts, p)
		}
	}
	if strip >= len(parts) {
		return parts, "", false
	}
	return parts, filepath.FromSlash(strings.Join(parts[strip:], "/")), true
}

// prefixTracker watches every entry's leading path segment and yields the
// one segment they all share, if any.
type prefixTracker struct {
	first    string
	saw      bool
	multiple bool
}

func (p *prefixTracker) add(seg string) {
	switch {
	case !p.saw:
		p.first, p.saw = seg, true
	case seg != p.first:
		p.multiple = true
	}
}

func (p *prefixTracker) value() string {
	if p.saw && !p.multiple {
		return p.first
	}
	return ""
}
