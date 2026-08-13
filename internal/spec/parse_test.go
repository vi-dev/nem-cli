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
