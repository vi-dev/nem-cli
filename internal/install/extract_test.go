package install_test

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz"

	"github.com/vi-dev/nem-cli/internal/install"
	"github.com/vi-dev/nem-cli/internal/spec"
)

type tarEntry struct {
	name       string
	typeflag   byte
	content    []byte
	linkname   string
	mode       int64
	paxRecords map[string]string
}

func buildTar(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		typeflag := e.typeflag
		if typeflag == 0 {
			typeflag = tar.TypeReg
		}
		if typeflag == tar.TypeXGlobalHeader {
			// archive/tar requires only PAXRecords set on this type.
			if err := tw.WriteHeader(&tar.Header{Typeflag: typeflag, PAXRecords: e.paxRecords}); err != nil {
				t.Fatalf("write tar header %s: %v", e.name, err)
			}
			continue
		}
		mode := e.mode
		if mode == 0 {
			mode = 0o644
		}
		hdr := &tar.Header{
			Name:       e.name,
			Typeflag:   typeflag,
			Linkname:   e.linkname,
			Mode:       mode,
			Size:       int64(len(e.content)),
			PAXRecords: e.paxRecords,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write tar header %s: %v", e.name, err)
		}
		if len(e.content) > 0 {
			if _, err := tw.Write(e.content); err != nil {
				t.Fatalf("write tar content %s: %v", e.name, err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}
	return buf.Bytes()
}

func gzipBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	if _, err := gw.Write(data); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

func xzBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	xw, err := xz.NewWriter(&buf)
	if err != nil {
		t.Fatalf("xz writer: %v", err)
	}
	if _, err := xw.Write(data); err != nil {
		t.Fatalf("xz write: %v", err)
	}
	if err := xw.Close(); err != nil {
		t.Fatalf("xz close: %v", err)
	}
	return buf.Bytes()
}

func zstdBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw, err := zstd.NewWriter(&buf)
	if err != nil {
		t.Fatalf("zstd writer: %v", err)
	}
	if _, err := zw.Write(data); err != nil {
		t.Fatalf("zstd write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zstd close: %v", err)
	}
	return buf.Bytes()
}

type zipEntry struct {
	name      string
	content   []byte
	mode      os.FileMode
	isDir     bool
	isSymlink bool
}

func buildZip(t *testing.T, entries []zipEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		name := e.name
		mode := e.mode
		switch {
		case e.isDir:
			if !strings.HasSuffix(name, "/") {
				name += "/"
			}
			if mode == 0 {
				mode = 0o755 | os.ModeDir
			}
		case e.isSymlink:
			mode = os.ModeSymlink | 0o777
		case mode == 0:
			mode = 0o644
		}
		fh := &zip.FileHeader{Name: name, Method: zip.Deflate}
		fh.SetMode(mode)
		w, err := zw.CreateHeader(fh)
		if err != nil {
			t.Fatalf("create zip entry %s: %v", name, err)
		}
		if len(e.content) > 0 {
			if _, err := w.Write(e.content); err != nil {
				t.Fatalf("write zip entry %s: %v", name, err)
			}
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip writer: %v", err)
	}
	return buf.Bytes()
}

func extractPkg(strip int) *spec.Package {
	return pkgWith(spec.Action{Extract: &spec.ExtractAction{Strip: strip}})
}

