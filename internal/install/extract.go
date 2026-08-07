package install

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/bzip2"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz"
)

var (
	gzipMagic  = []byte{0x1f, 0x8b}
	xzMagic    = []byte{0xfd, 0x37, 0x7a, 0x58, 0x5a, 0x00}
	zstdMagic  = []byte{0x28, 0xb5, 0x2f, 0xfd}
	bzip2Magic = []byte{0x42, 0x5a, 0x68}

	// Zip is sniffed by its specific 4-byte record signatures rather than
	// the bare "PK" prefix: a plain, uncompressed tar whose first member
	// name happens to start with "PK" (e.g. "PKGBUILD") would otherwise
	// mis-sniff as a zip archive, since a tar's first bytes are its first
	// header's name field.
	zipLocalFileMagic = []byte{0x50, 0x4b, 0x03, 0x04}
	zipEmptyMagic     = []byte{0x50, 0x4b, 0x05, 0x06}
	zipSpannedMagic   = []byte{0x50, 0x4b, 0x07, 0x08}
)

// tarMagicOffset is where the ustar magic sits in a tar header, used to
// recognize an uncompressed tar stream.
const tarMagicOffset = 257

// sniffLen covers the ustar magic at tarMagicOffset, the deepest sniff any
// format needs; it's well within bufio.Reader's default 4096-byte buffer.
const sniffLen = tarMagicOffset + len("ustar")

// extract sniffs artifactPath's format by magic bytes and streams it into
// root, dropping strip leading path components from every entry. The
// artifact is opened once and never fully buffered: sniffing peeks the
// leading bytes through a bufio.Reader, and every format decodes straight
// from that same reader (or, for zip, seeks the underlying file directly).
func extract(artifactPath string, root *os.Root, strip int) error {
	f, err := os.Open(artifactPath)
	if err != nil {
		return fmt.Errorf("open artifact: %w", err)
	}
	defer f.Close()

	br := bufio.NewReader(f)
	peek, err := br.Peek(sniffLen)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, bufio.ErrBufferFull) {
		return fmt.Errorf("read artifact: %w", err)
	}

	switch {
	case bytes.HasPrefix(peek, gzipMagic):
		gr, err := gzip.NewReader(br)
		if err != nil {
			return fmt.Errorf("open gzip stream: %w", err)
		}
		defer gr.Close()
		return extractTar(tar.NewReader(gr), root, strip)
	case bytes.HasPrefix(peek, xzMagic):
		xr, err := xz.NewReader(br)
		if err != nil {
			return fmt.Errorf("open xz stream: %w", err)
		}
		return extractTar(tar.NewReader(xr), root, strip)
	case bytes.HasPrefix(peek, zstdMagic):
		zr, err := zstd.NewReader(br)
		if err != nil {
			return fmt.Errorf("open zstd stream: %w", err)
		}
		defer zr.Close()
		return extractTar(tar.NewReader(zr), root, strip)
	case bytes.HasPrefix(peek, bzip2Magic):
		return extractTar(tar.NewReader(bzip2.NewReader(br)), root, strip)
	case isZip(peek):
		info, err := f.Stat()
		if err != nil {
			return fmt.Errorf("stat artifact: %w", err)
		}
		zr, err := zip.NewReader(f, info.Size())
		if err != nil {
			return fmt.Errorf("open zip archive: %w", err)
		}
		return extractZip(zr, root, strip)
	case isPlainTar(peek):
		return extractTar(tar.NewReader(br), root, strip)
	default:
		return errors.New("unrecognized archive format")
	}
}

func isZip(data []byte) bool {
	return bytes.HasPrefix(data, zipLocalFileMagic) ||
		bytes.HasPrefix(data, zipEmptyMagic) ||
		bytes.HasPrefix(data, zipSpannedMagic)
}

func isPlainTar(data []byte) bool {
	const magic = "ustar"
	end := tarMagicOffset + len(magic)
	return len(data) >= end && string(data[tarMagicOffset:end]) == magic
}

// extractTar walks a tar stream, writing dirs, files, and symlinks into
// root under strip and containment rules. Hardlinks are rejected. Every
// write goes through root so an entry that lexically looks contained but
// really resolves outside (via a symlink planted by an earlier entry) is
// still refused by the OS-level root check, not just the lexical one.
func extractTar(tr *tar.Reader, root *os.Root, strip int) error {
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read tar entry: %w", err)
		}
		// PAX extended headers apply to the next entry and are not
		// themselves filesystem objects.
		if hdr.Typeflag == tar.TypeXHeader || hdr.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		relPath, ok := stripEntryPath(hdr.Name, strip)
		if !ok {
			continue
		}
		if !filepath.IsLocal(relPath) {
			return fmt.Errorf("entry %q escapes staging dir", hdr.Name)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := root.MkdirAll(relPath, 0o755); err != nil {
				return fmt.Errorf("create dir %s: %w", relPath, err)
			}
		case tar.TypeReg:
			if err := mkdirParent(root, relPath); err != nil {
				return err
			}
			if err := writeTarFile(root, relPath, tr, os.FileMode(hdr.Mode)&0o777); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := checkSymlinkContainment(relPath, hdr.Linkname); err != nil {
				return err
			}
			if err := mkdirParent(root, relPath); err != nil {
				return err
			}
			if err := root.Symlink(hdr.Linkname, relPath); err != nil {
				return fmt.Errorf("create symlink %s: %w", relPath, err)
			}
		case tar.TypeLink:
			return errors.New("hardlinks are not supported")
		default:
			return fmt.Errorf("entry %q: unsupported tar type %v", hdr.Name, hdr.Typeflag)
		}
	}
}

