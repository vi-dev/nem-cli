package build

import (
	"archive/tar"
	"bufio"
	"compress/bzip2"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/vi-dev/nem-cli/internal/fetch"
)

// fetchSource downloads url into dir. When wantSHA256 is set, it delegates
// to fetch.Download, which verifies the digest as it streams and errors
// with a ChecksumMismatchError on mismatch; verified is true and sha256 is
// wantSHA256. When wantSHA256 is empty, it downloads unverified (trust on
// first use) and reports the digest it computed with verified false, for
// the caller to pin going forward.
func fetchSource(ctx context.Context, client *http.Client, url, wantSHA256, dir string, meta fetch.Meta) (path, sha256sum string, verified bool, err error) {
	if wantSHA256 != "" {
		path, err := fetch.Download(ctx, client, url, wantSHA256, dir, meta, nil)
		if err != nil {
			return "", "", false, err
		}
		return path, wantSHA256, true, nil
	}
	path, sum, err := downloadUnverified(ctx, client, url, dir, meta)
	if err != nil {
		return "", "", false, err
	}
	return path, sum, false, nil
}

// downloadUnverified streams url's body into a fresh temp file in dir,
// hashing as it goes, without comparing against any expected digest. The
// temp file is removed on any error.
func downloadUnverified(ctx context.Context, client *http.Client, url, dir string, meta fetch.Meta) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", "", fmt.Errorf("build request for %s: %w", url, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("fetch %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("fetch %s: unexpected status %s", url, resp.Status)
	}

	f, err := os.CreateTemp(dir, meta.Name+"-"+meta.Version+"-*.tmp")
	if err != nil {
		return "", "", fmt.Errorf("create temp file in %s: %w", dir, err)
	}
	tmpPath := f.Name()

	sum := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(f, sum), resp.Body)
	closeErr := f.Close()
	if copyErr != nil {
		os.Remove(tmpPath)
		return "", "", fmt.Errorf("download %s: %w", url, copyErr)
	}
	if closeErr != nil {
		os.Remove(tmpPath)
		return "", "", fmt.Errorf("close temp file %s: %w", tmpPath, closeErr)
	}

	return tmpPath, hex.EncodeToString(sum.Sum(nil)), nil
}

// unpackSource extracts archivePath, a gzip- or bzip2-compressed tar, into
// destDir. Every entry is written at its full archive path (no path
// components are dropped), so when the archive holds one common leading
// directory (the usual "name-version/" shape of a source release), that
// directory ends up directly under destDir, and root reports its path. When
// entries don't share one leading segment, root is destDir itself.
//
// Every write is routed through an os.Root rooted at destDir, so a symlink
// planted by an earlier entry can't redirect a later entry's lexically
// safe-looking path outside destDir once the OS follows it. Entries whose
// path is not filepath.IsLocal (absolute, or escaping via "..") are
// rejected, as are hardlinks; symlinks are allowed only when their target
// stays inside destDir.
func unpackSource(archivePath, destDir string) (root string, err error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return "", fmt.Errorf("open archive %s: %w", archivePath, err)
	}
	defer f.Close()

	decompressed, err := decompressStream(f)
	if err != nil {
		return "", err
	}

	dirRoot, err := os.OpenRoot(destDir)
	if err != nil {
		return "", fmt.Errorf("open destination dir %s: %w", destDir, err)
	}
	defer dirRoot.Close()

	tr := tar.NewReader(decompressed)
	var commonSeg string
	sawEntry, multipleRoots := false, false

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("read tar entry: %w", err)
		}
		// PAX extended headers apply to the next entry and are not
		// themselves filesystem objects.
		if hdr.Typeflag == tar.TypeXHeader || hdr.Typeflag == tar.TypeXGlobalHeader {
			continue
		}

		parts, relPath, ok := splitTarPath(hdr.Name)
		if !ok {
			continue
		}
		if !filepath.IsLocal(relPath) {
			return "", fmt.Errorf("entry %q escapes destination dir", hdr.Name)
		}

		if !sawEntry {
			commonSeg, sawEntry = parts[0], true
		} else if parts[0] != commonSeg {
			multipleRoots = true
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := dirRoot.MkdirAll(relPath, 0o755); err != nil {
				return "", fmt.Errorf("create dir %s: %w", relPath, err)
			}
		case tar.TypeReg:
			if err := mkdirParent(dirRoot, relPath); err != nil {
				return "", err
			}
			if err := writeRegularFile(dirRoot, relPath, tr, os.FileMode(hdr.Mode)&0o777); err != nil {
				return "", err
			}
		case tar.TypeSymlink:
			if err := checkSymlinkContainment(relPath, hdr.Linkname); err != nil {
				return "", err
			}
			if err := mkdirParent(dirRoot, relPath); err != nil {
				return "", err
			}
			if err := dirRoot.Symlink(hdr.Linkname, relPath); err != nil {
				return "", fmt.Errorf("create symlink %s: %w", relPath, err)
			}
		case tar.TypeLink:
			return "", errors.New("hardlinks are not supported")
		default:
			return "", fmt.Errorf("entry %q: unsupported tar type %v", hdr.Name, hdr.Typeflag)
		}
	}

	if sawEntry && !multipleRoots {
		return filepath.Join(destDir, commonSeg), nil
	}
	return destDir, nil
}

// decompressStream wraps r in the decompressor matching its leading magic
// bytes: gzip (1f 8b) or bzip2 ("BZh"). Source releases ship as one or the
// other; anything else is rejected rather than fed to tar as-is.
func decompressStream(r io.Reader) (io.Reader, error) {
	br := bufio.NewReader(r)
	magic, err := br.Peek(3)
	if err != nil {
		return nil, fmt.Errorf("read archive header: %w", err)
	}
	switch {
	case magic[0] == 0x1f && magic[1] == 0x8b:
		gr, err := gzip.NewReader(br)
		if err != nil {
			return nil, fmt.Errorf("open gzip stream: %w", err)
		}
		return gr, nil
	case magic[0] == 'B' && magic[1] == 'Z' && magic[2] == 'h':
		return bzip2.NewReader(br), nil
	default:
		return nil, fmt.Errorf("unsupported source compression (magic %x); want gzip or bzip2", magic)
	}
}

// splitTarPath splits a tar entry's "/"-separated name into its non-empty
// path components and the equivalent OS-native relative path. ok is false
// for a name with no components left (e.g. "/" or "").
func splitTarPath(name string) (parts []string, relPath string, ok bool) {
	for _, p := range strings.Split(name, "/") {
		if p != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) == 0 {
		return nil, "", false
	}
	return parts, filepath.FromSlash(strings.Join(parts, "/")), true
}

// checkSymlinkContainment rejects an absolute target outright, and
// otherwise resolves target against relEntryPath's directory (itself
// already verified local to destDir) to confirm the link stays inside
// destDir too. This lexical check can't see that relEntryPath's own
// directory might really be somewhere else because an earlier entry
// aliased it via a symlink — routing the actual write through os.Root
// catches that case.
func checkSymlinkContainment(relEntryPath, target string) error {
	if filepath.IsAbs(target) {
		return fmt.Errorf("symlink %s: target %q is absolute", relEntryPath, target)
	}
	resolved := filepath.Join(filepath.Dir(relEntryPath), target)
	if !filepath.IsLocal(resolved) {
		return fmt.Errorf("symlink %s: target %q escapes destination dir", relEntryPath, target)
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

func writeRegularFile(root *os.Root, relPath string, r io.Reader, mode os.FileMode) error {
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
