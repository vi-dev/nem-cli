// Package selfupdate replaces the running nem binary with a release build
// downloaded from GitHub.
package selfupdate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/vi-dev/nem-cli/internal/archive"
	"github.com/vi-dev/nem-cli/internal/fetch"
	"github.com/vi-dev/nem-cli/internal/netx"
	"github.com/vi-dev/nem-cli/internal/report"
	"github.com/vi-dev/nem-cli/internal/spec"
)

// maxAPIBody caps how much of a metadata response is read; real bodies are
// a few KB.
const maxAPIBody = 1 << 20

// Updater downloads nem release builds from GitHub. APIBase exists so tests
// can point resolution at a local server; production uses NewUpdater's
// defaults.
type Updater struct {
	Client       *http.Client
	APIBase      string
	DownloadBase string
	Repo         string
	Token        string
}

// Update downloads the release build for version, verifies its sha256
// against the release's checksums.txt, smoke-runs the new binary, and
// atomically renames it over exePath. task may be nil; when non-nil it
// receives download progress.
func (u *Updater) Update(ctx context.Context, version, exePath string, task report.Task) error {
	// The staging file doubles as the writability preflight: it lives next
	// to exePath so the final rename is atomic (same filesystem), and
	// creating it fails before any download when the directory is read-only.
	installDir := filepath.Dir(exePath)
	stage, err := os.CreateTemp(installDir, ".nem-update-*")
	if err != nil {
		return fmt.Errorf("install directory %s is not writable; re-run with permission to modify it (e.g. sudo): %w", installDir, err)
	}
	stagePath := stage.Name()
	stage.Close()
	defer os.Remove(stagePath)

	tmpDir, err := os.MkdirTemp("", "nem-selfupdate-")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	checksums, err := u.fetchChecksums(ctx, version)
	if err != nil {
		return err
	}
	asset := assetName(version, runtime.GOOS, runtime.GOARCH)
	wantSHA, err := parseChecksums(checksums, asset)
	if err != nil {
		return fmt.Errorf("release %s: %w", version, err)
	}

	meta := fetch.Meta{Name: "nem", Version: version, Platform: spec.Platform{OS: runtime.GOOS, Arch: runtime.GOARCH}}
	archive, err := fetch.Download(ctx, u.Client, u.assetURL(version, asset), wantSHA, tmpDir, meta, task)
	if err != nil {
		return err
	}

	if err := extractBinary(archive, stagePath); err != nil {
		return err
	}
	if out, err := exec.CommandContext(ctx, stagePath, "version").CombinedOutput(); err != nil {
		return fmt.Errorf("downloaded nem %s failed to run (%v): %s", version, err, bytes.TrimSpace(out))
	}

	if err := os.Rename(stagePath, exePath); err != nil {
		return fmt.Errorf("replace %s: %w", exePath, err)
	}
	return nil
}

func (u *Updater) assetURL(version, name string) string {
	return u.DownloadBase + "/" + version + "/" + name
}

func (u *Updater) fetchChecksums(ctx context.Context, version string) ([]byte, error) {
	url := u.assetURL(version, "checksums.txt")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request for %s: %w", url, err)
	}
	resp, err := u.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: unexpected status %s", url, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAPIBody))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", url, err)
	}
	return data, nil
}

// NewUpdater returns an Updater pointed at the real nem release repository.
// token may be empty; when set it authenticates GitHub API calls.
func NewUpdater(token string) *Updater {
	const repo = "vi-dev/nem-cli"
	return &Updater{
		Client:       netx.Client(),
		APIBase:      "https://api.github.com",
		DownloadBase: "https://github.com/" + repo + "/releases/download",
		Repo:         repo,
		Token:        token,
	}
}

// ExecutablePath resolves the running binary's path through any symlinks,
// so the update replaces the real file rather than a link to it.
func ExecutablePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate running binary: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", exe, err)
	}
	return resolved, nil
}

// ResolveLatest returns the tag of the newest stable release. Token, when
// set, authenticates the API call — unauthenticated GitHub API access is
// tightly rate-limited per IP.
func (u *Updater) ResolveLatest(ctx context.Context) (string, error) {
	url := fmt.Sprintf("%s/repos/%s/releases/latest", u.APIBase, u.Repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("build request for %s: %w", url, err)
	}
	if u.Token != "" {
		req.Header.Set("Authorization", "Bearer "+u.Token)
	}
	resp, err := u.Client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetch %s: unexpected status %s", url, resp.Status)
	}

	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxAPIBody)).Decode(&release); err != nil {
		return "", fmt.Errorf("decode release metadata from %s: %w", url, err)
	}
	if release.TagName == "" {
		return "", fmt.Errorf("release metadata from %s has no tag_name", url)
	}
	return release.TagName, nil
}

// assetName builds the release-archive file name for version on goos/goarch.
// Release assets embed the version verbatim — the tag for a release, the
// literal "unstable" for the rolling main build — so one formula covers both.
func assetName(version, goos, goarch string) string {
	return fmt.Sprintf("nem_%s_%s_%s.tar.gz", version, goos, goarch)
}

// parseChecksums finds filename's hex digest in a GoReleaser checksums.txt
// body ("<sha256>  <filename>" lines).
// extractBinary pulls the nem binary out of the release archive at
// archivePath and writes it, executable, to destPath. GoReleaser may place
// the binary at the archive root or inside a directory named after the
// archive; the extracted tree is searched for the base name "nem".
func extractBinary(archivePath, destPath string) error {
	dir, err := os.MkdirTemp("", "nem-update-*")
	if err != nil {
		return fmt.Errorf("create extraction dir: %w", err)
	}
	defer os.RemoveAll(dir)
	root, err := os.OpenRoot(dir)
	if err != nil {
		return fmt.Errorf("open extraction dir: %w", err)
	}
	defer root.Close()
	if _, err := archive.Extract(archivePath, root, archive.Options{SingleName: "nem"}); err != nil {
		return fmt.Errorf("extract release archive: %w", err)
	}

	var src string
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && d.Name() == "nem" {
			src = path
			return fs.SkipAll
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("search release archive: %w", err)
	}
	if src == "" {
		return errors.New("no nem binary found in release archive")
	}

	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open extracted binary: %w", err)
	}
	defer in.Close()
	out, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return fmt.Errorf("create %s: %w", destPath, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return fmt.Errorf("write %s: %w", destPath, err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close %s: %w", destPath, err)
	}
	// OpenFile's mode only applies when it creates the file; an existing
	// destination (the pre-created staging file) keeps its old mode.
	if err := os.Chmod(destPath, 0o755); err != nil {
		return fmt.Errorf("chmod %s: %w", destPath, err)
	}
	return nil
}

func parseChecksums(data []byte, filename string) (string, error) {
	for line := range strings.Lines(string(data)) {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == filename {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("no checksum entry for %s", filename)
}
