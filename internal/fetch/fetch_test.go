package fetch_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vi-dev/nem-cli/internal/fetch"
	"github.com/vi-dev/nem-cli/internal/report"
	"github.com/vi-dev/nem-cli/internal/spec"
)

// fakeTask is a minimal report.Task recorder for asserting what a download
// reports without depending on internal/report's rendering.
type fakeTask struct {
	mu       sync.Mutex
	progress []progressCall
}

type progressCall struct{ done, total int64 }

func (f *fakeTask) Status(string)  {}
func (f *fakeTask) Count(int, int) {}
func (f *fakeTask) Done(string)    {}
func (f *fakeTask) Fail(string)    {}
func (f *fakeTask) Discard()       {}
func (f *fakeTask) Progress(done, total int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.progress = append(f.progress, progressCall{done, total})
}

func (f *fakeTask) calls() []progressCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]progressCall(nil), f.progress...)
}

var _ report.Task = (*fakeTask)(nil)

func testMeta() fetch.Meta {
	return fetch.Meta{Name: "go", Version: "v1.2.3", Platform: spec.Platform{OS: "linux", Arch: "amd64"}}
}

func TestDownloadSuccess(t *testing.T) {
	body := []byte("hello artifact bytes")
	sum := sha256.Sum256(body)
	want := hex.EncodeToString(sum[:])

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	defer srv.Close()

	dir := t.TempDir()
	path, err := fetch.Download(context.Background(), srv.Client(), srv.URL, want, dir, testMeta(), nil)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	defer os.Remove(path)

	if filepath.Dir(path) != dir {
		t.Errorf("temp path %q not in dir %q", path, dir)
	}
	base := filepath.Base(path)
	if !strings.HasPrefix(base, "go-v1.2.3-") || !strings.HasSuffix(base, ".tmp") {
		t.Errorf("temp path base %q missing name-version-*.tmp shape", base)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("downloaded bytes = %q, want %q", got, body)
	}
}

func TestDownloadChecksumMismatch(t *testing.T) {
	body := []byte("hello artifact bytes")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	defer srv.Close()

	dir := t.TempDir()
	wantSHA := strings.Repeat("a", 64)
	_, err := fetch.Download(context.Background(), srv.Client(), srv.URL, wantSHA, dir, testMeta(), nil)

	var cme *fetch.ChecksumMismatchError
	if !errors.As(err, &cme) {
		t.Fatalf("want ChecksumMismatchError, got %v", err)
	}
	if cme.Want != wantSHA {
		t.Errorf("Want = %q, want %q", cme.Want, wantSHA)
	}
	sum := sha256.Sum256(body)
	wantGot := hex.EncodeToString(sum[:])
	if cme.Got != wantGot {
		t.Errorf("Got = %q, want %q", cme.Got, wantGot)
	}
	if cme.Name != "go" || cme.Version != "v1.2.3" {
		t.Errorf("Name/Version = %q/%q", cme.Name, cme.Version)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("temp file not cleaned up after mismatch: %v", entries)
	}
}

func TestDownloadNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv.Close()

	dir := t.TempDir()
	_, err := fetch.Download(context.Background(), srv.Client(), srv.URL, "x", dir, testMeta(), nil)

	var nf *fetch.ArtifactNotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("want ArtifactNotFoundError, got %v", err)
	}
	if nf.Missing != "url" {
		t.Errorf("Missing = %q, want %q", nf.Missing, "url")
	}
	if nf.Name != "go" || nf.Version != "v1.2.3" {
		t.Errorf("Name/Version = %q/%q", nf.Name, nf.Version)
	}
}

func TestDownloadGoneIsNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", http.StatusGone)
	}))
	defer srv.Close()

	dir := t.TempDir()
	_, err := fetch.Download(context.Background(), srv.Client(), srv.URL, "x", dir, testMeta(), nil)

	var nf *fetch.ArtifactNotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("want ArtifactNotFoundError, got %v", err)
	}
}

func TestDownloadOtherStatusWraps(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	dir := t.TempDir()
	_, err := fetch.Download(context.Background(), srv.Client(), srv.URL, "x", dir, testMeta(), nil)
	if err == nil {
		t.Fatal("want error for 500 status")
	}
	var nf *fetch.ArtifactNotFoundError
	if errors.As(err, &nf) {
		t.Fatalf("500 must not be reported as ArtifactNotFoundError: %v", err)
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error %q does not mention status", err.Error())
	}
}

