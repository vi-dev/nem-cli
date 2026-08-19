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
		{"version with plus", func(p *Package) {
			p.Versions[0].Version = "25.0.4+7"
		}, "oci tag"},
		{"version leading separator", func(p *Package) {
			p.Versions[0].Version = "-1.0.0"
		}, "oci tag"},
		{"version too long", func(p *Package) {
			p.Versions[0].Version = strings.Repeat("9", 129)
		}, "oci tag"},
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

func TestValidateVersionTagSpellings(t *testing.T) {
	for _, v := range []string{"v1.0.0", "25.0.4_7", "2024.05.01-rc.1", "_1"} {
		p := valid()
		p.Versions[0].Version = v
		if err := p.Validate(); err != nil {
			t.Errorf("version %q rejected: %v", v, err)
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

func TestValidateCompatRequiresLinkKind(t *testing.T) {
	pkg, err := Parse([]byte(`
schema: 2
name: a
deps:
  - name: b
    compat: "3"
artifact: {oci: ":{{.Version}}"}
install: [{extract: {}}]
versions: [v1.0.0]
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if err := pkg.Validate(); err == nil || !strings.Contains(err.Error(), "compat requires kind: link") {
		t.Fatalf("want compat-requires-link error, got %v", err)
	}
}

func TestValidateBuildSection(t *testing.T) {
	base := func(b *Build) *Package {
		return &Package{
			Schema: 2, Name: "a",
			Artifact: Artifact{OCI: ":{{.Version}}"},
			Install:  []Action{{Extract: &ExtractAction{}}},
			Versions: []VersionEntry{{Version: "v1.0.0"}},
			Build:    b,
		}
	}
	good := &Build{Output: "dist", Steps: []BuildStep{{Run: "make"}}}
	good.Source.URL = "https://ex/{{.Version}}.tgz"
	good.Deps = []Dep{{Name: "openssl", Kind: DepKindLink, Compat: "3"}}
	if err := base(good).Validate(); err != nil {
		t.Fatalf("valid build rejected: %v", err)
	}
	for name, mut := range map[string]func(*Build){
		"empty source":        func(b *Build) { b.Source.URL = "" },
		"empty output":        func(b *Build) { b.Output = "" },
		"no steps":            func(b *Build) { b.Steps = nil },
		"empty run":           func(b *Build) { b.Steps = []BuildStep{{Run: ""}} },
		"compat no link":      func(b *Build) { b.Deps = []Dep{{Name: "x", Compat: "3"}} },
		"unrenderable source": func(b *Build) { b.Source.URL = "{{.Bogus}}" },
		"bad dep name":        func(b *Build) { b.Deps = []Dep{{Name: "Bad", Kind: DepKindLink}} },
		"bad compat format":   func(b *Build) { b.Deps = []Dep{{Name: "x", Kind: DepKindLink, Compat: "abc"}} },
	} {
		b := &Build{Output: "dist", Deps: good.Deps, Steps: []BuildStep{{Run: "make"}}}
		b.Source.URL = "https://ex/{{.Version}}.tgz"
		mut(b)
		if err := base(b).Validate(); err == nil {
			t.Errorf("%s: want validation error", name)
		}
	}
}

func TestValidateCompatFormat(t *testing.T) {
	pkg, err := Parse([]byte(`
schema: 2
name: a
deps:
  - name: b
    kind: link
    compat: "3.x"
artifact: {oci: ":{{.Version}}"}
install: [{extract: {}}]
versions: [v1.0.0]
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if err := pkg.Validate(); err == nil || !strings.Contains(err.Error(), "invalid compat") {
		t.Fatalf("want invalid-compat error, got %v", err)
	}
}

func TestValidateActionTemplates(t *testing.T) {
	p := valid()
	p.Install = append(p.Install, Action{Copy: &CopyAction{Src: "{{.Bogus}}", Dst: "bin/x"}})
	if err := p.Validate(); err == nil {
		t.Fatal("want error for unknown template field in copy src")
	}
	p = valid()
	p.Install = append(p.Install, Action{Mkdir: "{{.OS"})
	if err := p.Validate(); err == nil {
		t.Fatal("want error for malformed template in mkdir")
	}
	p = valid()
	p.Install = append(p.Install, Action{Copy: &CopyAction{Src: "tool-{{.Arch}}", Dst: "bin/tool"}})
	if err := p.Validate(); err != nil {
		t.Fatalf("templated copy rejected: %v", err)
	}
}
