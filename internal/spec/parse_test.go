package spec

import (
	"strings"
	"testing"
)

const fullYAML = `
schema: 2
name: go
description: The Go programming language
platforms: [darwin/arm64, linux/amd64]
deps:
  - git
  - name: ncurses
    version: v6.5
    platforms: [linux]
versionDiscovery:
  github:
    repo: golang/go
    filter: '^go\d+\.\d+\.\d+$'
    prefix: "go"
artifact:
  url: "https://go.dev/dl/go{{.Version}}.{{.OS}}-{{.Arch}}.tar.gz"
install:
  - extract: {strip: 1}
  - copy: {src: "go/bin/gofmt", dst: "bin/gofmt", mode: 0o755}
  - move: {src: "go-dist", dst: "go"}
  - mkdir: "cache"
bins: ["bin", "go/bin"]
env:
  - name: GOROOT
    value: "{{.InstallDir}}/go"
versions:
  - version: v1.26.5
    sha256:
      darwin/arm64: "aaa"
      linux/amd64: "bbb"
    sourceSha256: "ccc"
`

func TestParseFull(t *testing.T) {
	p, err := Parse([]byte(fullYAML))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if p.Name != "go" || p.Schema != 2 {
		t.Fatalf("basic fields: %+v", p)
	}
	if len(p.Deps) != 2 || p.Deps[0].Name != "git" || p.Deps[1].Version != "v6.5" {
		t.Fatalf("deps: %+v", p.Deps)
	}
	if p.VersionDiscovery == nil || p.VersionDiscovery.GitHub.Prefix != "go" {
		t.Fatalf("discovery: %+v", p.VersionDiscovery)
	}
	if len(p.Install) != 4 || p.Install[0].Extract.Strip != 1 ||
		p.Install[1].Copy.Dst != "bin/gofmt" || p.Install[2].Move.Dst != "go" ||
		p.Install[3].Mkdir != "cache" {
		t.Fatalf("install: %+v", p.Install)
	}
	if p.Install[1].Copy.Mode != 0o755 {
		t.Fatalf("copy mode: got %o, want %o", p.Install[1].Copy.Mode, uint32(0o755))
	}
	if len(p.Versions) != 1 || p.Versions[0].Sha256["linux/amd64"] != "bbb" ||
		p.Versions[0].SourceSha256 != "ccc" {
		t.Fatalf("versions: %+v", p.Versions)
	}
}

func TestParseScalarForms(t *testing.T) {
	y := `
schema: 2
name: tiny
artifact:
  oci: ":{{.Version}}"
install:
  - extract: {}
versions:
  - v1.0.0
`
	p, err := Parse([]byte(y))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if p.Versions[0].Version != "v1.0.0" || p.Versions[0].Sha256 != nil {
		t.Fatalf("bare scalar entry: %+v", p.Versions[0])
	}
	if len(p.Bins) != 1 || p.Bins[0] != "bin" {
		t.Fatalf("bins default: %+v", p.Bins)
	}
}

func TestParseGitLabDiscovery(t *testing.T) {
	y := `
schema: 2
name: glab
artifact:
  oci: ":{{.Version}}"
install:
  - extract: {}
versions:
  - v1.0.0
versionDiscovery:
  gitlab:
    repo: gitlab-org/cli
    filter: '^v\d+\.\d+\.\d+$'
    prefix: "v"
    suffix: "-x"
`
	p, err := Parse([]byte(y))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	g := p.VersionDiscovery.GitLab
	if g == nil || g.Repo != "gitlab-org/cli" || g.Filter != `^v\d+\.\d+\.\d+$` ||
		g.Prefix != "v" || g.Suffix != "-x" {
		t.Fatalf("gitlab discovery: %+v", g)
	}
}

func TestParseGitDiscovery(t *testing.T) {
	y := `
schema: 2
name: tiny
artifact:
  oci: ":{{.Version}}"
install:
  - extract: {}
versions:
  - v1.0.0
versionDiscovery:
  git:
    url: https://gitlab.example.com/group/project.git
    filter: '^v\d+\.\d+\.\d+$'
    prefix: "v"
`
	p, err := Parse([]byte(y))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	g := p.VersionDiscovery.Git
	if g == nil || g.URL != "https://gitlab.example.com/group/project.git" ||
		g.Filter != `^v\d+\.\d+\.\d+$` || g.Prefix != "v" || g.Suffix != "" {
		t.Fatalf("git discovery: %+v", g)
	}
}

func TestParseRejectsScalarSha256(t *testing.T) {
	y := `
schema: 2
name: tiny
artifact:
  oci: ":{{.Version}}"
install:
  - extract: {}
versions:
  - version: v0.9.0
    sha256: "dead"
`
	_, err := Parse([]byte(y))
	if err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("want sha256 error for scalar sha256, got %v", err)
	}
}

func TestParseRejectsUnknownFields(t *testing.T) {
	_, err := Parse([]byte("schema: 2\nname: x\nbogus: 1\n"))
	if err == nil || !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("want unknown-field error, got %v", err)
	}
}

func TestParseRejectsUnknownNestedFields(t *testing.T) {
	depYAML := "schema: 2\nname: x\nartifact:\n  oci: \":{{.Version}}\"\ninstall:\n  - extract: {}\nversions: [v1.0.0]\ndeps:\n  - name: y\n    typo: true\n"
	if _, err := Parse([]byte(depYAML)); err == nil || !strings.Contains(err.Error(), "typo") {
		t.Fatalf("dep mapping unknown field: got %v", err)
	}
	actYAML := "schema: 2\nname: x\nartifact:\n  oci: \":{{.Version}}\"\ninstall:\n  - copy: {src: a, dst: b, typo: true}\nversions: [v1.0.0]\n"
	if _, err := Parse([]byte(actYAML)); err == nil || !strings.Contains(err.Error(), "typo") {
		t.Fatalf("action payload unknown field: got %v", err)
	}
}

