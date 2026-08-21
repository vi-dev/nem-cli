package archive_test

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz"

	"github.com/vi-dev/nem-cli/internal/archive"
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
			Name:     e.name,
			Typeflag: typeflag,
			Linkname: e.linkname,
			Mode:     mode,
			Size:     int64(len(e.content)),
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

// buildV7Tar hand-assembles a pre-POSIX (V7) tar holding one regular file:
// a bare 512-byte header with no "ustar" magic, recognizable only by its
// header checksum. archive/tar can read this format but never writes it.
func buildV7Tar(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var hdr [512]byte
	copy(hdr[0:], name)
	copy(hdr[100:], "0000644\x00")
	copy(hdr[108:], "0000000\x00")
	copy(hdr[116:], "0000000\x00")
	copy(hdr[124:], []byte(octal11(int64(len(content)))))
	copy(hdr[136:], []byte(octal11(0)))
	hdr[156] = '0'
	for i := 148; i < 156; i++ {
		hdr[i] = ' '
	}
	var sum int64
	for _, b := range hdr {
		sum += int64(b)
	}
	copy(hdr[148:], []byte(octal(sum, 6)+"\x00 "))

	var buf bytes.Buffer
	buf.Write(hdr[:])
	buf.Write(content)
	if pad := 512 - len(content)%512; pad != 512 {
		buf.Write(make([]byte, pad))
	}
	buf.Write(make([]byte, 1024))
	return buf.Bytes()
}

func octal(n int64, width int) string {
	s := ""
	for v := n; v > 0; v /= 8 {
		s = string(rune('0'+v%8)) + s
	}
	for len(s) < width {
		s = "0" + s
	}
	return s
}

func octal11(n int64) string { return octal(n, 11) + "\x00" }

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

// extractBytes writes data to a file and extracts it into a fresh dir,
// returning the dir and the extraction result.
func extractBytes(t *testing.T, data []byte, opts archive.Options) (string, archive.Result, error) {
	t.Helper()
	tmp := t.TempDir()
	artifact := filepath.Join(tmp, "artifact")
	if err := os.WriteFile(artifact, data, 0o644); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	dest := filepath.Join(tmp, "dest")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatalf("mkdir dest: %v", err)
	}
	root, err := os.OpenRoot(dest)
	if err != nil {
		t.Fatalf("open root: %v", err)
	}
	defer root.Close()
	res, err := archive.Extract(artifact, root, opts)
	return dest, res, err
}

func mustFile(t *testing.T, path, content string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != content {
		t.Fatalf("%s = %q, %v, want %q", path, got, err, content)
	}
}

func TestExtractTarGzStripModesSymlink(t *testing.T) {
	data := gzipBytes(t, buildTar(t, []tarEntry{
		{name: "pkg-1.0/", typeflag: tar.TypeDir, mode: 0o755},
		{name: "pkg-1.0/bin/tool", content: []byte("binary"), mode: 0o755},
		{name: "pkg-1.0/bin/tool-link", typeflag: tar.TypeSymlink, linkname: "tool"},
	}))

	dest, res, err := extractBytes(t, data, archive.Options{Strip: 1})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if res.CommonPrefix != "pkg-1.0" {
		t.Fatalf("CommonPrefix = %q, want %q", res.CommonPrefix, "pkg-1.0")
	}
	mustFile(t, filepath.Join(dest, "bin", "tool"), "binary")
	info, err := os.Stat(filepath.Join(dest, "bin", "tool"))
	if err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("tool mode = %v, %v, want 0755", info.Mode().Perm(), err)
	}
	target, err := os.Readlink(filepath.Join(dest, "bin", "tool-link"))
	if err != nil || target != "tool" {
		t.Fatalf("symlink target = %q, %v, want %q", target, err, "tool")
	}
}