func TestDownloadContextCancelMidDownload(t *testing.T) {
	sentFirstChunk := make(chan struct{})
	blockUntil := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("ResponseWriter does not support flushing")
		}
		w.Write([]byte("first-chunk-of-artifact-bytes"))
		flusher.Flush()
		close(sentFirstChunk)
		<-blockUntil
	}))
	defer srv.Close()
	defer close(blockUntil)

	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		_, err := fetch.Download(ctx, srv.Client(), srv.URL, strings.Repeat("a", 64), dir, testMeta(), nil)
		errCh <- err
	}()

	select {
	case <-sentFirstChunk:
	case <-time.After(5 * time.Second):
		t.Fatal("server never sent first chunk")
	}
	cancel()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("want error after context cancel mid-download")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Download did not return after context cancel")
	}
}

func TestDownloadFeedsProgress(t *testing.T) {
	body := []byte("progress body bytes for a small download")
	sum := sha256.Sum256(body)
	want := hex.EncodeToString(sum[:])

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	defer srv.Close()

	ft := &fakeTask{}
	dir := t.TempDir()
	path, err := fetch.Download(context.Background(), srv.Client(), srv.URL, want, dir, testMeta(), ft)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	defer os.Remove(path)

	calls := ft.calls()
	if len(calls) == 0 {
		t.Fatal("want at least one Progress call when task is non-nil")
	}
	last := calls[len(calls)-1]
	if last.done != int64(len(body)) {
		t.Errorf("final Progress done = %d, want %d", last.done, len(body))
	}
	if last.total != int64(len(body)) {
		t.Errorf("final Progress total = %d, want %d", last.total, len(body))
	}
}

func TestDownloadFeedsProgressPeriodically(t *testing.T) {
	body := bytes.Repeat([]byte("x"), 600*1024) // several 256KB chunks
	sum := sha256.Sum256(body)
	want := hex.EncodeToString(sum[:])

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	defer srv.Close()

	ft := &fakeTask{}
	dir := t.TempDir()
	path, err := fetch.Download(context.Background(), srv.Client(), srv.URL, want, dir, testMeta(), ft)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	defer os.Remove(path)

	calls := ft.calls()
	if len(calls) < 2 {
		t.Fatalf("want multiple periodic Progress calls for a %d-byte body, got %d", len(body), len(calls))
	}
	for i := 1; i < len(calls); i++ {
		if calls[i].done < calls[i-1].done {
			t.Errorf("Progress done went backwards: %d then %d", calls[i-1].done, calls[i].done)
		}
	}
	last := calls[len(calls)-1]
	if last.done != int64(len(body)) {
		t.Errorf("final Progress done = %d, want %d", last.done, len(body))
	}
}

func TestDownloadNilTaskOK(t *testing.T) {
	body := []byte("no task here")
	sum := sha256.Sum256(body)
	want := hex.EncodeToString(sum[:])

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	defer srv.Close()

	dir := t.TempDir()
	path, err := fetch.Download(context.Background(), srv.Client(), srv.URL, want, dir, testMeta(), nil)
	if err != nil {
		t.Fatalf("Download with nil task: %v", err)
	}
	os.Remove(path)
}

func TestDownloadInvalidURLErrors(t *testing.T) {
	dir := t.TempDir()
	_, err := fetch.Download(context.Background(), http.DefaultClient, "://not-a-url", "x", dir, testMeta(), nil)
	if err == nil {
		t.Fatal("want error for an invalid url")
	}
}

func TestArtifactNotFoundErrorMessage(t *testing.T) {
	err := &fetch.ArtifactNotFoundError{Name: "go", Version: "v1.2.3", Platform: "linux/amd64", Missing: "url"}
	got := err.Error()
	if got == "" || got[0] < 'a' || got[0] > 'z' {
		t.Errorf("error message must be lowercase-first: %q", got)
	}
	for _, want := range []string{"go", "v1.2.3", "linux/amd64", "url"} {
		if !strings.Contains(got, want) {
			t.Errorf("error message %q missing %q", got, want)
		}
	}
}

func TestChecksumMismatchErrorMessage(t *testing.T) {
	err := &fetch.ChecksumMismatchError{Name: "go", Version: "v1.2.3", Platform: "linux/amd64", Got: "aaaa", Want: "bbbb"}
	got := err.Error()
	if got == "" || got[0] < 'a' || got[0] > 'z' {
		t.Errorf("error message must be lowercase-first: %q", got)
	}
	for _, want := range []string{"go", "v1.2.3", "linux/amd64", "aaaa", "bbbb"} {
		if !strings.Contains(got, want) {
			t.Errorf("error message %q missing %q", got, want)
		}
	}
}