func TestParseActionPlatforms(t *testing.T) {
	p, err := Parse([]byte(`
schema: 2
name: x
artifact: {oci: ":{{.Version}}"}
install:
  - extract: {strip: 1}
    platforms: [linux]
  - copy: {src: a, dst: b}
    platforms: [darwin/arm64]
  - mkdir: cache
    platforms: [linux/amd64, darwin]
  - move: {src: a, dst: b}
versions: [v1.0.0]
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(p.Install[0].Platforms) != 1 || p.Install[0].Platforms[0] != (Platform{OS: "linux"}) {
		t.Fatalf("extract platforms: %+v", p.Install[0].Platforms)
	}
	if len(p.Install[1].Platforms) != 1 || p.Install[1].Platforms[0] != (Platform{OS: "darwin", Arch: "arm64"}) {
		t.Fatalf("copy platforms: %+v", p.Install[1].Platforms)
	}
	if len(p.Install[2].Platforms) != 2 || p.Install[2].Mkdir != "cache" {
		t.Fatalf("mkdir action: %+v", p.Install[2])
	}
	if p.Install[3].Platforms != nil {
		t.Fatalf("unconstrained action platforms: %+v", p.Install[3].Platforms)
	}
}

func TestParseRejectsUnsupportedActionPlatform(t *testing.T) {
	y := "schema: 2\nname: x\nartifact: {oci: \":{{.Version}}\"}\ninstall:\n  - extract: {}\n    platforms: [windows]\nversions: [v1.0.0]\n"
	if _, err := Parse([]byte(y)); err == nil || !strings.Contains(err.Error(), "windows") {
		t.Fatalf("want unsupported-platform error, got %v", err)
	}
}

func TestParseRejectsActionKeyCount(t *testing.T) {
	onlyPlatforms := "schema: 2\nname: x\nartifact: {oci: \":{{.Version}}\"}\ninstall:\n  - platforms: [linux]\nversions: [v1.0.0]\n"
	if _, err := Parse([]byte(onlyPlatforms)); err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("platforms without action: got %v", err)
	}
	twoActions := "schema: 2\nname: x\nartifact: {oci: \":{{.Version}}\"}\ninstall:\n  - extract: {}\n    mkdir: cache\n    platforms: [linux]\nversions: [v1.0.0]\n"
	if _, err := Parse([]byte(twoActions)); err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("two action keys: got %v", err)
	}
}

func TestParseBuildStepPlatforms(t *testing.T) {
	p, err := Parse([]byte(`
schema: 2
name: x
artifact: {oci: ":{{.Version}}"}
install:
  - extract: {}
build:
  source: {url: "https://example.com/src.tar.gz"}
  steps:
    - run: make
      platforms: [linux]
    - run: make install
  output: out
versions: [v1.0.0]
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(p.Build.Steps) != 2 {
		t.Fatalf("steps: %+v", p.Build.Steps)
	}
	if len(p.Build.Steps[0].Platforms) != 1 || p.Build.Steps[0].Platforms[0] != (Platform{OS: "linux"}) {
		t.Fatalf("step platforms: %+v", p.Build.Steps[0])
	}
	if p.Build.Steps[1].Run != "make install" || p.Build.Steps[1].Platforms != nil {
		t.Fatalf("unconstrained step: %+v", p.Build.Steps[1])
	}
}

func TestParseLibsKindAndCompat(t *testing.T) {
	pkg, err := Parse([]byte(`
schema: 2
name: gpgme
libs: [lib]
deps:
  - name: gpg
  - name: openssl
    kind: link
    compat: "3"
artifact:
  oci: ":{{.Version}}"
install:
  - extract: {}
versions:
  - v1.0.0
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(pkg.Libs) != 1 || pkg.Libs[0] != "lib" {
		t.Fatalf("Libs = %v, want [lib]", pkg.Libs)
	}
	if pkg.Deps[0].Kind != DepKindRun {
		t.Fatalf("gpg dep kind = %q, want run (default)", pkg.Deps[0].Kind)
	}
	if pkg.Deps[1].Kind != DepKindLink || pkg.Deps[1].Compat != "3" {
		t.Fatalf("openssl dep = %+v, want kind=link compat=3", pkg.Deps[1])
	}
}

func TestParseRejectsUnknownDepKind(t *testing.T) {
	_, err := Parse([]byte(`
schema: 2
name: a
deps:
  - name: b
    kind: sideways
artifact: {oci: ":{{.Version}}"}
install: [{extract: {}}]
versions: [v1.0.0]
`))
	if err == nil || !strings.Contains(err.Error(), "sideways") {
		t.Fatalf("want error naming the bad kind, got %v", err)
	}
}

func TestParseBuildNormalize(t *testing.T) {
	src := `
schema: 2
name: x
artifact: {oci: ":{{.Version}}"}
install:
  - extract: {}
build:
  source: {url: "https://example.com/src.tar.gz"}
  steps:
    - run: make
  output: out
versions: [v1.0.0]
`
	p, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if p.Build.Normalize != nil {
		t.Fatalf("absent normalize must parse to nil, got %v", *p.Build.Normalize)
	}
	p, err = Parse([]byte(strings.Replace(src, "  output: out", "  output: out\n  normalize: false", 1)))
	if err != nil {
		t.Fatalf("Parse with normalize: %v", err)
	}
	if p.Build.Normalize == nil || *p.Build.Normalize {
		t.Fatalf("normalize: false must parse to *false, got %v", p.Build.Normalize)
	}
}