func TestExtractZip(t *testing.T) {
	data := buildZip(t, []zipEntry{
		{name: "src-1.0/file.txt", content: []byte("hi")},
		{name: "src-1.0/sub/other.txt", content: []byte("there"), mode: 0o755},
		{name: "src-1.0/link", isSymlink: true, content: []byte("file.txt")},
	})

	dest, res, err := extractBytes(t, data, archive.Options{})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if res.CommonPrefix != "src-1.0" {
		t.Fatalf("CommonPrefix = %q, want %q", res.CommonPrefix, "src-1.0")
	}
	mustFile(t, filepath.Join(dest, "src-1.0", "file.txt"), "hi")
	mustFile(t, filepath.Join(dest, "src-1.0", "sub", "other.txt"), "there")
	target, err := os.Readlink(filepath.Join(dest, "src-1.0", "link"))
	if err != nil || target != "file.txt" {
		t.Fatalf("link target = %q, %v, want %q", target, err, "file.txt")
	}
}

func TestExtractPlainTarUstar(t *testing.T) {
	data := buildTar(t, []tarEntry{{name: "plain.txt", content: []byte("no compression")}})
	dest, _, err := extractBytes(t, data, archive.Options{})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	mustFile(t, filepath.Join(dest, "plain.txt"), "no compression")
}

func TestExtractPlainTarV7Checksum(t *testing.T) {
	data := buildV7Tar(t, "old.txt", []byte("pre-posix"))
	if bytes.Contains(data[:512], []byte("ustar")) {
		t.Fatal("fixture unexpectedly carries ustar magic, precondition broken")
	}
	dest, _, err := extractBytes(t, data, archive.Options{})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	mustFile(t, filepath.Join(dest, "old.txt"), "pre-posix")
}

func TestExtractTarXzAndZstd(t *testing.T) {
	tarBytes := buildTar(t, []tarEntry{{name: "bin/tool", content: []byte("payload"), mode: 0o755}})
	for name, data := range map[string][]byte{
		"xz":   xzBytes(t, tarBytes),
		"zstd": zstdBytes(t, tarBytes),
	} {
		dest, _, err := extractBytes(t, data, archive.Options{})
		if err != nil {
			t.Fatalf("Extract %s: %v", name, err)
		}
		mustFile(t, filepath.Join(dest, "bin", "tool"), "payload")
	}
}

// bzip2TarFixtureB64 is `tar -cf - a | bzip2 -9`, base64-encoded, holding
// a/hello.txt with content "hi\n". compress/bzip2 has no writer, so the
// fixture was produced once with the system bzip2 and embedded.
const bzip2TarFixtureB64 = `QlpoOTFBWSZTWXOm2KsAAO1/sP+4I4BQCf/iOm//c+/v/0AAQo4wAAhAAhwMQBxkaZMTQZMmE0yBkNAaA0yaGAE0BhJI1R6CI0ABoAAAMg0yAAAAHGRpkxNBkyYTTIGQ0BoDTJoYATQGCpSTIiep6myanqb1TTEyaNDTQAGh6jR6mIYh6nqacC+yplT+PzPj0qa/6bmzLGumu1pcj8sLVfsqCvsqkm4owsi8uIOuoSpSSGZaqdpQTDi3W6rmMTnSpdSgOoURfUb7cKk6xdJU0lRoU3VK1rl42HgsJ7SjvM5UmJLqUTgSqlKKdZOJZO0ltry0xWXGo8zKpZYfVqqt7mTZNtOifZwvFLFOvMmATdx1BMBNlKJ8ceZx8lhSBMKakVwdMLKrxQRGFXcyYiqilcIzaxOUDC2GZxRFAgL8orZ/IzxL4VLCdMUJyzwFikqCZNUmYVgTSrKYiTiccklJQFRAVVpyMAcYVy5RdrmnW0thMLK+DiNZa97lcRwuRmZjY7K+qdJcb+/9n3Uft92leWLH0VuhfXWBYsN5RL/No3tTRztpypjTzp1UuJadDYeheTMvFxws6UcKjaa8xp/HMmkl9NpbbS3Txs6Xk/qa0xTKnG21E8SxNRNlKJemRNhOolaWp22d3VaYi+ol5M6dVNuaCbS6exNMuJgmCxNCVZEyLy6XZYc50lT/KI6SiP+LuSKcKEg502xVgA==`

