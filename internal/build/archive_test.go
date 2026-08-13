package build

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTarGzDirRoundTrip(t *testing.T) {
	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(src, "lib", "libz.a"), []byte("archive"), 0o644)
	os.WriteFile(filepath.Join(src, "lib", "libz.1.3.1.dylib"), []byte("dylib"), 0o755)
	os.Symlink("libz.1.3.1.dylib", filepath.Join(src, "lib", "libz.1.dylib"))
	os.MkdirAll(filepath.Join(src, "include"), 0o755)
	os.WriteFile(filepath.Join(src, "include", "zlib.h"), []byte("header"), 0o644)

	var buf bytes.Buffer
	if err := tarGzDir(&buf, src); err != nil {
		t.Fatalf("tarGzDir: %v", err)
	}

	got := map[string]string{} // name -> content, "@"+target for symlinks
	gz, _ := gzip.NewReader(&buf)
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		switch h.Typeflag {
		case tar.TypeSymlink:
			got[h.Name] = "@" + h.Linkname
		case tar.TypeReg:
			b, _ := io.ReadAll(tr)
			got[h.Name] = string(b)
		}
	}
	for name, want := range map[string]string{
		"lib/libz.a":           "archive",
		"lib/libz.1.3.1.dylib": "dylib",
		"lib/libz.1.dylib":     "@libz.1.3.1.dylib",
		"include/zlib.h":       "header",
	} {
		if got[name] != want {
			t.Errorf("entry %q = %q, want %q", name, got[name], want)
		}
	}
}

func TestTarGzDirReproducible(t *testing.T) {
	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(src, "lib", "libz.a"), []byte("archive"), 0o644)
	os.WriteFile(filepath.Join(src, "include.h"), []byte("header"), 0o644)

	var before bytes.Buffer
	if err := tarGzDir(&before, src); err != nil {
		t.Fatalf("tarGzDir: %v", err)
	}

	t0 := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(filepath.Join(src, "lib", "libz.a"), t0, t0); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(src, "lib"), t0, t0); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(src, "include.h"), t0, t0); err != nil {
		t.Fatal(err)
	}

	var after bytes.Buffer
	if err := tarGzDir(&after, src); err != nil {
		t.Fatalf("tarGzDir: %v", err)
	}

	if !bytes.Equal(before.Bytes(), after.Bytes()) {
		t.Fatal("tarGzDir output changed after mtime-only change; mtime is leaking into the archive")
	}
}
