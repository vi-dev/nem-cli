package main

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
	"strings"
	"testing"

	"github.com/vi-dev/nem-cli/internal/selfupdate"
)

// releaseFixture serves a fake GitHub release for tag: the latest-release
// API answer, the platform's asset archive holding script as the nem
// binary, and its checksums.txt. It also stages a fake installed binary
// and returns its path.
func releaseFixture(t *testing.T, tag, script string) string {
	t.Helper()

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	hdr := &tar.Header{Name: "nem", Mode: 0o755, Size: int64(len(script))}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatalf("write tar header: %v", err)
	}
	if _, err := tw.Write([]byte(script)); err != nil {
		t.Fatalf("write tar entry: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	archive := buf.Bytes()

	asset := fmt.Sprintf("nem_%s_%s_%s.tar.gz", tag, runtime.GOOS, runtime.GOARCH)
	sum := sha256.Sum256(archive)
	checksums := fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), asset)

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/vi-dev/nem-cli/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"tag_name": "` + tag + `"}`))
	})
	mux.HandleFunc("/download/"+tag+"/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(checksums))
	})
	mux.HandleFunc("/download/"+tag+"/"+asset, func(w http.ResponseWriter, r *http.Request) {
		w.Write(archive)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	withUpdater(t, &selfupdate.Updater{
		Client:       srv.Client(),
		APIBase:      srv.URL,
		DownloadBase: srv.URL + "/download",
		Repo:         "vi-dev/nem-cli",
	})

	exePath := filepath.Join(t.TempDir(), "nem")
	if err := os.WriteFile(exePath, []byte("old-binary"), 0o755); err != nil {
		t.Fatalf("seed exe: %v", err)
	}
	oldPath := selfExecutablePath
	selfExecutablePath = func() (string, error) { return exePath, nil }
	t.Cleanup(func() { selfExecutablePath = oldPath })
	return exePath
}

// execNemSelf executes the root command with args against an isolated
// NEM_HOME and returns the combined output.
func execNemSelf(t *testing.T, args ...string) (string, error) {
	t.Helper()
	t.Setenv("NEM_HOME", t.TempDir())

	cmd := newRoot()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

// withVersion swaps the build-time version for the test's duration.
func withVersion(t *testing.T, v string) {
	t.Helper()
	old := version
	version = v
	t.Cleanup(func() { version = old })
}

// withUpdater points the self update command at a test updater.
func withUpdater(t *testing.T, u *selfupdate.Updater) {
	t.Helper()
	old := newSelfUpdater
	newSelfUpdater = func() *selfupdate.Updater { return u }
	t.Cleanup(func() { newSelfUpdater = old })
}

// latestReleaseAPI serves a GitHub releases/latest response answering tag.
func latestReleaseAPI(t *testing.T, tag string) *selfupdate.Updater {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"tag_name": "` + tag + `"}`))
	}))
	t.Cleanup(srv.Close)
	return &selfupdate.Updater{Client: srv.Client(), APIBase: srv.URL, Repo: "vi-dev/nem-cli"}
}

func TestSelfUpdateCheckUpdateAvailable(t *testing.T) {
	withVersion(t, "v1.0.0")
	withUpdater(t, latestReleaseAPI(t, "v1.1.0"))

	out, err := execNemSelf(t, "self", "update", "--check")
	if err != nil {
		t.Fatalf("self update --check: %v\n%s", err, out)
	}
	if !strings.Contains(out, "v1.0.0") || !strings.Contains(out, "v1.1.0") {
		t.Errorf("output does not name both versions:\n%s", out)
	}
}

func TestSelfUpdateCheckUpToDate(t *testing.T) {
	withVersion(t, "v1.1.0")
	withUpdater(t, latestReleaseAPI(t, "v1.1.0"))

	out, err := execNemSelf(t, "self", "update", "--check")
	if err != nil {
		t.Fatalf("self update --check: %v\n%s", err, out)
	}
	if !strings.Contains(out, "up to date") {
		t.Errorf("output missing up-to-date notice:\n%s", out)
	}
}

func TestSelfUpdateInstallsLatest(t *testing.T) {
	withVersion(t, "v1.0.0")
	script := "#!/bin/sh\nexit 0\n"
	exePath := releaseFixture(t, "v1.1.0", script)

	out, err := execNemSelf(t, "self", "update")
	if err != nil {
		t.Fatalf("self update: %v\n%s", err, out)
	}
	got, err := os.ReadFile(exePath)
	if err != nil {
		t.Fatalf("read updated binary: %v", err)
	}
	if string(got) != script {
		t.Errorf("binary content = %q, want the downloaded script", got)
	}
	if !strings.Contains(out, "https://github.com/vi-dev/nem-cli/releases/tag/v1.1.0") {
		t.Errorf("output missing release notes link:\n%s", out)
	}
}

func TestSelfUpdateUnstableWarns(t *testing.T) {
	withVersion(t, "v1.0.0")
	script := "#!/bin/sh\nexit 0\n"
	exePath := releaseFixture(t, "unstable", script)

	out, err := execNemSelf(t, "self", "update", "--version", "unstable")
	if err != nil {
		t.Fatalf("self update --version unstable: %v\n%s", err, out)
	}
	if !strings.Contains(out, "WARN") || !strings.Contains(out, "unstable") {
		t.Errorf("output missing unstable warning:\n%s", out)
	}
	got, _ := os.ReadFile(exePath)
	if string(got) != script {
		t.Errorf("binary content = %q, want the downloaded script", got)
	}
	if strings.Contains(out, "releases/tag") {
		t.Errorf("unstable update should not link release notes:\n%s", out)
	}
}

func TestSelfUpdateRefusesDevBuild(t *testing.T) {
	withVersion(t, "dev")
	withUpdater(t, latestReleaseAPI(t, "v1.1.0"))

	_, err := execNemSelf(t, "self", "update")
	if err == nil {
		t.Fatal("want error for dev build")
	}
	if !strings.Contains(err.Error(), "release") {
		t.Errorf("error does not explain the dev-build refusal: %v", err)
	}
}

func TestSelfUpdateRejectsMalformedVersion(t *testing.T) {
	withVersion(t, "v1.0.0")
	withUpdater(t, latestReleaseAPI(t, "v1.1.0"))

	_, err := execNemSelf(t, "self", "update", "--version", "1.2.3")
	if err == nil {
		t.Fatal("want error for version without v prefix")
	}
	if !strings.Contains(err.Error(), "1.2.3") {
		t.Errorf("error does not name the bad value: %v", err)
	}
}
