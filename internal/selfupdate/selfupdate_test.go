package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
)

// releaseServer serves a fake GitHub release: the platform's asset archive
// holding script as the nem binary, and a checksums.txt whose digest is
// sha256(archive) unless overridden via badSum. It returns the Updater
// pointed at it and a counter of requests seen.
func releaseServer(t *testing.T, version, script string, badSum bool) (*Updater, *atomic.Int32) {
	t.Helper()
	asset := fmt.Sprintf("nem_%s_%s_%s.tar.gz", version, runtime.GOOS, runtime.GOARCH)
	archive, err := os.ReadFile(buildArchive(t, map[string]string{"nem": script}))
	if err != nil {
		t.Fatalf("read archive fixture: %v", err)
	}
	sum := sha256.Sum256(archive)
	digest := hex.EncodeToString(sum[:])
	if badSum {
		digest = "deadbeef"
	}
	checksums := fmt.Sprintf("%s  %s\n", digest, asset)

	var requests atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/download/"+version+"/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Write([]byte(checksums))
	})
	mux.HandleFunc("/download/"+version+"/"+asset, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Write(archive)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	u := &Updater{Client: srv.Client(), DownloadBase: srv.URL + "/download", Repo: "vi-dev/nem-cli"}
	return u, &requests
}

// seedExe places a fake installed nem binary in a fresh temp dir.
func seedExe(t *testing.T) string {
	t.Helper()
	exePath := filepath.Join(t.TempDir(), "nem")
	if err := os.WriteFile(exePath, []byte("old-binary"), 0o755); err != nil {
		t.Fatalf("seed exe: %v", err)
	}
	return exePath
}

func TestUpdateReplacesBinary(t *testing.T) {
	script := "#!/bin/sh\nexit 0\n"
	u, _ := releaseServer(t, "v2.0.0", script, false)
	exePath := seedExe(t)

	if err := u.Update(t.Context(), "v2.0.0", exePath, nil); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err := os.ReadFile(exePath)
	if err != nil {
		t.Fatalf("read updated binary: %v", err)
	}
	if string(got) != script {
		t.Errorf("binary content = %q, want the downloaded script", got)
	}
	leftovers, err := filepath.Glob(filepath.Join(filepath.Dir(exePath), ".nem-update-*"))
	if err != nil {
		t.Fatalf("glob staging leftovers: %v", err)
	}
	if len(leftovers) > 0 {
		t.Errorf("staging files left behind: %v", leftovers)
	}
}

func TestUpdateChecksumMismatch(t *testing.T) {
	u, _ := releaseServer(t, "v2.0.0", "#!/bin/sh\nexit 0\n", true)
	exePath := seedExe(t)

	if err := u.Update(t.Context(), "v2.0.0", exePath, nil); err == nil {
		t.Fatal("want error on checksum mismatch")
	}
	got, _ := os.ReadFile(exePath)
	if string(got) != "old-binary" {
		t.Errorf("installed binary changed on failed update: %q", got)
	}
}

func TestUpdateSmokeFailure(t *testing.T) {
	u, _ := releaseServer(t, "v2.0.0", "#!/bin/sh\nexit 1\n", false)
	exePath := seedExe(t)

	if err := u.Update(t.Context(), "v2.0.0", exePath, nil); err == nil {
		t.Fatal("want error when new binary fails to run")
	}
	got, _ := os.ReadFile(exePath)
	if string(got) != "old-binary" {
		t.Errorf("installed binary changed on failed update: %q", got)
	}
}

func TestUpdateUnwritableDir(t *testing.T) {
	u, requests := releaseServer(t, "v2.0.0", "#!/bin/sh\nexit 0\n", false)
	exePath := seedExe(t)
	dir := filepath.Dir(exePath)
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatalf("chmod dir read-only: %v", err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })

	err := u.Update(t.Context(), "v2.0.0", exePath, nil)
	if err == nil {
		t.Fatal("want error for unwritable install directory")
	}
	if got := requests.Load(); got != 0 {
		t.Errorf("made %d HTTP requests before failing writability preflight, want 0", got)
	}
}