// bzip2TarFixture is `tar -cf - a | bzip2 -9`, base64-encoded, of a single
// regular file a/hello.txt containing "hi\n". compress/bzip2 has no writer,
// so this fixture was produced once with the system bzip2 and embedded here
// to exercise the bzip2 sniff-and-decode path without any test-time
// dependency beyond the standard library.
const bzip2TarFixtureB64 = `QlpoOTFBWSZTWXOm2KsAAO1/sP+4I4BQCf/iOm//c+/v/0AAQo4wAAhAAhwMQBxkaZMTQZMmE0yBkNAaA0yaGAE0BhJI1R6CI0ABoAAAMg0yAAAAHGRpkxNBkyYTTIGQ0BoDTJoYATQGCpSTIiep6myanqb1TTEyaNDTQAGh6jR6mIYh6nqacC+yplT+PzPj0qa/6bmzLGumu1pcj8sLVfsqCvsqkm4owsi8uIOuoSpSSGZaqdpQTDi3W6rmMTnSpdSgOoURfUb7cKk6xdJU0lRoU3VK1rl42HgsJ7SjvM5UmJLqUTgSqlKKdZOJZO0ltry0xWXGo8zKpZYfVqqt7mTZNtOifZwvFLFOvMmATdx1BMBNlKJ8ceZx8lhSBMKakVwdMLKrxQRGFXcyYiqilcIzaxOUDC2GZxRFAgL8orZ/IzxL4VLCdMUJyzwFikqCZNUmYVgTSrKYiTiccklJQFRAVVpyMAcYVy5RdrmnW0thMLK+DiNZa97lcRwuRmZjY7K+qdJcb+/9n3Uft92leWLH0VuhfXWBYsN5RL/No3tTRztpypjTzp1UuJadDYeheTMvFxws6UcKjaa8xp/HMmkl9NpbbS3Txs6Xk/qa0xTKnG21E8SxNRNlKJemRNhOolaWp22d3VaYi+ol5M6dVNuaCbS6exNMuJgmCxNCVZEyLy6XZYc50lT/KI6SiP+LuSKcKEg502xVgA==`

