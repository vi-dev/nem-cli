package publish

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"oras.land/oras-go/v2"

	"github.com/vi-dev/nem-cli/internal/report"
	"github.com/vi-dev/nem-cli/internal/spec"
)

// PublishCatalogForTest publishes pkgs (name -> pkg.yaml content) into
// target at ref via a real Publish call, failing the test on any error.
func PublishCatalogForTest(t testing.TB, target oras.Target, ref string, pkgs map[string]string) {
	t.Helper()
	dir := t.TempDir()
	for name, y := range pkgs {
		pkgDir := filepath.Join(dir, "pkgs", name)
		if err := os.MkdirAll(pkgDir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", pkgDir, err)
		}
		if err := os.WriteFile(filepath.Join(pkgDir, "pkg.yaml"), []byte(y), 0o644); err != nil {
			t.Fatalf("write pkg.yaml for %s: %v", name, err)
		}
	}

	restore := SetTargetOpener(func(context.Context, string) (oras.Target, error) { return target, nil })
	defer restore()
	if err := Publish(context.Background(), dir, ref, Options{}, report.Discard()); err != nil {
		t.Fatalf("publish test catalog to %s: %v", ref, err)
	}
}

// UniformSha256 pins hexDigest for every supported platform, keyed as URLPkgYAML expects.
func UniformSha256(hexDigest string) map[string]string {
	m := make(map[string]string, len(spec.Supported))
	for _, p := range spec.Supported {
		m[p.String()] = hexDigest
	}
	return m
}

// URLPkgYAML builds a url-fetched pkg.yaml; fill treats it as fillable.
func URLPkgYAML(name, version, urlTemplate string, sha256 map[string]string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "schema: 2\nname: %s\ndescription: test package\nartifact:\n  url: %q\ninstall:\n  - extract: {strip: 0}\nversions:\n  - version: %s\n    sha256:\n", name, urlTemplate, version)
	for _, p := range spec.Supported {
		fmt.Fprintf(&b, "      %s: %q\n", p, sha256[p.String()])
	}
	return []byte(b.String())
}

// OCIPkgYAML builds an oci-artifact pkg.yaml; fill must report it not-fillable.
func OCIPkgYAML(name, version string) []byte {
	return []byte(fmt.Sprintf("schema: 2\nname: %s\ndescription: test package\nartifact:\n  oci: \":{{.Version}}\"\ninstall:\n  - extract: {strip: 0}\nversions:\n  - version: %s\n", name, version))
}
