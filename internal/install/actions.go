// Package install executes a package's install action list inside a
// staging directory: extracting the downloaded artifact and copying,
// moving, or creating files under it, all confined to the staging tree.
package install

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/vi-dev/nem-cli/internal/spec"
)

// artifactToken is the literal copy src value that refers to the verified
// downloaded artifact rather than a path inside staging.
const artifactToken = "{{.Artifact}}"

// RunActions executes pkg.Install in order inside stagingDir.
// artifactPath is the verified downloaded artifact ({{.Artifact}} in copy
// src).
//
// Every filesystem mutation goes through an os.Root rooted at stagingDir:
// a purely lexical containment check on an action's own path (or a
// symlink's own target) is not enough, because an earlier action can
// plant a symlink that makes a later, lexically-innocent-looking path
// really resolve outside staging once the OS follows it. os.Root refuses
// any traversal that would leave the root regardless of how many symlink
// hops are involved.
func RunActions(pkg *spec.Package, stagingDir, artifactPath string) error {
	root, err := os.OpenRoot(stagingDir)
	if err != nil {
		return fmt.Errorf("open staging dir: %w", err)
	}
	defer root.Close()

	for i, a := range pkg.Install {
		if err := runAction(a, root, artifactPath); err != nil {
			return fmt.Errorf("install[%d]: %w", i, err)
		}
	}
	return nil
}

func runAction(a spec.Action, root *os.Root, artifactPath string) error {
	switch {
	case a.Extract != nil:
		return extract(artifactPath, root, a.Extract.Strip)
	case a.Copy != nil:
		return runCopy(a.Copy, root, artifactPath)
	case a.Move != nil:
		return runMove(a.Move, root)
	case a.Mkdir != "":
		return runMkdir(a.Mkdir, root)
	default:
		return errors.New("empty action")
	}
}

func runCopy(a *spec.CopyAction, root *os.Root, artifactPath string) error {
	if err := checkContained(a.Dst); err != nil {
		return err
	}

	var in *os.File
	var err error
	if a.Src == artifactToken {
		in, err = os.Open(artifactPath)
	} else {
		if err := checkContained(a.Src); err != nil {
			return err
		}
		in, err = root.Open(a.Src)
	}
	if err != nil {
		return fmt.Errorf("open %s: %w", a.Src, err)
	}
	defer in.Close()

	if err := mkdirParent(root, a.Dst); err != nil {
		return err
	}
	mode := os.FileMode(a.Mode)
	if mode == 0 {
		mode = 0o644
	}
	out, err := root.OpenFile(a.Dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode.Perm())
	if err != nil {
		return fmt.Errorf("create %s: %w", a.Dst, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return fmt.Errorf("copy to %s: %w", a.Dst, err)
	}
	return out.Close()
}

func runMove(a *spec.MoveAction, root *os.Root) error {
	if err := checkContained(a.Src); err != nil {
		return err
	}
	if err := checkContained(a.Dst); err != nil {
		return err
	}
	if err := root.Rename(a.Src, a.Dst); err != nil {
		return fmt.Errorf("rename %s to %s: %w", a.Src, a.Dst, err)
	}
	return nil
}

func runMkdir(dir string, root *os.Root) error {
	if err := checkContained(dir); err != nil {
		return err
	}
	if err := root.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	return nil
}

// checkContained is a fast-fail lexical pre-check on rel, a path relative
// to staging root: an obvious escape (e.g. "../evil") is rejected before
// touching the filesystem at all. It is not the containment guarantee
// itself — every actual read, write, or rename below still goes through
// os.Root, which is what catches escapes lexical analysis alone can't see
// (see RunActions).
func checkContained(rel string) error {
	if !filepath.IsLocal(rel) {
		return fmt.Errorf("path %q escapes staging dir", rel)
	}
	return nil
}
