package catalog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const dirPkgYAML = `
schema: 2
name: %NAME%
description: The %NAME% tool
artifact:
  oci: ":{{.Version}}"
install:
  - extract: {}
versions:
  - v2.0.0
  - v1.0.0
`

func writeDirCatalog(t *testing.T, names ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, n := range names {
		dir := filepath.Join(root, "pkgs", n)
		os.MkdirAll(dir, 0o755)
		y := []byte(strings.ReplaceAll(dirPkgYAML, "%NAME%", n))
		os.WriteFile(filepath.Join(dir, "pkg.yaml"), y, 0o644)
	}
	return root
}

func TestDirLoadAndVersions(t *testing.T) {
	root := writeDirCatalog(t, "go", "kubectl")
	d := NewDir(root)
	ctx := context.Background()

	pkg, dig, err := d.Load(ctx, "go")
	if err != nil || pkg.Name != "go" || dig != "" {
		t.Fatalf("Load: %+v, %q, %v", pkg, dig, err)
	}
	vs, err := d.Versions(ctx, "go")
	if err != nil || len(vs) != 2 || vs[0] != "v2.0.0" {
		t.Fatalf("Versions: %v, %v", vs, err)
	}

	var nf *PackageNotFoundError
	if _, _, err := d.Load(ctx, "absent"); !errors.As(err, &nf) {
		t.Fatalf("want PackageNotFoundError, got %v", err)
	}
}

func TestDirSummaries(t *testing.T) {
	root := writeDirCatalog(t, "zeta", "alpha")
	sums, err := NewDir(root).Summaries(context.Background())
	if err != nil {
		t.Fatalf("Summaries: %v", err)
	}
	if len(sums) != 2 || sums[0].Name != "alpha" || sums[0].Latest != "v2.0.0" ||
		sums[1].Name != "zeta" || sums[0].Description == "" {
		t.Fatalf("summaries: %+v", sums)
	}
}

func TestDirNameMismatchErrors(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "pkgs", "alias")
	os.MkdirAll(dir, 0o755)
	y := []byte(strings.ReplaceAll(dirPkgYAML, "%NAME%", "other"))
	os.WriteFile(filepath.Join(dir, "pkg.yaml"), y, 0o644)

	if _, _, err := NewDir(root).Load(context.Background(), "alias"); err == nil {
		t.Fatal("manifest name mismatch must error")
	}
}

func TestDirInvalidPkgErrors(t *testing.T) {
	root := writeDirCatalog(t, "ok")
	bad := filepath.Join(root, "pkgs", "bad")
	os.MkdirAll(bad, 0o755)
	os.WriteFile(filepath.Join(bad, "pkg.yaml"), []byte("schema: 1\nname: bad\n"), 0o644)
	if _, _, err := NewDir(root).Load(context.Background(), "bad"); err == nil {
		t.Fatal("invalid pkg.yaml must error")
	}
	// listings stay silent about the same package Load rejects
	sums, err := NewDir(root).Summaries(context.Background())
	if err != nil || len(sums) != 1 || sums[0].Name != "ok" {
		t.Fatalf("summaries: %+v, %v", sums, err)
	}
}