func TestGitHubURL(t *testing.T) {
	got := fetch.GitHubURL("golang/go", "v1.26.5", "go1.26.5.linux-amd64.tar.gz")
	want := "https://github.com/golang/go/releases/download/v1.26.5/go1.26.5.linux-amd64.tar.gz"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestUpstreamURLEmptyArtifactErrors(t *testing.T) {
	p := &spec.Package{Name: "x", Artifact: spec.Artifact{}}
	_, err := fetch.UpstreamURL(p, "v1", spec.Platform{OS: "linux", Arch: "amd64"})
	if err == nil {
		t.Fatal("want error for empty artifact")
	}
	if err.Error() != "artifact url is empty" {
		t.Errorf("got %q, want %q", err.Error(), "artifact url is empty")
	}
}

func TestUpstreamURLGitHubViaAssetName(t *testing.T) {
	p := &spec.Package{
		Name: "go",
		Artifact: spec.Artifact{GitHub: &spec.GitHubAsset{
			Repo:  "golang/go",
			Asset: "go{{.Version}}.{{.OS}}-{{.Arch}}.tar.gz",
		}},
	}
	got, err := fetch.UpstreamURL(p, "1.26.5", spec.Platform{OS: "linux", Arch: "amd64"})
	if err != nil {
		t.Fatalf("UpstreamURL: %v", err)
	}
	want := "https://github.com/golang/go/releases/download/1.26.5/go1.26.5.linux-amd64.tar.gz"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDownloadUnverifiedComputesSum(t *testing.T) {
	body := []byte("catalog artifact bytes")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	defer srv.Close()

	dir := t.TempDir()
	meta := fetch.Meta{Name: "jq", Version: "1.8.3", Platform: spec.Platform{OS: "linux", Arch: "amd64"}}
	path, sum, err := fetch.DownloadUnverified(context.Background(), srv.Client(), srv.URL, dir, meta, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256(body)
	if sum != hex.EncodeToString(want[:]) {
		t.Fatalf("sum = %s, want %x", sum, want)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("file content mismatch (err %v)", err)
	}
}

func TestDigestURLComputesSumWithoutFiles(t *testing.T) {
	body := []byte("catalog artifact bytes")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	defer srv.Close()

	meta := fetch.Meta{Name: "jq", Version: "1.8.3", Platform: spec.Platform{OS: "linux", Arch: "amd64"}}
	sum, err := fetch.DigestURL(context.Background(), srv.Client(), srv.URL, meta, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256(body)
	if sum != hex.EncodeToString(want[:]) {
		t.Fatalf("sum = %s, want %x", sum, want)
	}
}

func TestDigestURLNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	meta := fetch.Meta{Name: "jq", Version: "9.9.9", Platform: spec.Platform{OS: "linux", Arch: "amd64"}}
	_, err := fetch.DigestURL(context.Background(), srv.Client(), srv.URL, meta, nil)
	var nf *fetch.ArtifactNotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("err = %v, want ArtifactNotFoundError", err)
	}
	if !strings.Contains(err.Error(), srv.URL) {
		t.Fatalf("err = %v, want the failing URL in the message", err)
	}
}

func TestDownloadUnverifiedNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	meta := fetch.Meta{Name: "jq", Version: "9.9.9", Platform: spec.Platform{OS: "linux", Arch: "amd64"}}
	_, _, err := fetch.DownloadUnverified(context.Background(), srv.Client(), srv.URL, t.TempDir(), meta, nil)
	var nf *fetch.ArtifactNotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("err = %v, want ArtifactNotFoundError", err)
	}
	if !strings.Contains(err.Error(), srv.URL) {
		t.Fatalf("err = %v, want the failing URL in the message", err)
	}
}

func TestUpstreamURLPlainURL(t *testing.T) {
	p := &spec.Package{
		Name:     "go",
		Artifact: spec.Artifact{URL: "https://go.dev/dl/go{{.Version}}.{{.OS}}-{{.Arch}}.tar.gz"},
	}
	got, err := fetch.UpstreamURL(p, "1.26.5", spec.Platform{OS: "darwin", Arch: "arm64"})
	if err != nil {
		t.Fatalf("UpstreamURL: %v", err)
	}
	want := "https://go.dev/dl/go1.26.5.darwin-arm64.tar.gz"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
