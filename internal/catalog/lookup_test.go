package catalog

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/vi-dev/nem-cli/internal/project"
)

func TestLookupFirstMatchWins(t *testing.T) {
	a := writeDirCatalog(t, "shared", "only-a") // helper from dir_test.go
	b := writeDirCatalog(t, "shared", "only-b")
	sources := []Named{{Name: "a", Source: NewDir(a)}, {Name: "b", Source: NewDir(b)}}
	ctx := context.Background()

	_, cat, _, err := Lookup(ctx, sources, project.ToolKey{Name: "shared"})
	if err != nil || cat != "a" {
		t.Fatalf("first-match: %q, %v", cat, err)
	}
	_, cat, _, err = Lookup(ctx, sources, project.ToolKey{Name: "only-b"})
	if err != nil || cat != "b" {
		t.Fatalf("fallthrough: %q, %v", cat, err)
	}
	var nf *PackageNotFoundError
	_, _, _, err = Lookup(ctx, sources, project.ToolKey{Name: "nowhere"})
	if !errors.As(err, &nf) || len(nf.Catalogs) != 2 {
		t.Fatalf("exhausted: %v", err)
	}
}

func TestLookupPinned(t *testing.T) {
	a := writeDirCatalog(t, "shared")
	b := writeDirCatalog(t, "shared", "only-b")
	sources := []Named{{Name: "a", Source: NewDir(a)}, {Name: "b", Source: NewDir(b)}}
	ctx := context.Background()

	_, cat, _, err := Lookup(ctx, sources, project.ToolKey{Catalog: "b", Name: "shared"})
	if err != nil || cat != "b" {
		t.Fatalf("pin honored: %q, %v", cat, err)
	}
	var cnf *CatalogNotFoundError
	if _, _, _, err := Lookup(ctx, sources, project.ToolKey{Catalog: "zzz", Name: "shared"}); !errors.As(err, &cnf) {
		t.Fatalf("want CatalogNotFoundError, got %v", err)
	}
	var nf *PackageNotFoundError
	if _, _, _, err := Lookup(ctx, sources, project.ToolKey{Catalog: "a", Name: "only-b"}); !errors.As(err, &nf) {
		t.Fatalf("pinned miss must not fall through: %v", err)
	}
}

func TestOpenBuildsSourcesInOrder(t *testing.T) {
	h := testHome(t)
	cfg := &Config{Catalogs: []Entry{
		{Name: "dev", Type: "dir", Path: t.TempDir()},
		{Name: "official", Type: "oci", Ref: OfficialRef},
	}}
	named, err := Open(cfg, h)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if len(named) != 2 || named[0].Name != "dev" || named[1].Name != "official" {
		t.Fatalf("Open: %+v", named)
	}
	if _, ok := named[0].Source.(*Dir); !ok {
		t.Fatal("dev should be a Dir source")
	}
	if _, ok := named[1].Source.(*OCI); !ok {
		t.Fatal("official should be an OCI source")
	}
}

func TestOpenSkipsDisabled(t *testing.T) {
	cfg := &Config{Catalogs: []Entry{
		{Name: "a", Type: "dir", Path: "/tmp/a"},
		{Name: "b", Type: "dir", Path: "/tmp/b", Disabled: true},
		{Name: "c", Type: "dir", Path: "/tmp/c"},
	}}
	named, err := Open(cfg, testHome(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	got := make([]string, len(named))
	for i, n := range named {
		got[i] = n.Name
	}
	if !slices.Equal(got, []string{"a", "c"}) {
		t.Fatalf("Open should skip disabled and preserve order, got %v", got)
	}
}
