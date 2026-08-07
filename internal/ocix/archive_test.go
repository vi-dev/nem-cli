package ocix

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/vi-dev/nem-cli/internal/spec"
	"oras.land/oras-go/v2/content/oci"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/registry/remote/errcode"
)

func TestPullArchiveFromReturnsRightPlatform(t *testing.T) {
	ctx := context.Background()
	store, err := oci.New(t.TempDir())
	if err != nil {
		t.Fatalf("oci.New: %v", err)
	}
	linuxBytes := []byte("linux/amd64 archive payload")
	darwinBytes := []byte("darwin/arm64 archive payload")
	PushFakeArchive(t, store, "v1.26.5", map[string][]byte{
		"linux/amd64":  linuxBytes,
		"darwin/arm64": darwinBytes,
	})

	dir := t.TempDir()
	path, err := PullArchiveFrom(ctx, store, "v1.26.5", spec.Platform{OS: "linux", Arch: "amd64"}, dir)
	if err != nil {
		t.Fatalf("PullArchiveFrom: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read pulled archive: %v", err)
	}
	if string(got) != string(linuxBytes) {
		t.Fatalf("archive bytes = %q, want %q", got, linuxBytes)
	}
	if filepath.Dir(path) != dir {
		t.Fatalf("archive written to %q, want under %q", path, dir)
	}

	path2, err := PullArchiveFrom(ctx, store, "v1.26.5", spec.Platform{OS: "darwin", Arch: "arm64"}, dir)
	if err != nil {
		t.Fatalf("PullArchiveFrom (darwin): %v", err)
	}
	got2, err := os.ReadFile(path2)
	if err != nil {
		t.Fatalf("read pulled archive: %v", err)
	}
	if string(got2) != string(darwinBytes) {
		t.Fatalf("archive bytes = %q, want %q", got2, darwinBytes)
	}
}

func TestPullArchiveFromMissingTag(t *testing.T) {
	ctx := context.Background()
	store, err := oci.New(t.TempDir())
	if err != nil {
		t.Fatalf("oci.New: %v", err)
	}
	PushFakeArchive(t, store, "v1.26.5", map[string][]byte{"linux/amd64": []byte("x")})

	_, err = PullArchiveFrom(ctx, store, "v9.9.9", spec.Platform{OS: "linux", Arch: "amd64"}, t.TempDir())
	if !errors.Is(err, ErrArchiveNotFound) {
		t.Fatalf("want ErrArchiveNotFound, got %v", err)
	}
}

func TestPullArchiveFromMissingPlatform(t *testing.T) {
	ctx := context.Background()
	store, err := oci.New(t.TempDir())
	if err != nil {
		t.Fatalf("oci.New: %v", err)
	}
	PushFakeArchive(t, store, "v1.26.5", map[string][]byte{"linux/amd64": []byte("x")})

	_, err = PullArchiveFrom(ctx, store, "v1.26.5", spec.Platform{OS: "darwin", Arch: "arm64"}, t.TempDir())
	if !errors.Is(err, ErrArchiveNotFound) {
		t.Fatalf("want ErrArchiveNotFound, got %v", err)
	}
}

// resolveErrTarget wraps a real store but forces Resolve to fail with a
// non-not-found error, standing in for an auth or network failure.
type resolveErrTarget struct {
	*oci.Store
	err error
}

func (f resolveErrTarget) Resolve(ctx context.Context, ref string) (ocispec.Descriptor, error) {
	return ocispec.Descriptor{}, f.err
}

func TestPullArchiveFromNonNotFoundErrorPassesThrough(t *testing.T) {
	ctx := context.Background()
	store, err := oci.New(t.TempDir())
	if err != nil {
		t.Fatalf("oci.New: %v", err)
	}
	PushFakeArchive(t, store, "v1.26.5", map[string][]byte{"linux/amd64": []byte("x")})

	authErr := errors.New("401 unauthorized")
	faulty := resolveErrTarget{Store: store, err: authErr}

	_, err = PullArchiveFrom(ctx, faulty, "v1.26.5", spec.Platform{OS: "linux", Arch: "amd64"}, t.TempDir())
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if errors.Is(err, ErrArchiveNotFound) {
		t.Fatalf("auth/network error must not map to ErrArchiveNotFound: %v", err)
	}
	if !errors.Is(err, authErr) {
		t.Fatalf("want wrapped authErr, got %v", err)
	}
}

func TestArchiveAbsent(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"errdef not found", errdef.ErrNotFound, true},
		{"403 forbidden", &errcode.ErrorResponse{StatusCode: 403}, true},
		{"404 not found", &errcode.ErrorResponse{StatusCode: 404}, true},
		{"401 unauthorized", &errcode.ErrorResponse{StatusCode: 401}, false},
		{"500 internal error", &errcode.ErrorResponse{StatusCode: 500}, false},
		{"plain error", errors.New("x"), false},
		{"wrapped 403", fmt.Errorf("ctx: %w", &errcode.ErrorResponse{StatusCode: 403}), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := archiveAbsent(c.err); got != c.want {
				t.Fatalf("archiveAbsent(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}

func TestPullArchiveFromForbiddenMapsToArchiveNotFound(t *testing.T) {
	ctx := context.Background()
	store, err := oci.New(t.TempDir())
	if err != nil {
		t.Fatalf("oci.New: %v", err)
	}
	PushFakeArchive(t, store, "v1.26.5", map[string][]byte{"linux/amd64": []byte("x")})

	faulty := resolveErrTarget{Store: store, err: &errcode.ErrorResponse{StatusCode: 403}}

	_, err = PullArchiveFrom(ctx, faulty, "v1.26.5", spec.Platform{OS: "linux", Arch: "amd64"}, t.TempDir())
	if !errors.Is(err, ErrArchiveNotFound) {
		t.Fatalf("want ErrArchiveNotFound for a 403 resolve error, got %v", err)
	}
}

func TestArchivesRef(t *testing.T) {
	cases := []struct {
		name       string
		catalogRef string
		pkgName    string
		want       string
		wantErr    bool
	}{
		{
			name:       "with tag",
			catalogRef: "ghcr.io/org/cat:v2",
			pkgName:    "go",
			want:       "ghcr.io/org/cat/archives/go",
		},
		{
			name:       "with digest",
			catalogRef: "ghcr.io/org/cat@sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			pkgName:    "go",
			want:       "ghcr.io/org/cat/archives/go",
		},
		{
			name:       "no slash",
			catalogRef: "cat:v2",
			pkgName:    "go",
			wantErr:    true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ArchivesRef(c.catalogRef, c.pkgName)
			if c.wantErr {
				if err == nil {
					t.Fatalf("ArchivesRef(%q): want error, got nil", c.catalogRef)
				}
				return
			}
			if err != nil {
				t.Fatalf("ArchivesRef(%q): unexpected error: %v", c.catalogRef, err)
			}
			if got != c.want {
				t.Fatalf("ArchivesRef(%q) = %q, want %q", c.catalogRef, got, c.want)
			}
		})
	}
}
