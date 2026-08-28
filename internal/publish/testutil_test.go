package publish

import (
	"context"
	"testing"

	"oras.land/oras-go/v2/content/memory"

	"github.com/vi-dev/nem-cli/internal/ocix"
	"github.com/vi-dev/nem-cli/internal/spec"
)

func TestURLPkgYAMLLintsClean(t *testing.T) {
	yaml := URLPkgYAML("atool", "1.0.0", "https://example.test/{{.Version}}/{{.OS}}-{{.Arch}}.tar.gz", UniformSha256("aaa"))
	pkg, err := spec.Parse(yaml)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := pkg.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if pkg.Artifact.URL == "" || pkg.Artifact.OCI != "" || pkg.Artifact.GitHub != nil {
		t.Fatalf("artifact = %+v, want url-only", pkg.Artifact)
	}
}

func TestOCIPkgYAMLLintsClean(t *testing.T) {
	yaml := OCIPkgYAML("atool", "1.0.0")
	pkg, err := spec.Parse(yaml)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := pkg.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if pkg.Artifact.OCI == "" || pkg.Artifact.URL != "" || pkg.Artifact.GitHub != nil {
		t.Fatalf("artifact = %+v, want oci-only", pkg.Artifact)
	}
}

func TestPublishCatalogForTestPublishesBothArtifactKinds(t *testing.T) {
	target := memory.New()
	pkgs := map[string]string{
		"urltool": string(URLPkgYAML("urltool", "1.0.0", "https://example.test/{{.Version}}/{{.OS}}-{{.Arch}}.tar.gz", UniformSha256("aaa"))),
		"ocitool": string(OCIPkgYAML("ocitool", "1.0.0")),
	}
	PublishCatalogForTest(t, target, "example.test/cat", pkgs)

	idx, _, err := ocix.FetchCatalogIndex(context.Background(), target, "v2")
	if err != nil {
		t.Fatalf("FetchIndex: %v", err)
	}
	if len(idx.Manifests) != 2 {
		t.Fatalf("manifests = %d, want 2", len(idx.Manifests))
	}
}
