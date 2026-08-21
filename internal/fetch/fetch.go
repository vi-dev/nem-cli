// Package fetch downloads pinned package artifacts over http, verifying
// their sha256 as they stream to disk, and builds the URLs nem's url/github
// fetchers resolve to.
package fetch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/vi-dev/nem-cli/internal/home"
	"github.com/vi-dev/nem-cli/internal/report"
	"github.com/vi-dev/nem-cli/internal/spec"
)

// Meta carries the package identity used to build error messages and the
// downloaded temp file's name.
type Meta struct {
	Name, Version string
	Platform      spec.Platform
}

// progressChunk is how many bytes accumulate between periodic Progress
// calls during a download.
const progressChunk = 256 * 1024

// Download fetches url into dir with a fresh temp name, streaming the
// response body through sha256 as it writes. On checksum mismatch the temp
// file is deleted and a ChecksumMismatchError is returned; on success the
// temp file's path is returned for the caller to move or consume — the
// caller owns its cleanup. A 404 or 410 response is reported as
// ArtifactNotFoundError{Missing: "url"}; any other non-200 status is
// wrapped with the response status. task may be nil; when non-nil, its
// Progress is fed (done, total) as the body downloads, total being -1 when
// the response carries no Content-Length.
func Download(ctx context.Context, client *http.Client, url, wantSHA256, dir string, meta Meta, task report.Task) (string, error) {
	path, got, err := DownloadUnverified(ctx, client, url, dir, meta, task)
	if err != nil {
		return "", err
	}
	if got != wantSHA256 {
		os.Remove(path)
		return "", &ChecksumMismatchError{Name: meta.Name, Version: meta.Version, Platform: meta.Platform.String(), Got: got, Want: wantSHA256}
	}
	return path, nil
}

// DownloadUnverified streams url's body into a fresh temp file in dir,
// hashing as it goes, without comparing against any expected digest. It
// returns the temp file's path and the computed hex digest, for callers
// that pin checksums rather than verify them.
func DownloadUnverified(ctx context.Context, client *http.Client, url, dir string, meta Meta, task report.Task) (string, string, error) {
	resp, err := get(ctx, client, url, meta)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	f, err := os.CreateTemp(dir, meta.Name+"-"+meta.Version+"-*"+home.TmpSuffix)
	if err != nil {
		return "", "", fmt.Errorf("create temp file in %s: %w", dir, err)
	}
	tmpPath := f.Name()

	sum, copyErr := digest(resp, f, task)
	closeErr := f.Close()
	if copyErr != nil {
		os.Remove(tmpPath)
		return "", "", fmt.Errorf("download %s: %w", url, copyErr)
	}
	if closeErr != nil {
		os.Remove(tmpPath)
		return "", "", fmt.Errorf("close temp file %s: %w", tmpPath, closeErr)
	}
	return tmpPath, sum, nil
}

// DigestURL streams url's body through sha256 without persisting it,
// returning the hex digest — for callers that only need to pin a
// checksum, not keep the bytes.
func DigestURL(ctx context.Context, client *http.Client, url string, meta Meta, task report.Task) (string, error) {
	resp, err := get(ctx, client, url, meta)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	sum, err := digest(resp, io.Discard, task)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", url, err)
	}
	return sum, nil
}

// get issues the artifact GET, mapping 404/410 to ArtifactNotFoundError
// and any other non-200 status to an error. The caller owns resp.Body.
func get(ctx context.Context, client *http.Client, url string, meta Meta) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request for %s: %w", url, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", url, err)
	}
	switch resp.StatusCode {
	case http.StatusOK:
		return resp, nil
	case http.StatusNotFound, http.StatusGone:
		resp.Body.Close()
		return nil, &ArtifactNotFoundError{Name: meta.Name, Version: meta.Version, Platform: meta.Platform.String(), Missing: "url", URL: url}
	default:
		resp.Body.Close()
		return nil, fmt.Errorf("fetch %s: unexpected status %s", url, resp.Status)
	}
}

// digest copies resp's body to w while hashing it, feeding task progress
// when task is non-nil, and returns the hex digest.
func digest(resp *http.Response, w io.Writer, task report.Task) (string, error) {
	sum := sha256.New()
	dst := io.MultiWriter(w, sum)
	if task != nil {
		dst = io.MultiWriter(w, sum, newProgressWriter(task, resp.ContentLength))
	}
	written, err := io.Copy(dst, resp.Body)
	if task != nil {
		task.Progress(written, resp.ContentLength)
	}
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

// progressWriter reports download progress to a report.Task every
// progressChunk bytes written.
type progressWriter struct {
	task     report.Task
	total    int64
	written  int64
	reported int64
}

func newProgressWriter(task report.Task, total int64) *progressWriter {
	return &progressWriter{task: task, total: total}
}

func (w *progressWriter) Write(p []byte) (int, error) {
	n := len(p)
	w.written += int64(n)
	if w.written-w.reported >= progressChunk {
		w.reported = w.written
		w.task.Progress(w.written, w.total)
	}
	return n, nil
}

// GitHubURL builds the public release-asset URL for repo/version/asset,
// without touching the GitHub API.
func GitHubURL(repo, version, asset string) string {
	return fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", repo, version, asset)
}

// UpstreamURL resolves pkg's url or github artifact fetcher to the concrete
// URL to download for (version, plat). It errors when the rendered URL is
// empty, which also covers a package whose artifact is neither url nor
// github (e.g. oci, which this package does not fetch).
func UpstreamURL(pkg *spec.Package, version string, plat spec.Platform) (string, error) {
	var url string
	switch {
	case pkg.Artifact.GitHub != nil:
		asset, err := pkg.AssetName(version, plat)
		if err != nil {
			return "", err
		}
		url = GitHubURL(pkg.Artifact.GitHub.Repo, version, asset)
	case pkg.Artifact.URL != "":
		u, err := pkg.ArtifactURL(version, plat)
		if err != nil {
			return "", err
		}
		url = u
	}
	if url == "" {
		return "", errors.New("artifact url is empty")
	}
	return url, nil
}
