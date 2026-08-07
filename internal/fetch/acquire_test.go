package fetch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/vi-dev/nem-cli/internal/ocix"
	"github.com/vi-dev/nem-cli/internal/spec"
)

var testPlat = spec.Platform{OS: "linux", Arch: "amd64"}

func urlPkg(url, sha string) *spec.Package {
	return &spec.Package{
		Name:     "go",
		Artifact: spec.Artifact{URL: url},
		Versions: []spec.VersionEntry{{
			Version: "v1.2.3",
			Sha256:  map[string]string{testPlat.String(): sha},
		}},
	}
}

func ociPkg(ociRef string) *spec.Package {
	return &spec.Package{
		Name:     "go",
		Artifact: spec.Artifact{OCI: ociRef},
		Versions: []spec.VersionEntry{{Version: "v1.2.3"}},
	}
}

// withPullArchive overrides the pullArchive package var for the duration of
// t, restoring it afterward.
func withPullArchive(t *testing.T, fn func(ctx context.Context, catalogRef, name, tag string, plat spec.Platform, dir string) (string, error)) {
	t.Helper()
	orig := pullArchive
	pullArchive = fn
	t.Cleanup(func() { pullArchive = orig })
}

// hitCountingServer returns an httptest server that counts requests and a
// pointer to the count.
func hitCountingServer(t *testing.T, body []byte) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func TestAcquireRegistryHitSkipsUpstreamButVerifiesSha256(t *testing.T) {
	body := []byte("upstream bytes, never fetched")
	srv, hits := hitCountingServer(t, body)

	wrongBytes := []byte("wrong archive bytes from registry")
	dir := t.TempDir()

	withPullArchive(t, func(ctx context.Context, catalogRef, name, tag string, plat spec.Platform, dir2 string) (string, error) {
		f, err := os.CreateTemp(dir2, "fake-archive-*.tmp")
		if err != nil {
			t.Fatalf("CreateTemp: %v", err)
		}
		if _, err := f.Write(wrongBytes); err != nil {
			t.Fatalf("write fake archive: %v", err)
		}
		f.Close()
		return f.Name(), nil
	})

	pkg := urlPkg(srv.URL, strings.Repeat("a", 64))
	src := Source{CatalogRef: "ghcr.io/org/cat:v2"}

	_, err := Acquire(context.Background(), pkg, "v1.2.3", testPlat, src, dir, nil)
	var cme *ChecksumMismatchError
	if !errors.As(err, &cme) {
		t.Fatalf("want ChecksumMismatchError, got %v", err)
	}
	if hits.Load() != 0 {
		t.Errorf("upstream hit %d times, want 0 (registry hit must skip upstream)", hits.Load())
	}
}

func TestAcquireArchiveNotFoundFallsBackToUpstream(t *testing.T) {
	body := []byte("upstream fallback bytes")
	sum := sha256.Sum256(body)
	wantSHA := hex.EncodeToString(sum[:])
	srv, hits := hitCountingServer(t, body)

	withPullArchive(t, func(ctx context.Context, catalogRef, name, tag string, plat spec.Platform, dir string) (string, error) {
		return "", ocix.ErrArchiveNotFound
	})

	pkg := urlPkg(srv.URL, wantSHA)
	src := Source{CatalogRef: "ghcr.io/org/cat:v2"}
	dir := t.TempDir()

	path, err := Acquire(context.Background(), pkg, "v1.2.3", testPlat, src, dir, nil)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer os.Remove(path)

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != string(body) {
		t.Errorf("downloaded bytes = %q, want %q", got, body)
	}
	if hits.Load() != 1 {
		t.Errorf("upstream hit %d times, want 1", hits.Load())
	}
}

func TestAcquireNonNotFoundPullErrorAbortsWithoutFallback(t *testing.T) {
	body := []byte("must never be fetched")
	srv, hits := hitCountingServer(t, body)

	pullErr := errors.New("401 unauthorized")
	withPullArchive(t, func(ctx context.Context, catalogRef, name, tag string, plat spec.Platform, dir string) (string, error) {
		return "", pullErr
	})

	pkg := urlPkg(srv.URL, strings.Repeat("a", 64))
	src := Source{CatalogRef: "ghcr.io/org/cat:v2"}
	dir := t.TempDir()

	_, err := Acquire(context.Background(), pkg, "v1.2.3", testPlat, src, dir, nil)
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if !errors.Is(err, pullErr) {
		t.Errorf("want wrapped pullErr, got %v", err)
	}
	if hits.Load() != 0 {
		t.Errorf("upstream hit %d times, want 0 (non-not-found pull error must not fall back)", hits.Load())
	}
}

