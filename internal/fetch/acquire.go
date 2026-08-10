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
	"strings"
	"text/template"

	"github.com/vi-dev/nem-cli/internal/ocix"
	"github.com/vi-dev/nem-cli/internal/report"
	"github.com/vi-dev/nem-cli/internal/spec"
	"oras.land/oras-go/v2/registry"
)

// Source tells Acquire where the package came from.
type Source struct {
	CatalogRef string // OCI catalog ref; "" for dir catalogs
}

// httpClient is the client Acquire's upstream downloads use; overridable in
// tests.
var httpClient = http.DefaultClient

// pullArchive opens catalogRef's archives repo for name and pulls tag/plat
// from it into dir; overridden in tests.
var pullArchive = func(ctx context.Context, catalogRef, name, tag string, plat spec.Platform, dir string) (string, error) {
	repo, err := ocix.RemoteArchives(catalogRef, name)
	if err != nil {
		return "", err
	}
	return ocix.PullArchiveFrom(ctx, repo, tag, plat, dir)
}

// SetPullArchiveForTest swaps the pullArchive package var Acquire's
// registry-first fetch path uses and returns a closure that restores the
// previous implementation. Test-only: it lets a caller outside this
// package stub the registry pull without a real OCI registry.
func SetPullArchiveForTest(f func(ctx context.Context, catalogRef, name, tag string, plat spec.Platform, dir string) (string, error)) (restore func()) {
	prev := pullArchive
	pullArchive = f
	return func() { pullArchive = prev }
}

// remoteByRef opens the repository named by an absolute oci ref and pulls
// plat's archive from it into dir; overridden in tests.
var remoteByRef = func(ctx context.Context, ref string, plat spec.Platform, dir string) (string, error) {
	parsed, err := registry.ParseReference(ref)
	if err != nil {
		return "", fmt.Errorf("parse oci ref %q: %w", ref, err)
	}
	repo, err := ocix.NewRemoteRepository(parsed.Registry + "/" + parsed.Repository)
	if err != nil {
		return "", err
	}
	return ocix.PullArchiveFrom(ctx, repo, parsed.ReferenceOrDefault(), plat, dir)
}

// Acquire returns a verified artifact file path for (pkg, version,
// platform). Order: (1) pkg.Artifact.OCI != "" fetches from the registry
// only — a relative ref resolves against src.CatalogRef's archives repo, an
// absolute ref is used as-is; digests are verified by transport, so no
// pinned sha256 is needed, and a not-found is a plain error with no
// fallback. (2) a url/github artifact with src.CatalogRef != "" tries the
// catalog's archives repo first, falling back to the upstream fetcher only
// on ocix.ErrArchiveNotFound; any other pull error aborts without
// fallback. (3) a dir catalog (src.CatalogRef == "") fetches upstream only.
// In (2) and (3) the pinned sha256 verifies whichever source served the
// bytes — Download verifies upstream bytes itself; a registry pull is
// re-hashed with VerifyFile before acceptance.
func Acquire(ctx context.Context, pkg *spec.Package, version string, plat spec.Platform, src Source, dir string, task report.Task) (string, error) {
	if pkg.Artifact.OCI != "" {
		return acquireOCI(ctx, pkg, version, plat, src, dir)
	}

	meta := Meta{Name: pkg.Name, Version: version, Platform: plat}
	sha, err := pkg.Sha256(version, plat)
	if err != nil {
		return "", err
	}

	if src.CatalogRef != "" {
		path, err := pullArchive(ctx, src.CatalogRef, pkg.Name, version, plat, dir)
		switch {
		case err == nil:
			if verr := VerifyFile(path, sha, meta); verr != nil {
				os.Remove(path)
				return "", verr
			}
			return path, nil
		case errors.Is(err, ocix.ErrArchiveNotFound):
			// fall through to the upstream fetcher
		default:
			return "", err
		}
	}

	url, err := UpstreamURL(pkg, version, plat)
	if err != nil {
		return "", err
	}
	return Download(ctx, httpClient, url, sha, dir, meta, task)
}

// acquireOCI resolves and pulls pkg's oci artifact, per Acquire's order (1).
func acquireOCI(ctx context.Context, pkg *spec.Package, version string, plat spec.Platform, src Source, dir string) (string, error) {
	ref, err := templateOCIRef(pkg.Artifact.OCI, version, plat)
	if err != nil {
		return "", err
	}
	if isRelativeOCIRef(ref) {
		if src.CatalogRef == "" {
			return "", errors.New("relative oci ref requires an oci catalog")
		}
		return pullArchive(ctx, src.CatalogRef, pkg.Name, ociRefTag(ref), plat, dir)
	}
	return remoteByRef(ctx, ref, plat, dir)
}

// ociTemplateCtx is the template context for pkg.Artifact.OCI, matching
// spec's artifact template contexts.
type ociTemplateCtx struct {
	Version, OS, Arch string
}

var ociHelperFuncs = template.FuncMap{
	"trimPrefix": func(s, prefix string) string { return strings.TrimPrefix(s, prefix) },
	"trimSuffix": func(s, suffix string) string { return strings.TrimSuffix(s, suffix) },
	"replace":    func(s, old, new string) string { return strings.ReplaceAll(s, old, new) },
}

// templateOCIRef expands pkg.Artifact.OCI's {{.Version}}/{{.OS}}/{{.Arch}}
// placeholders.
func templateOCIRef(tmpl, version string, plat spec.Platform) (string, error) {
	t, err := template.New("").Funcs(ociHelperFuncs).Option("missingkey=error").Parse(tmpl)
	if err != nil {
		return "", fmt.Errorf("parse oci ref template %q: %w", tmpl, err)
	}
	var b strings.Builder
	if err := t.Execute(&b, ociTemplateCtx{Version: version, OS: plat.OS, Arch: plat.Arch}); err != nil {
		return "", fmt.Errorf("expand oci ref template %q: %w", tmpl, err)
	}
	return b.String(), nil
}

// isRelativeOCIRef reports whether a templated oci ref is relative (a bare
// tag or digest, resolved against the catalog's archives repo) rather than
// an absolute registry/repository ref.
func isRelativeOCIRef(ref string) bool {
	return ref == "" || strings.HasPrefix(ref, ":") || strings.HasPrefix(ref, "@")
}

// ociRefTag strips a relative oci ref's leading ':' or '@' to the bare
// tag/digest PullArchiveFrom resolves.
func ociRefTag(ref string) string {
	if ref == "" {
		return ref
	}
	return ref[1:]
}

// VerifyFile hashes the file at path and compares it against wantSHA256
// (hex), returning a ChecksumMismatchError on mismatch.
func VerifyFile(path, wantSHA256 string, meta Meta) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	sum := sha256.New()
	if _, err := io.Copy(sum, f); err != nil {
		return fmt.Errorf("hash %s: %w", path, err)
	}
	got := hex.EncodeToString(sum.Sum(nil))
	if got != wantSHA256 {
		return &ChecksumMismatchError{Name: meta.Name, Version: meta.Version, Platform: meta.Platform.String(), Got: got, Want: wantSHA256}
	}
	return nil
}