func TestExtractTarBzip2(t *testing.T) {
	data, err := base64.StdEncoding.DecodeString(bzip2TarFixtureB64)
	if err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	dest, _, err := extractBytes(t, data, archive.Options{})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	mustFile(t, filepath.Join(dest, "a", "hello.txt"), "hi\n")
}

func TestExtractSingleFileGz(t *testing.T) {
	dest, res, err := extractBytes(t, gzipBytes(t, []byte("#!/bin/sh\necho hi\n")), archive.Options{SingleName: "tool"})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if res.CommonPrefix != "" {
		t.Fatalf("CommonPrefix = %q, want empty for single file", res.CommonPrefix)
	}
	out := filepath.Join(dest, "tool")
	mustFile(t, out, "#!/bin/sh\necho hi\n")
	info, err := os.Stat(out)
	if err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %v, %v, want 0755", info.Mode().Perm(), err)
	}
}

// TestExtractSingleFileGzLarge uses a payload well past the 512-byte inner
// sniff window, so detection must decide from the peeked prefix and the
// copy must still deliver every byte.
func TestExtractSingleFileGzLarge(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	payload := make([]byte, 2<<20)
	if _, err := rng.Read(payload); err != nil {
		t.Fatalf("fill payload: %v", err)
	}
	dest, _, err := extractBytes(t, gzipBytes(t, payload), archive.Options{SingleName: "blob"})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dest, "blob"))
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("blob mismatch (len %d vs %d), err %v", len(got), len(payload), err)
	}
}

func TestExtractSingleFileXzAndZstd(t *testing.T) {
	for name, data := range map[string][]byte{
		"xz":   xzBytes(t, []byte("xz-single")),
		"zstd": zstdBytes(t, []byte("xz-single")),
	} {
		dest, _, err := extractBytes(t, data, archive.Options{SingleName: "bin"})
		if err != nil {
			t.Fatalf("Extract %s: %v", name, err)
		}
		mustFile(t, filepath.Join(dest, "bin"), "xz-single")
	}
}

// bzip2SingleFixtureB64 is `printf '#!/bin/sh\necho single\n' | bzip2 -9`,
// base64-encoded; embedded because compress/bzip2 has no writer.
const bzip2SingleFixtureB64 = `QlpoOTFBWSZTWeXQUd4AAAJRgAAQaACa5YgAIAAigwg3pQpgACeENBuR2VVkVjq+LuSKcKEhy6CjvA==`

func TestExtractSingleFileBzip2(t *testing.T) {
	data, err := base64.StdEncoding.DecodeString(bzip2SingleFixtureB64)
	if err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	dest, _, err := extractBytes(t, data, archive.Options{SingleName: "run.sh"})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	mustFile(t, filepath.Join(dest, "run.sh"), "#!/bin/sh\necho single\n")
}

func TestExtractSingleFileWithoutNameErrors(t *testing.T) {
	_, _, err := extractBytes(t, gzipBytes(t, []byte("just bytes")), archive.Options{})
	if err == nil || !strings.Contains(err.Error(), "single file") {
		t.Fatalf("error = %v, want single-file naming error", err)
	}
}

// TestExtractSingleFileIgnoresStrip: strip counts leading path components
// of archive entries; a decompressed single file has none, so strip must
// not suppress or rename it.
func TestExtractSingleFileIgnoresStrip(t *testing.T) {
	dest, _, err := extractBytes(t, gzipBytes(t, []byte("data")), archive.Options{Strip: 3, SingleName: "f"})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	mustFile(t, filepath.Join(dest, "f"), "data")
}

