package spec

import (
	"strings"
	"testing"
)

func valid() *Package {
	return &Package{
		Schema: 2, Name: "go",
		Artifact: Artifact{URL: "https://x/{{.Version}}.tar.gz"},
		Install:  []Action{{Extract: &ExtractAction{}}},
		Bins:     []string{"bin"},
		Deps:     []Dep{{Name: "git"}},
		Versions: []VersionEntry{{
			Version: "v1.0.0",
			Sha256: map[string]string{
				"darwin/arm64": "a", "darwin/amd64": "b",
				"linux/arm64": "c", "linux/amd64": "d",
			},
		}},
	}
}

func TestValidateOK(t *testing.T) {
	if err := valid().Validate(); err != nil {
		t.Fatalf("valid package rejected: %v", err)
	}
}

func TestValidateRules(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Package)
		want   string
	}{
		{"schema", func(p *Package) { p.Schema = 1 }, "schema"},
		{"name", func(p *Package) { p.Name = "Bad" }, "name"},
		{"two fetchers", func(p *Package) { p.Artifact.OCI = ":{{.Version}}" }, "artifact"},
		{"no fetcher", func(p *Package) { p.Artifact = Artifact{} }, "artifact"},
		{"no install", func(p *Package) { p.Install = nil }, "install"},
		{"copy no src", func(p *Package) {
			p.Install = []Action{{Copy: &CopyAction{Dst: "bin/x"}}}
		}, "src"},
		{"move no dst", func(p *Package) {
			p.Install = []Action{{Move: &MoveAction{Src: "a"}}}
		}, "dst"},
		{"no versions", func(p *Package) { p.Versions = nil }, "versions"},
		{"bare scalar non-oci", func(p *Package) {
			p.Versions = []VersionEntry{{Version: "v1.0.0"}}
		}, "sha256"},
		{"incomplete sha256", func(p *Package) {
			p.Versions[0].Sha256 = map[string]string{"linux/amd64": "d"}
		}, "sha256"},
		{"bad env name", func(p *Package) {
			p.Env = []EnvExport{{Name: "1BAD", Value: "x"}}
		}, "env"},
		{"two discovery sources", func(p *Package) {
			p.VersionDiscovery = &Discovery{GitHub: &GitHubDiscovery{Repo: "a/b"}, OCI: "x"}
		}, "versionDiscovery"},
		{"bad filter", func(p *Package) {
			p.VersionDiscovery = &Discovery{GitHub: &GitHubDiscovery{Repo: "a/b", Filter: "("}}
		}, "filter"},
		{"bad dep name", func(p *Package) {
			p.Deps = []Dep{{Name: "Bad"}}
		}, "dep"},
		{"empty action", func(p *Package) {
			p.Install = []Action{{}}
		}, "empty action"},
		{"missing version", func(p *Package) {
			p.Versions = []VersionEntry{{Sha256: map[string]string{
				"darwin/arm64": "a", "darwin/amd64": "b",
				"linux/arm64": "c", "linux/amd64": "d",
			}}}
		}, "missing version"},
	}
	for _, c := range cases {
		p := valid()
		c.mutate(p)
		err := p.Validate()
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want mention of %q", c.name, err, c.want)
		}
	}
}

func TestValidateBareScalarOKForOCI(t *testing.T) {
	p := valid()
	p.Artifact = Artifact{OCI: ":{{.Version}}"}
	p.Versions = []VersionEntry{{Version: "v1.0.0"}}
	if err := p.Validate(); err != nil {
		t.Fatalf("bare scalar with oci artifact rejected: %v", err)
	}
}

func TestValidateSha256CoversDeclaredSubset(t *testing.T) {
	p := valid()
	p.Platforms = []Platform{{"linux", "amd64"}}
	p.Versions[0].Sha256 = map[string]string{"linux/amd64": "d"}
	if err := p.Validate(); err != nil {
		t.Fatalf("subset-complete sha256 rejected: %v", err)
	}
}
