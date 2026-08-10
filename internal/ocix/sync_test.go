package ocix

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"oras.land/oras-go/v2/content/oci"
)

func TestSyncAndLoad(t *testing.T) {
	ctx := context.Background()
	srcDir := t.TempDir()
	src, err := oci.New(srcDir)
	if err != nil {
		t.Fatalf("oci.New: %v", err)
	}
	goYAML := []byte("schema: 2\nname: go\n") // content is opaque to ocix; not parsed here
	kubectlYAML := []byte("schema: 2\nname: kubectl\n")
	PushFakeCatalogForTest(t, src, []FakeEntry{
		{Name: "go", Description: "The Go programming language", Latest: "v1.26.5", YAML: goYAML},
		{Name: "kubectl", Description: "Kubernetes CLI", Latest: "v1.34.1", YAML: kubectlYAML},
	}, "2")

	storePath := filepath.Join(t.TempDir(), "store")
	dig, err := SyncFrom(ctx, src, "v2", storePath)
	if err != nil {
		t.Fatalf("SyncFrom: %v", err)
	}
	if !strings.HasPrefix(dig, "sha256:") {
		t.Fatalf("index digest: %q", dig)
	}

	idx, err := LoadIndex(ctx, storePath)
	if err != nil || len(idx.Manifests) != 2 {
		t.Fatalf("LoadIndex: %+v, %v", idx, err)
	}

	data, mdig, err := LoadPkgBytes(ctx, storePath, "go")
	if err != nil || string(data) != string(goYAML) || !strings.HasPrefix(mdig, "sha256:") {
		t.Fatalf("LoadPkgBytes: %q, %q, %v", data, mdig, err)
	}

	var nf *PkgNotInIndexError
	if _, _, err := LoadPkgBytes(ctx, storePath, "absent"); !errors.As(err, &nf) {
		t.Fatalf("want PkgNotInIndexError, got %v", err)
	}
}

func TestSyncRejectsWrongSchema(t *testing.T) {
	ctx := context.Background()
	storePath := filepath.Join(t.TempDir(), "store")

	good, _ := oci.New(t.TempDir())
	PushFakeCatalogForTest(t, good, []FakeEntry{{Name: "go", YAML: []byte("x")}}, "2")
	if _, err := SyncFrom(ctx, good, "v2", storePath); err != nil {
		t.Fatalf("seed sync: %v", err)
	}

	bad, _ := oci.New(t.TempDir())
	PushFakeCatalogForTest(t, bad, []FakeEntry{{Name: "go", YAML: []byte("x")}}, "9")
	_, err := SyncFrom(ctx, bad, "v2", storePath)
	if err == nil || !strings.Contains(err.Error(), "schema") {
		t.Fatalf("want schema error, got %v", err)
	}

	idx, err := LoadIndex(ctx, storePath)
	if err != nil || len(idx.Manifests) != 1 {
		t.Fatalf("good mirror clobbered by bad-schema sync attempt: %+v, %v", idx, err)
	}
}

func TestLoadPkgBytesUnsynced(t *testing.T) {
	_, _, err := LoadPkgBytes(context.Background(), filepath.Join(t.TempDir(), "nope"), "go")
	if !errors.Is(err, ErrNotSynced) {
		t.Fatalf("want ErrNotSynced, got %v", err)
	}
}

func TestValidateRef(t *testing.T) {
	cases := []struct {
		ref string
		ok  bool
	}{
		{"ghcr.io/x/y", false},
		{"ghcr.io/x/y:v2", true},
		{"ghcr.io/x/y@sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", true},
		{"not a ref", false},
	}
	for _, c := range cases {
		err := ValidateRef(c.ref)
		if c.ok && err != nil {
			t.Errorf("ValidateRef(%q): unexpected error: %v", c.ref, err)
		}
		if !c.ok && err == nil {
			t.Errorf("ValidateRef(%q): want error, got nil", c.ref)
		}
	}
}

func TestSyncIsIdempotent(t *testing.T) {
	ctx := context.Background()
	src, _ := oci.New(t.TempDir())
	PushFakeCatalogForTest(t, src, []FakeEntry{{Name: "go", YAML: []byte("y")}}, "2")
	storePath := filepath.Join(t.TempDir(), "store")
	d1, err := SyncFrom(ctx, src, "v2", storePath)
	if err != nil {
		t.Fatal(err)
	}
	d2, err := SyncFrom(ctx, src, "v2", storePath)
	if err != nil || d1 != d2 {
		t.Fatalf("resync: %q vs %q, %v", d1, d2, err)
	}
}

func TestNewRemoteRepositoryPlainHTTP(t *testing.T) {
	cases := []struct {
		ref  string
		want bool
	}{
		{"localhost:5001/nem-local-catalog:v2", true},
		{"127.0.0.1:5000/cat:v2", true},
		{"[::1]:5000/cat:v2", true},
		{"ghcr.io/vi-dev/nem-official-catalog:v2", false},
		{"192.168.1.10:5000/cat:v2", false},
	}
	for _, c := range cases {
		repo, err := NewRemoteRepository(c.ref)
		if err != nil {
			t.Fatalf("NewRemoteRepository(%q): %v", c.ref, err)
		}
		if repo.PlainHTTP != c.want {
			t.Errorf("NewRemoteRepository(%q).PlainHTTP = %v, want %v", c.ref, repo.PlainHTTP, c.want)
		}
	}
}