// buildArchive writes a tar.gz holding the given name → content entries to
// a temp file and returns its path.
func buildArchive(t *testing.T, entries map[string]string) string {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for name, content := range entries {
		hdr := &tar.Header{Name: name, Mode: 0o755, Size: int64(len(content))}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write tar header: %v", err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatalf("write tar entry: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	path := filepath.Join(t.TempDir(), "archive.tar.gz")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	return path
}

func TestExtractBinaryAtRoot(t *testing.T) {
	archive := buildArchive(t, map[string]string{
		"README.md": "docs",
		"nem":       "binary-bytes",
	})
	dest := filepath.Join(t.TempDir(), "nem-staged")
	if err := extractBinary(archive, dest); err != nil {
		t.Fatalf("extractBinary: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read extracted binary: %v", err)
	}
	if string(got) != "binary-bytes" {
		t.Errorf("content = %q, want %q", got, "binary-bytes")
	}
	info, err := os.Stat(dest)
	if err != nil {
		t.Fatalf("stat extracted binary: %v", err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("mode = %v, want 0755", info.Mode().Perm())
	}
}

func TestExtractBinaryNested(t *testing.T) {
	archive := buildArchive(t, map[string]string{
		"nem_v1.2.3_darwin_arm64/nem": "nested-binary",
	})
	dest := filepath.Join(t.TempDir(), "nem-staged")
	if err := extractBinary(archive, dest); err != nil {
		t.Fatalf("extractBinary: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read extracted binary: %v", err)
	}
	if string(got) != "nested-binary" {
		t.Errorf("content = %q, want %q", got, "nested-binary")
	}
}

func TestExtractBinaryIntoExistingFile(t *testing.T) {
	archive := buildArchive(t, map[string]string{"nem": "new-binary"})
	dest := filepath.Join(t.TempDir(), "nem-staged")
	if err := os.WriteFile(dest, []byte("placeholder"), 0o600); err != nil {
		t.Fatalf("seed dest: %v", err)
	}
	if err := extractBinary(archive, dest); err != nil {
		t.Fatalf("extractBinary: %v", err)
	}
	info, err := os.Stat(dest)
	if err != nil {
		t.Fatalf("stat extracted binary: %v", err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("mode = %v, want 0755", info.Mode().Perm())
	}
}

func TestExtractBinaryMissing(t *testing.T) {
	archive := buildArchive(t, map[string]string{"README.md": "docs"})
	dest := filepath.Join(t.TempDir(), "nem-staged")
	if err := extractBinary(archive, dest); err == nil {
		t.Fatal("want error when archive has no nem binary")
	}
}

func TestResolveLatest(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Write([]byte(`{"tag_name": "v1.4.0"}`))
	}))
	defer srv.Close()

	u := &Updater{Client: srv.Client(), APIBase: srv.URL, Repo: "vi-dev/nem-cli", Token: "tok123"}
	tag, err := u.ResolveLatest(t.Context())
	if err != nil {
		t.Fatalf("ResolveLatest: %v", err)
	}
	if tag != "v1.4.0" {
		t.Errorf("tag = %q, want %q", tag, "v1.4.0")
	}
	if want := "/repos/vi-dev/nem-cli/releases/latest"; gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
	if want := "Bearer tok123"; gotAuth != want {
		t.Errorf("auth = %q, want %q", gotAuth, want)
	}
}

func TestResolveLatestAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusForbidden)
	}))
	defer srv.Close()

	u := &Updater{Client: srv.Client(), APIBase: srv.URL, Repo: "vi-dev/nem-cli"}
	if _, err := u.ResolveLatest(t.Context()); err == nil {
		t.Fatal("want error on non-200 API response")
	}
}

const checksumsFixture = "" +
	"a1b2c3  nem_v1.2.3_darwin_arm64.tar.gz\n" +
	"d4e5f6  nem_v1.2.3_linux_amd64.tar.gz\n" +
	"0912ab  nem_v1.2.3_darwin_arm64.tar.gz.sbom.spdx.json\n"

func TestParseChecksums(t *testing.T) {
	got, err := parseChecksums([]byte(checksumsFixture), "nem_v1.2.3_linux_amd64.tar.gz")
	if err != nil {
		t.Fatalf("parseChecksums: %v", err)
	}
	if got != "d4e5f6" {
		t.Errorf("got %q, want %q", got, "d4e5f6")
	}
}

func TestParseChecksumsMissingEntry(t *testing.T) {
	_, err := parseChecksums([]byte(checksumsFixture), "nem_v9.9.9_linux_amd64.tar.gz")
	if err == nil {
		t.Fatal("want error for missing entry")
	}
}

func TestNewUpdaterDefaults(t *testing.T) {
	u := NewUpdater("tok")
	if u.APIBase != "https://api.github.com" {
		t.Errorf("APIBase = %q", u.APIBase)
	}
	if want := "https://github.com/vi-dev/nem-cli/releases/download"; u.DownloadBase != want {
		t.Errorf("DownloadBase = %q, want %q", u.DownloadBase, want)
	}
	if u.Repo != "vi-dev/nem-cli" {
		t.Errorf("Repo = %q", u.Repo)
	}
	if u.Token != "tok" {
		t.Errorf("Token = %q", u.Token)
	}
	if u.Client == nil {
		t.Error("Client is nil")
	}
}

func TestExecutablePath(t *testing.T) {
	got, err := ExecutablePath()
	if err != nil {
		t.Fatalf("ExecutablePath: %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("path %q is not absolute", got)
	}
	info, err := os.Lstat(got)
	if err != nil {
		t.Fatalf("lstat %s: %v", got, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Errorf("path %q is still a symlink", got)
	}
}

func TestAssetName(t *testing.T) {
	tests := []struct {
		version, goos, goarch string
		want                  string
	}{
		{"v1.2.3", "darwin", "arm64", "nem_v1.2.3_darwin_arm64.tar.gz"},
		{"unstable", "linux", "amd64", "nem_unstable_linux_amd64.tar.gz"},
	}
	for _, tt := range tests {
		if got := assetName(tt.version, tt.goos, tt.goarch); got != tt.want {
			t.Errorf("assetName(%q, %q, %q) = %q, want %q", tt.version, tt.goos, tt.goarch, got, tt.want)
		}
	}
}