// extractZip walks a zip archive the same way extractTar walks a tar
// stream. Zip has no hardlink concept, so there is nothing to reject there.
func extractZip(zr *zip.Reader, root *os.Root, strip int) error {
	for _, f := range zr.File {
		relPath, ok := stripEntryPath(f.Name, strip)
		if !ok {
			continue
		}
		if !filepath.IsLocal(relPath) {
			return fmt.Errorf("entry %q escapes staging dir", f.Name)
		}
		mode := f.Mode()

		switch {
		case mode&os.ModeSymlink != 0:
			target, err := readZipSymlinkTarget(f)
			if err != nil {
				return err
			}
			if err := checkSymlinkContainment(relPath, target); err != nil {
				return err
			}
			if err := mkdirParent(root, relPath); err != nil {
				return err
			}
			if err := root.Symlink(target, relPath); err != nil {
				return fmt.Errorf("create symlink %s: %w", relPath, err)
			}
		case mode.IsDir():
			if err := root.MkdirAll(relPath, 0o755); err != nil {
				return fmt.Errorf("create dir %s: %w", relPath, err)
			}
		default:
			if err := mkdirParent(root, relPath); err != nil {
				return err
			}
			if err := writeZipFile(root, relPath, f, mode.Perm()); err != nil {
				return err
			}
		}
	}
	return nil
}

// mkdirParent ensures relPath's parent directory exists under root.
func mkdirParent(root *os.Root, relPath string) error {
	dir := filepath.Dir(relPath)
	if dir == "." {
		return nil
	}
	if err := root.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create dir %s: %w", dir, err)
	}
	return nil
}

// stripEntryPath drops the leading strip path components from name (a
// tar/zip entry path, always "/"-separated) and reports whether anything
// is left to extract. A negative strip (never validated upstream) is
// treated as zero rather than panicking on the slice below.
func stripEntryPath(name string, strip int) (string, bool) {
	if strip < 0 {
		strip = 0
	}
	var parts []string
	for _, p := range strings.Split(name, "/") {
		if p != "" {
			parts = append(parts, p)
		}
	}
	if strip >= len(parts) {
		return "", false
	}
	rel := strings.Join(parts[strip:], "/")
	return filepath.FromSlash(rel), true
}

// checkSymlinkContainment rejects an absolute target outright, and
// otherwise resolves target against relEntryPath's directory (itself
// already verified local to staging) to confirm the link stays inside
// staging too. This is a fast-fail on the obvious cases only: it can't
// see that relEntryPath's own directory might really be somewhere else
// entirely because an earlier entry aliased it via a symlink — that's
// what routing the actual write through os.Root is for.
func checkSymlinkContainment(relEntryPath, target string) error {
	if filepath.IsAbs(target) {
		return fmt.Errorf("symlink %s: target %q is absolute", relEntryPath, target)
	}
	resolved := filepath.Join(filepath.Dir(relEntryPath), target)
	if !filepath.IsLocal(resolved) {
		return fmt.Errorf("symlink %s: target %q escapes staging dir", relEntryPath, target)
	}
	return nil
}

func writeTarFile(root *os.Root, relPath string, r io.Reader, mode os.FileMode) error {
	f, err := root.OpenFile(relPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("create file %s: %w", relPath, err)
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return fmt.Errorf("write file %s: %w", relPath, err)
	}
	return f.Close()
}

func readZipSymlinkTarget(f *zip.File) (string, error) {
	rc, err := f.Open()
	if err != nil {
		return "", fmt.Errorf("open symlink entry %s: %w", f.Name, err)
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		return "", fmt.Errorf("read symlink entry %s: %w", f.Name, err)
	}
	return string(data), nil
}

func writeZipFile(root *os.Root, relPath string, f *zip.File, mode os.FileMode) error {
	rc, err := f.Open()
	if err != nil {
		return fmt.Errorf("open zip entry %s: %w", f.Name, err)
	}
	defer rc.Close()
	out, err := root.OpenFile(relPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode&0o777)
	if err != nil {
		return fmt.Errorf("create file %s: %w", relPath, err)
	}
	if _, err := io.Copy(out, rc); err != nil {
		out.Close()
		return fmt.Errorf("write file %s: %w", relPath, err)
	}
	return out.Close()
}
