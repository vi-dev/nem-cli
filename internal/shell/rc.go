package shell

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/vi-dev/nem-cli/internal/fsx"
)

const rcFileMode = 0o644

// InstallBlock inserts nem's managed block (see HookBlock) into rcPath,
// wrapped in BeginMarker/EndMarker. A file that already carries a marked
// block has that block replaced in place; otherwise the block is
// appended. Either way the result is idempotent: installing twice in a
// row leaves exactly one block in the file. A missing rcPath is treated
// as an empty file, so the first call creates it at the default mode;
// an existing file keeps its own permissions. The write goes through
// fsx.WriteAtomic so a reader never observes a partial rewrite.
func InstallBlock(rcPath string, d Dialect) error {
	existing, mode, err := readRCFile(rcPath)
	if err != nil {
		return err
	}

	prefix, suffix, _ := splitAroundBlock(existing)
	updated := prefix + blockFor(prefix, HookBlock(d)) + suffix

	if err := fsx.WriteAtomic(rcPath, []byte(updated), mode); err != nil {
		return fmt.Errorf("write %s: %w", rcPath, err)
	}
	return nil
}

// RemoveBlock deletes nem's managed block, markers included, from
// rcPath, leaving the rest of the file — and its permissions —
// untouched. A file with no marked block, including one that doesn't
// exist at all, is left exactly as is; this is a no-op, not an error.
func RemoveBlock(rcPath string) error {
	existing, mode, err := readRCFile(rcPath)
	if err != nil {
		return err
	}

	prefix, suffix, ok := splitAroundBlock(existing)
	if !ok {
		return nil
	}

	if err := fsx.WriteAtomic(rcPath, []byte(prefix+suffix), mode); err != nil {
		return fmt.Errorf("write %s: %w", rcPath, err)
	}
	return nil
}

// readRCFile reads rcPath's content and permission bits from a single
// open handle, so the mode reported always matches the content read. A
// missing file reads as empty content at the default rc file mode, so
// InstallBlock can create one from scratch.
func readRCFile(rcPath string) (content string, mode os.FileMode, err error) {
	f, err := os.Open(rcPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", rcFileMode, nil
		}
		return "", 0, fmt.Errorf("open %s: %w", rcPath, err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return "", 0, fmt.Errorf("stat %s: %w", rcPath, err)
	}

	data, err := io.ReadAll(f)
	if err != nil {
		return "", 0, fmt.Errorf("read %s: %w", rcPath, err)
	}

	return string(data), info.Mode().Perm(), nil
}

// blockFor renders body wrapped in BeginMarker/EndMarker, preceded by a
// newline whenever prefix — the content that will immediately precede
// the block — is non-empty. That newline terminates prefix's last line
// when prefix doesn't already end in one, or opens a blank-line
// separator when it does; either way exactly one newline is added, so
// findBlock's single-newline strip on removal is always undoing
// precisely this line, never eating into prefix's own content. An empty
// prefix (a brand new file) gets the block with no leading newline at
// all, so installing into an empty file never leaves a blank first
// line.
func blockFor(prefix, body string) string {
	sep := ""
	if prefix != "" {
		sep = "\n"
	}
	return sep + BeginMarker + "\n" + body + "\n" + EndMarker + "\n"
}

// splitAroundBlock returns the content immediately before and after
// nem's marked block in s, with the block, its markers, and the
// newline blockFor added ahead of it all excluded, and reports whether
// a block was found. When no block is found, prefix is s unchanged and
// suffix is empty, so InstallBlock's append path and its
// already-installed replace path share the same construction.
func splitAroundBlock(s string) (prefix, suffix string, ok bool) {
	start, end, ok := findBlock(s)
	if !ok {
		return s, "", false
	}
	return s[:start], s[end:], true
}

// findBlock locates the marked block within s by its exact marker
// lines, returning the byte span blockFor's insertion added: EndMarker's
// own trailing newline (deleting its whole line), and, when present,
// the single newline immediately before BeginMarker that blockFor
// prepends for any non-empty prefix. Stripping that newline unconditionally
// is exact — not merely a blank-line heuristic — because blockFor never
// adds more than one, so there is always exactly one to undo, and
// prefix's own trailing newline (if it had one) sits before that point,
// untouched.
func findBlock(s string) (start, end int, ok bool) {
	beginIdx := strings.Index(s, BeginMarker)
	if beginIdx < 0 {
		return 0, 0, false
	}
	afterBegin := beginIdx + len(BeginMarker)
	rel := strings.Index(s[afterBegin:], EndMarker)
	if rel < 0 {
		return 0, 0, false
	}
	endIdx := afterBegin + rel + len(EndMarker)

	start = beginIdx
	if beginIdx > 0 && s[beginIdx-1] == '\n' {
		start--
	}

	end = endIdx
	if end < len(s) && s[end] == '\n' {
		end++
	}

	return start, end, true
}
