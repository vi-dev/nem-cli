package build

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/vi-dev/nem-cli/internal/fetch"
)

func serve(t *testing.T, body []byte) (*httptest.Server, string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(body) }))
	t.Cleanup(srv.Close)
	sum := sha256.Sum256(body)
	return srv, hex.EncodeToString(sum[:])
}

func makeTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for name, content := range files {
		hdr := &tar.Header{
			Name: name,
			Mode: 0o644,
			Size: int64(len(content)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write tar header %s: %v", name, err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatalf("write tar content %s: %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("close gzip writer: %v", err)
	}
	return buf.Bytes()
}

func TestFetchSourceVerifyAndTOFU(t *testing.T) {
	body := []byte("tarball-bytes")
	srv, want := serve(t, body)
	dir := t.TempDir()
	meta := fetch.Meta{Name: "a", Version: "v1"}

	// pinned + correct → verified
	p, sha, verified, err := fetchSource(context.Background(), http.DefaultClient, srv.URL, want, dir, meta)
	if err != nil || !verified || sha != want {
		t.Fatalf("verify path: p=%q sha=%q verified=%v err=%v", p, sha, verified, err)
	}
	// unpinned → TOFU, returns computed sha, verified=false
	_, sha2, verified2, err := fetchSource(context.Background(), http.DefaultClient, srv.URL, "", dir, meta)
	if err != nil || verified2 || sha2 != want {
		t.Fatalf("tofu path: sha=%q verified=%v err=%v", sha2, verified2, err)
	}
	// pinned + wrong → error
	if _, _, _, err := fetchSource(context.Background(), http.DefaultClient, srv.URL, "deadbeef", dir, meta); err == nil {
		t.Fatal("want checksum mismatch error")
	}
}

// tarEntry describes one entry for buildTarGz, covering the tar shapes
// makeTarGz can't (symlinks, hardlinks, arbitrary typeflags).
type tarEntry struct {
	name     string
	typeflag byte
	content  []byte
	linkname string
	mode     int64
}

func buildTarGz(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for _, e := range entries {
		typeflag := e.typeflag
		if typeflag == 0 {
			typeflag = tar.TypeReg
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
	if err := gw.Close(); err != nil {
		t.Fatalf("close gzip writer: %v", err)
	}
	return buf.Bytes()
}

func writeArchive(t *testing.T, dir string, tgz []byte) string {
	t.Helper()
	arc := filepath.Join(dir, "s.tar.gz")
	if err := os.WriteFile(arc, tgz, 0o644); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	return arc
}

func TestUnpackSourcePathEscapeRejected(t *testing.T) {
	tgz := buildTarGz(t, []tarEntry{
		{name: "../escape.txt", content: []byte("pwned")},
	})
	base := t.TempDir()
	arc := writeArchive(t, base, tgz)
	dest := filepath.Join(base, "dest")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := unpackSource(arc, dest); err == nil {
		t.Fatal("expected error, got nil")
	}
	if _, err := os.Stat(filepath.Join(base, "escape.txt")); !os.IsNotExist(err) {
		t.Fatalf("escape.txt must not exist outside dest: %v", err)
	}
}

func TestUnpackSourceHardlinkRejected(t *testing.T) {
	tgz := buildTarGz(t, []tarEntry{
		{name: "real", content: []byte("data")},
		{name: "hard", typeflag: tar.TypeLink, linkname: "real"},
	})
	arc := writeArchive(t, t.TempDir(), tgz)

	if _, err := unpackSource(arc, t.TempDir()); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestUnpackSourceAbsoluteSymlinkTargetRejected(t *testing.T) {
	tgz := buildTarGz(t, []tarEntry{
		{name: "link", typeflag: tar.TypeSymlink, linkname: "/etc/passwd"},
	})
	arc := writeArchive(t, t.TempDir(), tgz)
	dest := t.TempDir()

	if _, err := unpackSource(arc, dest); err == nil {
		t.Fatal("expected error, got nil")
	}
	if _, err := os.Lstat(filepath.Join(dest, "link")); !os.IsNotExist(err) {
		t.Fatalf("link must not exist: %v", err)
	}
}

// TestUnpackSourceSymlinkChainEscapeRejected chains a symlink pointing
// outside dest ("evil" -> "..") with a regular-file entry that writes
// through it ("evil/x"), the pattern the os.Root routing in unpackSource
// exists to stop: a later entry's write must not follow a symlink an
// earlier entry planted to redirect it outside dest.
func TestUnpackSourceSymlinkChainEscapeRejected(t *testing.T) {
	base := t.TempDir()
	container := filepath.Join(base, "container")
	dest := filepath.Join(container, "dest")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}

	tgz := buildTarGz(t, []tarEntry{
		{name: "evil", typeflag: tar.TypeSymlink, linkname: ".."},
		{name: "evil/x", content: []byte("pwned")},
	})
	arc := writeArchive(t, container, tgz)

	if _, err := unpackSource(arc, dest); err == nil {
		t.Fatal("expected error, got nil")
	}
	if _, err := os.Stat(filepath.Join(container, "x")); !os.IsNotExist(err) {
		t.Fatalf("x must not exist outside dest: %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, "x")); !os.IsNotExist(err) {
		t.Fatalf("x must not exist outside dest: %v", err)
	}
}

func TestUnpackSourceStripsSingleRoot(t *testing.T) {
	tgz := makeTarGz(t, map[string]string{"src-1.0/configure": "#!/bin/sh\n", "src-1.0/main.c": "x"})
	arc := filepath.Join(t.TempDir(), "s.tar.gz")
	os.WriteFile(arc, tgz, 0o644)
	root, err := unpackSource(arc, t.TempDir())
	if err != nil {
		t.Fatalf("unpackSource: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "configure")); err != nil {
		t.Fatalf("expected configure at stripped root: %v", err)
	}
}
