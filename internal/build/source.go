package build

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/vi-dev/nem-cli/internal/archive"
	"github.com/vi-dev/nem-cli/internal/fetch"
	"github.com/vi-dev/nem-cli/internal/spec"
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
	path, sum, err := fetch.DownloadUnverified(ctx, client, url, dir, meta, nil)
	if err != nil {
		return "", "", false, err
	}
	return path, sum, false, nil
}

// unpackSource extracts archivePath — tar (plain or gzip-, bzip2-, xz-, or
// zstd-compressed), zip, or a compressed single file (landing as
// singleName) — into destDir via the archive package, which confines every
// write to destDir through an os.Root. Every entry is written at its full
// archive path (no path components are dropped), so when the archive holds
// one common leading directory (the usual "name-version/" shape of a
// source release), that directory ends up directly under destDir, and root
// reports its path. When entries don't share one leading segment — or the
// source was a single compressed file — root is destDir itself.
func unpackSource(archivePath, destDir, singleName string) (root string, err error) {
	dirRoot, err := os.OpenRoot(destDir)
	if err != nil {
		return "", fmt.Errorf("open destination dir %s: %w", destDir, err)
	}
	defer dirRoot.Close()

	res, err := archive.Extract(archivePath, dirRoot, archive.Options{SingleName: singleName})
	if err != nil {
		return "", err
	}
	if res.CommonPrefix != "" {
		return filepath.Join(destDir, res.CommonPrefix), nil
	}
	return destDir, nil
}

// sourceSingleName derives the name a compressed single-file source
// extracts to from the package's source URL; "" when unresolvable, which
// rejects such sources rather than inventing a name.
func sourceSingleName(pkg *spec.Package, version string) string {
	url, err := pkg.BuildSourceURL(version, spec.Current())
	if err != nil {
		return ""
	}
	return archive.SingleNameFromRef(url)
}
