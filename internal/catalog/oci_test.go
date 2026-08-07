package catalog

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"oras.land/oras-go/v2/content/oci"

	"github.com/vi-dev/nem-cli/internal/ocix"
)

const ociGoYAML = `
schema: 2
name: go
description: The Go programming language
artifact:
  oci: ":{{.Version}}"
install:
  - extract: {}
versions:
  - v1.26.5
  - v1.26.4
`

const ociMismatchYAML = `
schema: 2
name: other
description: manifest name does not match the requested alias
artifact:
  oci: ":{{.Version}}"
install:
  - extract: {}
versions:
  - v1.0.0
`

func syncedStore(t *testing.T) string {
	t.Helper()
	src, err := oci.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ocix.PushFakeCatalogForTest(t, src, []ocix.FakeEntry{{
		Name: "go", Description: "The Go programming language", Latest: "v1.26.5",
		YAML: []byte(ociGoYAML),
	}}, "2")
	storePath := filepath.Join(t.TempDir(), "store")
	if _, err := ocix.SyncFrom(context.Background(), src, "v2", storePath); err != nil {
		t.Fatal(err)
	}
	return storePath
}

func TestOCISourceLoadAndVersions(t *testing.T) {
	s := NewOCI("official", syncedStore(t))
	ctx := context.Background()
	pkg, dig, err := s.Load(ctx, "go")
	if err != nil || pkg.Name != "go" || dig == "" {
		t.Fatalf("Load: %+v, %q, %v", pkg, dig, err)
	}
	pkg2, _, _ := s.Load(ctx, "go")
	if pkg2 != pkg {
		t.Fatal("memoization by digest broken: distinct pointers")
	}
	vs, err := s.Versions(ctx, "go")
	if err != nil || len(vs) != 2 || vs[0] != "v1.26.5" {
		t.Fatalf("Versions: %v, %v", vs, err)
	}
	var nf *PackageNotFoundError
	if _, _, err := s.Load(ctx, "absent"); !errors.As(err, &nf) {
		t.Fatalf("want PackageNotFoundError, got %v", err)
	}
}

func TestOCISourceSummariesFromIndexOnly(t *testing.T) {
	sums, err := NewOCI("official", syncedStore(t)).Summaries(context.Background())
	if err != nil || len(sums) != 1 {
		t.Fatalf("Summaries: %+v, %v", sums, err)
	}
	if sums[0].Name != "go" || sums[0].Latest != "v1.26.5" || sums[0].Description == "" {
		t.Fatalf("summary: %+v", sums[0])
	}
}

func TestOCINameMismatchErrors(t *testing.T) {
	ctx := context.Background()
	src, err := oci.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ocix.PushFakeCatalogForTest(t, src, []ocix.FakeEntry{{
		Name: "alias", Description: "mismatch", Latest: "v1.0.0",
		YAML: []byte(ociMismatchYAML),
	}}, "2")
	storePath := filepath.Join(t.TempDir(), "store")
	if _, err := ocix.SyncFrom(ctx, src, "v2", storePath); err != nil {
		t.Fatal(err)
	}

	if _, _, err := NewOCI("official", storePath).Load(ctx, "alias"); err == nil {
		t.Fatal("manifest name mismatch must error")
	}
}

func TestOCISourceUnsynced(t *testing.T) {
	s := NewOCI("official", filepath.Join(t.TempDir(), "missing"))
	if _, err := s.Summaries(context.Background()); !errors.Is(err, ocix.ErrNotSynced) {
		t.Fatalf("want ErrNotSynced, got %v", err)
	}
}