func TestExtractCommonPrefixMultipleRoots(t *testing.T) {
	data := gzipBytes(t, buildTar(t, []tarEntry{
		{name: "a/x", content: []byte("1")},
		{name: "b/y", content: []byte("2")},
	}))
	_, res, err := extractBytes(t, data, archive.Options{})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if res.CommonPrefix != "" {
		t.Fatalf("CommonPrefix = %q, want empty", res.CommonPrefix)
	}
}

// TestExtractCommonPrefixIgnoresPaxHeader: the synthetic pax_global_header
// entry is not a filesystem object and must not break prefix detection.
func TestExtractCommonPrefixIgnoresPaxHeader(t *testing.T) {
	data := gzipBytes(t, buildTar(t, []tarEntry{
		{name: "pax_global_header", typeflag: tar.TypeXGlobalHeader, paxRecords: map[string]string{"comment": "x"}},
		{name: "src-1.0/main.c", content: []byte("int main(){}")},
	}))
	dest, res, err := extractBytes(t, data, archive.Options{})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if res.CommonPrefix != "src-1.0" {
		t.Fatalf("CommonPrefix = %q, want %q", res.CommonPrefix, "src-1.0")
	}
	mustFile(t, filepath.Join(dest, "src-1.0", "main.c"), "int main(){}")
}

func TestExtractEscapeEntryRejected(t *testing.T) {
	data := gzipBytes(t, buildTar(t, []tarEntry{{name: "../evil", content: []byte("pwned")}}))
	_, _, err := extractBytes(t, data, archive.Options{})
	if err == nil || !strings.Contains(err.Error(), "escapes extraction root") {
		t.Fatalf("error = %v, want containment error", err)
	}
}

func TestExtractZipEscapeEntryRejected(t *testing.T) {
	data := buildZip(t, []zipEntry{{name: "../evil", content: []byte("pwned")}})
	_, _, err := extractBytes(t, data, archive.Options{})
	if err == nil || !strings.Contains(err.Error(), "escapes extraction root") {
		t.Fatalf("error = %v, want containment error", err)
	}
}

func TestExtractAbsoluteSymlinkRejected(t *testing.T) {
	data := gzipBytes(t, buildTar(t, []tarEntry{
		{name: "link", typeflag: tar.TypeSymlink, linkname: "/etc/passwd"},
	}))
	_, _, err := extractBytes(t, data, archive.Options{})
	if err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("error = %v, want absolute-target error", err)
	}
}

func TestExtractHardlinkRejected(t *testing.T) {
	data := gzipBytes(t, buildTar(t, []tarEntry{
		{name: "real", content: []byte("data")},
		{name: "hard", typeflag: tar.TypeLink, linkname: "real"},
	}))
	_, _, err := extractBytes(t, data, archive.Options{})
	if err == nil || !strings.Contains(err.Error(), "hardlinks are not supported") {
		t.Fatalf("error = %v, want hardlink error", err)
	}
}

func TestSingleNameFromRef(t *testing.T) {
	cases := map[string]string{
		"https://ex.com/dl/tool-1.0.gz?token=x": "tool-1.0",
		"tool_v1.zst":                           "tool_v1",
		"https://ex.com/src.tar.gz":             "src.tar",
		"plain-name":                            "plain-name",
		".gz":                                   "",
		"":                                      "",
		"https://ex.com/":                       "",
	}
	for ref, want := range cases {
		if got := archive.SingleNameFromRef(ref); got != want {
			t.Errorf("SingleNameFromRef(%q) = %q, want %q", ref, got, want)
		}
	}
}

func TestExtractUnrecognizedFormatErrors(t *testing.T) {
	_, _, err := extractBytes(t, []byte("not an archive at all"), archive.Options{})
	if err == nil || !strings.Contains(err.Error(), "unrecognized archive format") {
		t.Fatalf("error = %v, want unrecognized-format error", err)
	}
}
