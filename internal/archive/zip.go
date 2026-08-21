package archive

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// extractZip walks a zip archive the same way extractTar walks a tar
// stream. Zip has no hardlink concept, so there is nothing to reject there.
func extractZip(zr *zip.Reader, root *os.Root, opts Options) (Result, error) {
	var prefix prefixTracker
	for _, f := range zr.File {
		parts, relPath, ok := splitEntryPath(f.Name, opts.Strip)
		if len(parts) > 0 {
			prefix.add(parts[0])
		}
		if !ok {
			continue
		}
		if !filepath.IsLocal(relPath) {
			return Result{}, fmt.Errorf("entry %q escapes extraction root", f.Name)
		}
		mode := f.Mode()

		switch {
		case mode&os.ModeSymlink != 0:
			target, err := readZipSymlinkTarget(f)
			if err != nil {
				return Result{}, err
			}
			if err := checkSymlinkContainment(relPath, target); err != nil {
				return Result{}, err
			}
			if err := mkdirParent(root, relPath); err != nil {
				return Result{}, err
			}
			if err := root.Symlink(target, relPath); err != nil {
				return Result{}, fmt.Errorf("create symlink %s: %w", relPath, err)
			}
		case mode.IsDir():
			if err := root.MkdirAll(relPath, 0o755); err != nil {
				return Result{}, fmt.Errorf("create dir %s: %w", relPath, err)
			}
		default:
			if err := mkdirParent(root, relPath); err != nil {
				return Result{}, err
			}
			if err := writeZipFile(root, relPath, f, mode.Perm()); err != nil {
				return Result{}, err
			}
		}
	}
	return Result{CommonPrefix: prefix.value()}, nil
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
	if err := writeFile(root, relPath, rc, mode&0o777); err != nil {
		return err
	}
	return nil
}