func TestAcquireOCIArtifactPullSuccess(t *testing.T) {
	dir := t.TempDir()
	archivePath := dir + "/prewritten-archive"
	if err := os.WriteFile(archivePath, []byte("archive bytes"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	var gotCatalogRef, gotName, gotTag string
	withPullArchive(t, func(ctx context.Context, catalogRef, name, tag string, plat spec.Platform, dir string) (string, error) {
		gotCatalogRef, gotName, gotTag = catalogRef, name, tag
		return archivePath, nil
	})

	pkg := ociPkg(":{{.Version}}")
	src := Source{CatalogRef: "ghcr.io/org/cat:v2"}

	path, err := Acquire(context.Background(), pkg, "v1.2.3", testPlat, src, dir, nil)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if path != archivePath {
		t.Errorf("path = %q, want %q", path, archivePath)
	}
	if gotCatalogRef != "ghcr.io/org/cat:v2" {
		t.Errorf("catalogRef = %q", gotCatalogRef)
	}
	if gotName != "go" {
		t.Errorf("name = %q", gotName)
	}
	if gotTag != "v1.2.3" {
		t.Errorf("tag = %q, want %q", gotTag, "v1.2.3")
	}
}

func TestAcquireOCIArtifactPullNotFoundErrorsWithoutFallback(t *testing.T) {
	withPullArchive(t, func(ctx context.Context, catalogRef, name, tag string, plat spec.Platform, dir string) (string, error) {
		return "", ocix.ErrArchiveNotFound
	})

	pkg := ociPkg(":{{.Version}}")
	src := Source{CatalogRef: "ghcr.io/org/cat:v2"}
	dir := t.TempDir()

	_, err := Acquire(context.Background(), pkg, "v1.2.3", testPlat, src, dir, nil)
	if !errors.Is(err, ocix.ErrArchiveNotFound) {
		t.Fatalf("want ErrArchiveNotFound, got %v", err)
	}
}

func TestAcquireDirCatalogGoesStraightUpstream(t *testing.T) {
	body := []byte("dir catalog upstream bytes")
	sum := sha256.Sum256(body)
	wantSHA := hex.EncodeToString(sum[:])
	srv, hits := hitCountingServer(t, body)

	withPullArchive(t, func(ctx context.Context, catalogRef, name, tag string, plat spec.Platform, dir string) (string, error) {
		t.Fatal("pullArchive must not be called for a dir catalog")
		return "", nil
	})

	pkg := urlPkg(srv.URL, wantSHA)
	src := Source{CatalogRef: ""}
	dir := t.TempDir()

	path, err := Acquire(context.Background(), pkg, "v1.2.3", testPlat, src, dir, nil)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer os.Remove(path)

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != string(body) {
		t.Errorf("downloaded bytes = %q, want %q", got, body)
	}
	if hits.Load() != 1 {
		t.Errorf("upstream hit %d times, want 1", hits.Load())
	}
}

func TestAcquireRegistrySuccessAcceptsWithoutUpstream(t *testing.T) {
	body := []byte("must never be fetched")
	srv, hits := hitCountingServer(t, body)

	archiveBytes := []byte("correct archive bytes from registry")
	sum := sha256.Sum256(archiveBytes)
	wantSHA := hex.EncodeToString(sum[:])
	dir := t.TempDir()

	withPullArchive(t, func(ctx context.Context, catalogRef, name, tag string, plat spec.Platform, dir2 string) (string, error) {
		f, err := os.CreateTemp(dir2, "fake-archive-*.tmp")
		if err != nil {
			t.Fatalf("CreateTemp: %v", err)
		}
		if _, err := f.Write(archiveBytes); err != nil {
			t.Fatalf("write fake archive: %v", err)
		}
		f.Close()
		return f.Name(), nil
	})

	pkg := urlPkg(srv.URL, wantSHA)
	src := Source{CatalogRef: "ghcr.io/org/cat:v2"}

	path, err := Acquire(context.Background(), pkg, "v1.2.3", testPlat, src, dir, nil)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != string(archiveBytes) {
		t.Errorf("bytes = %q, want %q", got, archiveBytes)
	}
	if hits.Load() != 0 {
		t.Errorf("upstream hit %d times, want 0 (registry success must not fall back)", hits.Load())
	}
}

func TestAcquireMissingSha256Errors(t *testing.T) {
	pkg := &spec.Package{
		Name:     "go",
		Artifact: spec.Artifact{URL: "https://example.invalid/x"},
		Versions: []spec.VersionEntry{{Version: "v1.2.3", Sha256: map[string]string{}}},
	}
	dir := t.TempDir()

	_, err := Acquire(context.Background(), pkg, "v1.2.3", testPlat, Source{}, dir, nil)
	if err == nil {
		t.Fatal("want error for missing pinned sha256")
	}
}

func TestAcquireAbsoluteOCIRefUsesRemoteByRef(t *testing.T) {
	dir := t.TempDir()
	archivePath := dir + "/prewritten-absolute"
	if err := os.WriteFile(archivePath, []byte("absolute ref archive"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	origRemoteByRef := remoteByRef
	t.Cleanup(func() { remoteByRef = origRemoteByRef })
	var gotRef string
	remoteByRef = func(ctx context.Context, ref string, plat spec.Platform, dir string) (string, error) {
		gotRef = ref
		return archivePath, nil
	}
	withPullArchive(t, func(ctx context.Context, catalogRef, name, tag string, plat spec.Platform, dir string) (string, error) {
		t.Fatal("pullArchive must not be called for an absolute oci ref")
		return "", nil
	})

	pkg := ociPkg("ghcr.io/other/repo:{{.Version}}")
	path, err := Acquire(context.Background(), pkg, "v1.2.3", testPlat, Source{}, dir, nil)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if path != archivePath {
		t.Errorf("path = %q, want %q", path, archivePath)
	}
	if gotRef != "ghcr.io/other/repo:v1.2.3" {
		t.Errorf("ref = %q", gotRef)
	}
}

func TestAcquireEmptyTemplatedOCIRefIsRelative(t *testing.T) {
	withPullArchive(t, func(ctx context.Context, catalogRef, name, tag string, plat spec.Platform, dir string) (string, error) {
		if tag != "" {
			t.Errorf("tag = %q, want empty", tag)
		}
		return "/fake/path", nil
	})

	pkg := ociPkg("{{if false}}x{{end}}")
	src := Source{CatalogRef: "ghcr.io/org/cat:v2"}
	dir := t.TempDir()

	path, err := Acquire(context.Background(), pkg, "v1.2.3", testPlat, src, dir, nil)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if path != "/fake/path" {
		t.Errorf("path = %q", path)
	}
}

func TestAcquireOCIRefTemplateErrorPropagates(t *testing.T) {
	pkg := ociPkg(":{{.Bogus}}")
	src := Source{CatalogRef: "ghcr.io/org/cat:v2"}
	dir := t.TempDir()

	_, err := Acquire(context.Background(), pkg, "v1.2.3", testPlat, src, dir, nil)
	if err == nil {
		t.Fatal("want error for unknown template key")
	}
}

func TestVerifyFileOpenErrorWraps(t *testing.T) {
	meta := Meta{Name: "go", Version: "v1.2.3", Platform: testPlat}
	err := VerifyFile("/nonexistent/path/for/test", strings.Repeat("a", 64), meta)
	if err == nil {
		t.Fatal("want error for nonexistent file")
	}
	var cme *ChecksumMismatchError
	if errors.As(err, &cme) {
		t.Fatal("open failure must not be reported as ChecksumMismatchError")
	}
}

func TestAcquireRelativeOCIRefWithoutCatalogErrors(t *testing.T) {
	pkg := ociPkg(":{{.Version}}")
	src := Source{CatalogRef: ""}
	dir := t.TempDir()

	_, err := Acquire(context.Background(), pkg, "v1.2.3", testPlat, src, dir, nil)
	if err == nil {
		t.Fatal("want error for relative oci ref without catalog")
	}
	want := "relative oci ref requires an oci catalog"
	if err.Error() != want {
		t.Errorf("got %q, want %q", err.Error(), want)
	}
}

func TestVerifyFileMatch(t *testing.T) {
	dir := t.TempDir()
	body := []byte("verify me")
	sum := sha256.Sum256(body)
	wantSHA := hex.EncodeToString(sum[:])

	path := dir + "/f"
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	meta := Meta{Name: "go", Version: "v1.2.3", Platform: testPlat}
	if err := VerifyFile(path, wantSHA, meta); err != nil {
		t.Fatalf("VerifyFile: %v", err)
	}
}

func TestVerifyFileMismatch(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/f"
	if err := os.WriteFile(path, []byte("actual bytes"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	meta := Meta{Name: "go", Version: "v1.2.3", Platform: testPlat}
	err := VerifyFile(path, strings.Repeat("a", 64), meta)
	var cme *ChecksumMismatchError
	if !errors.As(err, &cme) {
		t.Fatalf("want ChecksumMismatchError, got %v", err)
	}
	if cme.Name != "go" || cme.Version != "v1.2.3" {
		t.Errorf("Name/Version = %q/%q", cme.Name, cme.Version)
	}
}