func TestRunActionsExtractTarGzStripLandsFilesModesSymlink(t *testing.T) {
	entries := []tarEntry{
		{name: "pkg-1.0/", typeflag: tar.TypeDir, mode: 0o755},
		{name: "pkg-1.0/bin/tool", content: []byte("binary"), mode: 0o755},
		{name: "pkg-1.0/README.md", content: []byte("hello"), mode: 0o644},
		{name: "pkg-1.0/bin/tool-link", typeflag: tar.TypeSymlink, linkname: "tool"},
	}
	archive := gzipBytes(t, buildTar(t, entries))

	tmp := t.TempDir()
	staging := filepath.Join(tmp, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	artifact := writeArtifact(t, tmp, archive)

	if err := install.RunActions(extractPkg(1), staging, artifact, spec.Current()); err != nil {
		t.Fatalf("RunActions: %v", err)
	}

	tool := filepath.Join(staging, "bin", "tool")
	got, err := os.ReadFile(tool)
	if err != nil {
		t.Fatalf("read tool: %v", err)
	}
	if string(got) != "binary" {
		t.Fatalf("tool content = %q, want %q", got, "binary")
	}
	info, err := os.Stat(tool)
	if err != nil {
		t.Fatalf("stat tool: %v", err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("tool mode = %v, want 0755", info.Mode().Perm())
	}

	readme, err := os.ReadFile(filepath.Join(staging, "README.md"))
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	if string(readme) != "hello" {
		t.Fatalf("README content = %q, want %q", readme, "hello")
	}

	link := filepath.Join(staging, "bin", "tool-link")
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if target != "tool" {
		t.Fatalf("symlink target = %q, want %q", target, "tool")
	}
	linkContent, err := os.ReadFile(link)
	if err != nil {
		t.Fatalf("read through symlink: %v", err)
	}
	if string(linkContent) != "binary" {
		t.Fatalf("symlink resolved content = %q, want %q", linkContent, "binary")
	}
}

func TestRunActionsExtractZip(t *testing.T) {
	entries := []zipEntry{
		{name: "file.txt", content: []byte("hi")},
		{name: "sub/other.txt", content: []byte("there"), mode: 0o755},
		{name: "link", isSymlink: true, content: []byte("file.txt")},
		{name: "emptydir", isDir: true},
	}
	archive := buildZip(t, entries)

	tmp := t.TempDir()
	staging := filepath.Join(tmp, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	artifact := writeArtifact(t, tmp, archive)

	if err := install.RunActions(extractPkg(0), staging, artifact, spec.Current()); err != nil {
		t.Fatalf("RunActions: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(staging, "file.txt"))
	if err != nil || string(got) != "hi" {
		t.Fatalf("file.txt = %q, %v, want %q", got, err, "hi")
	}
	got, err = os.ReadFile(filepath.Join(staging, "sub", "other.txt"))
	if err != nil || string(got) != "there" {
		t.Fatalf("sub/other.txt = %q, %v, want %q", got, err, "there")
	}
	target, err := os.Readlink(filepath.Join(staging, "link"))
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if target != "file.txt" {
		t.Fatalf("link target = %q, want %q", target, "file.txt")
	}
	info, err := os.Stat(filepath.Join(staging, "emptydir"))
	if err != nil || !info.IsDir() {
		t.Fatalf("emptydir stat = %v, %v, want a directory", info, err)
	}
}

func TestRunActionsExtractEscapeEntryRejected(t *testing.T) {
	archive := gzipBytes(t, buildTar(t, []tarEntry{
		{name: "../evil", content: []byte("pwned")},
	}))

	tmp := t.TempDir()
	staging := filepath.Join(tmp, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	artifact := writeArtifact(t, tmp, archive)

	err := install.RunActions(extractPkg(0), staging, artifact, spec.Current())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "escapes staging dir") {
		t.Fatalf("error = %v, want containment error", err)
	}
	mustNotExist(t, filepath.Join(tmp, "evil"))
}

func TestRunActionsExtractZipEscapeEntryRejected(t *testing.T) {
	archive := buildZip(t, []zipEntry{
		{name: "../evil", content: []byte("pwned")},
	})

	tmp := t.TempDir()
	staging := filepath.Join(tmp, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	artifact := writeArtifact(t, tmp, archive)

	err := install.RunActions(extractPkg(0), staging, artifact, spec.Current())
	if err == nil || !strings.Contains(err.Error(), "escapes staging dir") {
		t.Fatalf("error = %v, want containment error", err)
	}
	mustNotExist(t, filepath.Join(tmp, "evil"))
}

func TestRunActionsExtractAbsoluteSymlinkTargetRejected(t *testing.T) {
	archive := gzipBytes(t, buildTar(t, []tarEntry{
		{name: "link", typeflag: tar.TypeSymlink, linkname: "/etc/passwd"},
	}))

	tmp := t.TempDir()
	staging := filepath.Join(tmp, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	artifact := writeArtifact(t, tmp, archive)

	err := install.RunActions(extractPkg(0), staging, artifact, spec.Current())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("error = %v, want absolute-target error", err)
	}
	mustNotExist(t, filepath.Join(staging, "link"))
}

func TestRunActionsExtractEscapingRelativeSymlinkTargetRejected(t *testing.T) {
	archive := gzipBytes(t, buildTar(t, []tarEntry{
		{name: "a/link", typeflag: tar.TypeSymlink, linkname: "../../x"},
	}))

	tmp := t.TempDir()
	staging := filepath.Join(tmp, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	artifact := writeArtifact(t, tmp, archive)

	err := install.RunActions(extractPkg(0), staging, artifact, spec.Current())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "escapes staging dir") {
		t.Fatalf("error = %v, want containment error", err)
	}
	mustNotExist(t, filepath.Join(staging, "a", "link"))
}

func TestRunActionsExtractHardlinkRejected(t *testing.T) {
	archive := gzipBytes(t, buildTar(t, []tarEntry{
		{name: "real", content: []byte("data")},
		{name: "hard", typeflag: tar.TypeLink, linkname: "real"},
	}))

	tmp := t.TempDir()
	staging := filepath.Join(tmp, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	artifact := writeArtifact(t, tmp, archive)

	err := install.RunActions(extractPkg(0), staging, artifact, spec.Current())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "hardlinks are not supported") {
		t.Fatalf("error = %v, want hardlink error", err)
	}
}

func TestRunActionsExtractZstdRoundTrip(t *testing.T) {
	tarBytes := buildTar(t, []tarEntry{
		{name: "bin/tool", content: []byte("zstd-payload"), mode: 0o755},
	})
	archive := zstdBytes(t, tarBytes)

	tmp := t.TempDir()
	staging := filepath.Join(tmp, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	artifact := writeArtifact(t, tmp, archive)

	if err := install.RunActions(extractPkg(0), staging, artifact, spec.Current()); err != nil {
		t.Fatalf("RunActions: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(staging, "bin", "tool"))
	if err != nil || string(got) != "zstd-payload" {
		t.Fatalf("bin/tool = %q, %v, want %q", got, err, "zstd-payload")
	}
}

func TestRunActionsExtractXzRoundTrip(t *testing.T) {
	tarBytes := buildTar(t, []tarEntry{
		{name: "bin/tool", content: []byte("xz-payload"), mode: 0o755},
	})
	archive := xzBytes(t, tarBytes)

	tmp := t.TempDir()
	staging := filepath.Join(tmp, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	artifact := writeArtifact(t, tmp, archive)

	if err := install.RunActions(extractPkg(0), staging, artifact, spec.Current()); err != nil {
		t.Fatalf("RunActions: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(staging, "bin", "tool"))
	if err != nil || string(got) != "xz-payload" {
		t.Fatalf("bin/tool = %q, %v, want %q", got, err, "xz-payload")
	}
}

func TestRunActionsExtractBzip2SniffAndDecode(t *testing.T) {
	archive, err := base64.StdEncoding.DecodeString(bzip2TarFixtureB64)
	if err != nil {
		t.Fatalf("decode fixture: %v", err)
	}

	tmp := t.TempDir()
	staging := filepath.Join(tmp, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	artifact := writeArtifact(t, tmp, archive)

	if err := install.RunActions(extractPkg(0), staging, artifact, spec.Current()); err != nil {
		t.Fatalf("RunActions: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(staging, "a", "hello.txt"))
	if err != nil || string(got) != "hi\n" {
		t.Fatalf("a/hello.txt = %q, %v, want %q", got, err, "hi\n")
	}
}

func TestRunActionsExtractPlainTar(t *testing.T) {
	archive := buildTar(t, []tarEntry{
		{name: "plain.txt", content: []byte("no compression")},
	})

	tmp := t.TempDir()
	staging := filepath.Join(tmp, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	artifact := writeArtifact(t, tmp, archive)

	if err := install.RunActions(extractPkg(0), staging, artifact, spec.Current()); err != nil {
		t.Fatalf("RunActions: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(staging, "plain.txt"))
	if err != nil || string(got) != "no compression" {
		t.Fatalf("plain.txt = %q, %v, want %q", got, err, "no compression")
	}
}

func TestRunActionsExtractUnrecognizedFormatErrors(t *testing.T) {
	tmp := t.TempDir()
	staging := filepath.Join(tmp, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	artifact := writeArtifact(t, tmp, []byte("not an archive at all"))

	err := install.RunActions(extractPkg(0), staging, artifact, spec.Current())
	if err == nil || !strings.Contains(err.Error(), "unrecognized archive format") {
		t.Fatalf("error = %v, want unrecognized-format error", err)
	}
}

func TestRunActionsExtractUnsupportedTarTypeRejected(t *testing.T) {
	archive := gzipBytes(t, buildTar(t, []tarEntry{
		{name: "dev", typeflag: tar.TypeChar},
	}))

	tmp := t.TempDir()
	staging := filepath.Join(tmp, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	artifact := writeArtifact(t, tmp, archive)

	err := install.RunActions(extractPkg(0), staging, artifact, spec.Current())
	if err == nil || !strings.Contains(err.Error(), "unsupported tar type") {
		t.Fatalf("error = %v, want unsupported-type error", err)
	}
}

func TestRunActionsExtractStripDropsTopDirEntry(t *testing.T) {
	archive := gzipBytes(t, buildTar(t, []tarEntry{
		{name: "top/", typeflag: tar.TypeDir},
		{name: "top/file", content: []byte("x")},
	}))

	tmp := t.TempDir()
	staging := filepath.Join(tmp, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	artifact := writeArtifact(t, tmp, archive)

	if err := install.RunActions(extractPkg(1), staging, artifact, spec.Current()); err != nil {
		t.Fatalf("RunActions: %v", err)
	}
	entries, err := os.ReadDir(staging)
	if err != nil {
		t.Fatalf("read staging: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "file" {
		t.Fatalf("staging entries = %v, want exactly [file]", entries)
	}
}

// nestedTempDirs returns a base temp dir (t.TempDir(), private to this
// test) containing a "container" dir containing a "staging" dir, all
// created. Escapes one level above staging land in container; two levels
// above land in base — both are ours to inspect safely, unlike the shared
// system temp root.
func nestedTempDirs(t *testing.T) (base, container, staging string) {
	t.Helper()
	base = t.TempDir()
	container = filepath.Join(base, "container")
	staging = filepath.Join(container, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	return base, container, staging
}

// TestRunActionsExtractSymlinkChainEscapeRejected reproduces the proven
// containment escape: a symlink entry (x/a -> ../y) is itself safely
// contained by the lexical check, but it makes "x/a" really point at "y"
// (one level shallower than its literal name suggests). A second symlink
// (x/a/evil -> ../../pwned) is then evaluated against the *literal*
// "x/a/evil" path by the lexical check and looks safe (2 ups cancels the
// literal 2-deep prefix) — but resolved against the *real* location (one
// level shallower, thanks to the first symlink), it lands one level above
// staging. A regular-file entry reusing the same name is how the write
// actually happens: opening "x/a/evil" for writing follows both symlinks.
func TestRunActionsExtractSymlinkChainEscapeRejected(t *testing.T) {
	base, container, staging := nestedTempDirs(t)
	archive := gzipBytes(t, buildTar(t, []tarEntry{
		{name: "y", typeflag: tar.TypeDir},
		{name: "x/a", typeflag: tar.TypeSymlink, linkname: "../y"},
		{name: "x/a/evil", typeflag: tar.TypeSymlink, linkname: "../../pwned"},
		{name: "x/a/evil", content: []byte("pwned"), mode: 0o755},
	}))
	artifact := writeArtifact(t, container, archive)

	err := install.RunActions(extractPkg(0), staging, artifact, spec.Current())
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	mustNotExist(t, filepath.Join(container, "pwned"))
	mustNotExist(t, filepath.Join(base, "pwned"))
}

// TestRunActionsExtractZipSymlinkChainEscapeRejected is the zip
// counterpart of TestRunActionsExtractSymlinkChainEscapeRejected — the
// same chain, same shape, same required protection, since extractZip
// shares the same containment code path as extractTar.
func TestRunActionsExtractZipSymlinkChainEscapeRejected(t *testing.T) {
	base, container, staging := nestedTempDirs(t)
	archive := buildZip(t, []zipEntry{
		{name: "y", isDir: true},
		{name: "x/a", isSymlink: true, content: []byte("../y")},
		{name: "x/a/evil", isSymlink: true, content: []byte("../../pwned")},
		{name: "x/a/evil", content: []byte("pwned")},
	})
	artifact := writeArtifact(t, container, archive)

	err := install.RunActions(extractPkg(0), staging, artifact, spec.Current())
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	mustNotExist(t, filepath.Join(container, "pwned"))
	mustNotExist(t, filepath.Join(base, "pwned"))
}

// TestRunActionsExtractTwoHopSymlinkChainEscapeRejected chains two
// shallowing symlinks (y/y2 -> ../z, then y/y2/y3 -> ../y4) before the
// escaping entry, accumulating two levels of "lexical vs. real" depth
// mismatch instead of one. The escaping symlink (y/y2/y3/evil ->
// ../../../pwned) still passes the lexical check (3 ups cancels the
// literal 3-deep prefix "y/y2/y3" exactly), but the real parent directory
// is only one level deep (y4), so the write attempt really lands two
// levels above staging. Verified empirically before writing this test
// (with plain os.* calls in a throwaway dir, no os.Root) that this
// construction does escape exactly two levels up when unprotected.
func TestRunActionsExtractTwoHopSymlinkChainEscapeRejected(t *testing.T) {
	base, container, staging := nestedTempDirs(t)
	archive := gzipBytes(t, buildTar(t, []tarEntry{
		{name: "z", typeflag: tar.TypeDir},
		{name: "y4", typeflag: tar.TypeDir},
		{name: "y/y2", typeflag: tar.TypeSymlink, linkname: "../z"},
		{name: "y/y2/y3", typeflag: tar.TypeSymlink, linkname: "../y4"},
		{name: "y/y2/y3/evil", typeflag: tar.TypeSymlink, linkname: "../../../pwned"},
		{name: "y/y2/y3/evil", content: []byte("pwned"), mode: 0o644},
	}))
	artifact := writeArtifact(t, container, archive)

	err := install.RunActions(extractPkg(0), staging, artifact, spec.Current())
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	mustNotExist(t, filepath.Join(container, "pwned"))
	mustNotExist(t, filepath.Join(base, "pwned"))
}

// TestRunActionsExtractSymlinkChainEscapeViaNestedEntryRejected covers
// the variant where the outside directory the escape targets already
// exists for real (as opposed to something the write itself would
// create), and the write-through uses a distinctly named nested entry
// (x/a/evil/payload) rather than a second entry reusing the escaping
// symlink's own name. Resolving "x/a/evil" as a directory component
// still requires following the escaping symlink, so it is still refused.
func TestRunActionsExtractSymlinkChainEscapeViaNestedEntryRejected(t *testing.T) {
	base, container, staging := nestedTempDirs(t)
	outside := filepath.Join(container, "existing-outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatalf("pre-create outside dir: %v", err)
	}

	archive := gzipBytes(t, buildTar(t, []tarEntry{
		{name: "y", typeflag: tar.TypeDir},
		{name: "x/a", typeflag: tar.TypeSymlink, linkname: "../y"},
		{name: "x/a/evil", typeflag: tar.TypeSymlink, linkname: "../../existing-outside"},
		{name: "x/a/evil/payload", content: []byte("pwned")},
	}))
	artifact := writeArtifact(t, container, archive)

	err := install.RunActions(extractPkg(0), staging, artifact, spec.Current())
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	mustNotExist(t, filepath.Join(outside, "payload"))
	mustNotExist(t, filepath.Join(base, "payload"))
}

func TestRunActionsExtractPaxGlobalHeaderSkipped(t *testing.T) {
	archive := gzipBytes(t, buildTar(t, []tarEntry{
		{name: "pax_global_header", typeflag: tar.TypeXGlobalHeader, paxRecords: map[string]string{"comment": "hello"}},
		{name: "real.txt", content: []byte("hi")},
	}))

	tmp := t.TempDir()
	staging := filepath.Join(tmp, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	artifact := writeArtifact(t, tmp, archive)

	if err := install.RunActions(extractPkg(0), staging, artifact, spec.Current()); err != nil {
		t.Fatalf("RunActions: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(staging, "real.txt"))
	if err != nil || string(got) != "hi" {
		t.Fatalf("real.txt = %q, %v, want %q", got, err, "hi")
	}
}

// TestRunActionsExtractPlainTarWithPKPrefixedFirstEntry guards against
// mis-sniffing: an uncompressed tar's first bytes are its first header's
// name field, so a tar whose first member is named e.g. "PKGBUILD" starts
// with the same two bytes as a zip's "PK" signature. Sniffing must key off
// zip's specific 4-byte record signatures, not a bare "PK" prefix.
func TestRunActionsExtractPlainTarWithPKPrefixedFirstEntry(t *testing.T) {
	archive := buildTar(t, []tarEntry{
		{name: "PKGBUILD", content: []byte("# a real tar entry, not a zip")},
	})
	if !bytes.HasPrefix(archive, []byte("PK")) {
		t.Fatalf("test fixture doesn't actually start with PK, precondition broken")
	}

	tmp := t.TempDir()
	staging := filepath.Join(tmp, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	artifact := writeArtifact(t, tmp, archive)

	if err := install.RunActions(extractPkg(0), staging, artifact, spec.Current()); err != nil {
		t.Fatalf("RunActions: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(staging, "PKGBUILD"))
	if err != nil || string(got) != "# a real tar entry, not a zip" {
		t.Fatalf("PKGBUILD = %q, %v, want the plain-tar content", got, err)
	}
}

func TestRunActionsExtractNegativeStripNoPanic(t *testing.T) {
	archive := gzipBytes(t, buildTar(t, []tarEntry{
		{name: "a/b/file", content: []byte("x")},
	}))

	tmp := t.TempDir()
	staging := filepath.Join(tmp, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	artifact := writeArtifact(t, tmp, archive)

	if err := install.RunActions(extractPkg(-1), staging, artifact, spec.Current()); err != nil {
		t.Fatalf("RunActions: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(staging, "a", "b", "file"))
	if err != nil || string(got) != "x" {
		t.Fatalf("a/b/file = %q, %v, want %q", got, err, "x")
	}
}

// TestRunActionsExtractLargeTarGzStreams builds a several-megabyte tar.gz
// fixture and extracts it, exercising the streaming decode path: gzip
// wrapped over the buffered sniff reader, tar entries copied straight to
// disk one at a time rather than the whole artifact sitting in a []byte.
func TestRunActionsExtractLargeTarGzStreams(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	payload := make([]byte, 5<<20)
	if _, err := rng.Read(payload); err != nil {
		t.Fatalf("fill payload: %v", err)
	}
	want := sha256.Sum256(payload)

	archive := gzipBytes(t, buildTar(t, []tarEntry{
		{name: "big.bin", content: payload, mode: 0o644},
		{name: "small.txt", content: []byte("small"), mode: 0o644},
	}))

	tmp := t.TempDir()
	staging := filepath.Join(tmp, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	artifact := writeArtifact(t, tmp, archive)

	if err := install.RunActions(extractPkg(0), staging, artifact, spec.Current()); err != nil {
		t.Fatalf("RunActions: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(staging, "big.bin"))
	if err != nil {
		t.Fatalf("read big.bin: %v", err)
	}
	if sha256.Sum256(got) != want {
		t.Fatal("big.bin content mismatch after extraction")
	}
	small, err := os.ReadFile(filepath.Join(staging, "small.txt"))
	if err != nil || string(small) != "small" {
		t.Fatalf("small.txt = %q, %v, want %q", small, err, "small")
	}
}

// TestExtractSourceStreamsArtifact keeps the point of this refactor
// enforced at the source level: extract.go must sniff and decode the
// artifact through a reader, never read it whole into a []byte first.
func TestExtractSourceStreamsArtifact(t *testing.T) {
	src, err := os.ReadFile("extract.go")
	if err != nil {
		t.Fatalf("read extract.go: %v", err)
	}
	if bytes.Contains(src, []byte("os.ReadFile")) {
		t.Fatal("extract.go calls os.ReadFile; the artifact must be streamed, not buffered whole")
	}
}
