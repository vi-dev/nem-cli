// Package archive extracts downloaded artifacts — tar (plain or gzip-,
// bzip2-, xz-, or zstd-compressed), zip, or a compressed single file —
// into a directory. The format is sniffed from the content, never the
// file name, and every filesystem write is routed through an os.Root so
// no entry (nor symlink chain planted by earlier entries) can reach
// outside the extraction root.
package archive

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
	"strconv"
	"strings"

	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz"
)

// Options controls extraction.
type Options struct {
	// Strip drops this many leading path components from every archive
	// entry; entries fully consumed by stripping are skipped. It has no
	// effect on a compressed single file.
	Strip int
	// SingleName names the output file (relative to the extraction root)
	// when the artifact decompresses to a single file rather than an
	// archive; when empty, such artifacts are rejected.
	SingleName string
}

// Result reports what extraction found.
type Result struct {
	// CommonPrefix is the leading path segment (before stripping) shared
	// by every archive entry — the usual "name-version/" top directory of
	// a source release. It is "" when entries have several distinct
	// leading segments, the archive is empty, or the artifact was a
	// compressed single file.
	CommonPrefix string
}

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

// tarMagicOffset is where the ustar magic sits in a tar header.
const tarMagicOffset = 257

// tarHeaderLen is one tar header block, the deepest sniff any format
// needs; it's well within bufio.Reader's default 4096-byte buffer.
const tarHeaderLen = 512

// Extract sniffs artifactPath's format by magic bytes and streams it into
// root. The artifact is opened once and never fully buffered: sniffing
// peeks the leading bytes through a bufio.Reader, and every format decodes
// straight from that same reader (or, for zip, seeks the underlying file
// directly). Compressed streams are sniffed a second time after
// decompression to tell a tar archive from a compressed single file.
func Extract(artifactPath string, root *os.Root, opts Options) (Result, error) {
	f, err := os.Open(artifactPath)
	if err != nil {
		return Result{}, fmt.Errorf("open artifact: %w", err)
	}
	defer f.Close()

	br := bufio.NewReader(f)
	peek, err := br.Peek(tarHeaderLen)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, bufio.ErrBufferFull) {
		return Result{}, fmt.Errorf("read artifact: %w", err)
	}

	switch {
	case bytes.HasPrefix(peek, gzipMagic):
		gr, err := gzip.NewReader(br)
		if err != nil {
			return Result{}, fmt.Errorf("open gzip stream: %w", err)
		}
		defer gr.Close()
		return extractDecompressed(gr, root, opts)
	case bytes.HasPrefix(peek, xzMagic):
		xr, err := xz.NewReader(br)
		if err != nil {
			return Result{}, fmt.Errorf("open xz stream: %w", err)
		}
		return extractDecompressed(xr, root, opts)
	case bytes.HasPrefix(peek, zstdMagic):
		zr, err := zstd.NewReader(br)
		if err != nil {
			return Result{}, fmt.Errorf("open zstd stream: %w", err)
		}
		defer zr.Close()
		return extractDecompressed(zr, root, opts)
	case bytes.HasPrefix(peek, bzip2Magic):
		return extractDecompressed(bzip2.NewReader(br), root, opts)
	case isZip(peek):
		info, err := f.Stat()
		if err != nil {
			return Result{}, fmt.Errorf("stat artifact: %w", err)
		}
		zr, err := zip.NewReader(f, info.Size())
		if err != nil {
			return Result{}, fmt.Errorf("open zip archive: %w", err)
		}
		return extractZip(zr, root, opts)
	case looksLikeTar(peek):
		return extractTar(tar.NewReader(br), root, opts)
	default:
		return Result{}, errors.New("unrecognized archive format")
	}
}

// extractDecompressed peeks the first header block of an already
// decompressed stream to decide whether it carries a tar archive or a
// lone file.
func extractDecompressed(r io.Reader, root *os.Root, opts Options) (Result, error) {
	br := bufio.NewReaderSize(r, tarHeaderLen)
	peek, err := br.Peek(tarHeaderLen)
	if err != nil && !errors.Is(err, io.EOF) {
		return Result{}, fmt.Errorf("read decompressed stream: %w", err)
	}
	if looksLikeTar(peek) {
		return extractTar(tar.NewReader(br), root, opts)
	}
	return extractSingleFile(br, root, opts.SingleName)
}

func extractSingleFile(r io.Reader, root *os.Root, name string) (Result, error) {
	if name == "" {
		return Result{}, errors.New("artifact decompresses to a single file, and no output name is available")
	}
	rel := filepath.FromSlash(name)
	if !filepath.IsLocal(rel) {
		return Result{}, fmt.Errorf("single-file name %q escapes extraction root", name)
	}
	if err := mkdirParent(root, rel); err != nil {
		return Result{}, err
	}
	// A compressed single artifact is almost always a bare executable;
	// gzip and friends carry no mode to restore, so default to 0755.
	if err := writeFile(root, rel, r, 0o755); err != nil {
		return Result{}, err
	}
	return Result{}, nil
}

func isZip(data []byte) bool {
	return bytes.HasPrefix(data, zipLocalFileMagic) ||
		bytes.HasPrefix(data, zipEmptyMagic) ||
		bytes.HasPrefix(data, zipSpannedMagic)
}

// looksLikeTar reports whether block starts with a plausible tar header:
// the POSIX "ustar" magic, or — for pre-POSIX (V7) tars, which carry no
// magic at all — a header whose checksum field matches the header bytes.
func looksLikeTar(block []byte) bool {
	if len(block) < tarHeaderLen {
		return false
	}
	if string(block[tarMagicOffset:tarMagicOffset+5]) == "ustar" {
		return true
	}
	return validTarChecksum(block)
}

// validTarChecksum verifies block's tar header checksum: the octal value
// in bytes 148–155 must equal the sum of all 512 header bytes with the
// checksum field itself counted as spaces. Both unsigned and signed sums
// are accepted, as historical tar implementations wrote either.
func validTarChecksum(block []byte) bool {
	field := strings.Trim(string(block[148:156]), " \x00")
	want, err := strconv.ParseInt(field, 8, 64)
	if err != nil || want <= 0 {
		return false
	}
	var unsigned, signed int64
	for i, b := range block[:tarHeaderLen] {
		if i >= 148 && i < 156 {
			b = ' '
		}
		unsigned += int64(b)
		signed += int64(int8(b))
	}
	return want == unsigned || want == signed
}
