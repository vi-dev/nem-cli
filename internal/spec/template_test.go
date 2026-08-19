package spec

import (
	"strings"
	"testing"
)

func TestArtifactURL(t *testing.T) {
	p := valid() // from validate_test.go
	p.Artifact.URL = "https://go.dev/dl/go{{.Version}}.{{.OS}}-{{.Arch}}.tar.gz"
	got, err := p.ArtifactURL("1.26.5", Platform{"darwin", "arm64"})
	if err != nil {
		t.Fatalf("ArtifactURL: %v", err)
	}
	want := "https://go.dev/dl/go1.26.5.darwin-arm64.tar.gz"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestTemplateHelpers(t *testing.T) {
	p := valid()
	p.Artifact.URL = `https://x/{{trimPrefix .Version "v"}}/{{replace .OS "darwin" "macos"}}`
	got, err := p.ArtifactURL("v1.0.0", Platform{"darwin", "arm64"})
	if err != nil {
		t.Fatalf("ArtifactURL: %v", err)
	}
	if got != "https://x/1.0.0/macos" {
		t.Errorf("got %q", got)
	}
}

func TestTemplateUnknownKeyErrors(t *testing.T) {
	p := valid()
	p.Artifact.URL = "https://x/{{.Bogus}}"
	if _, err := p.ArtifactURL("v1", Platform{"linux", "amd64"}); err == nil {
		t.Fatal("want error for unknown template key")
	}
}

func TestAssetName(t *testing.T) {
	p := valid()
	p.Artifact = Artifact{GitHub: &GitHubAsset{Repo: "golang/go", Asset: "go{{.Version}}.{{.OS}}-{{.Arch}}.tar.gz"}}
	got, err := p.AssetName("1.26.5", Platform{"linux", "amd64"})
	if err != nil || got != "go1.26.5.linux-amd64.tar.gz" {
		t.Fatalf("AssetName: %q, %v", got, err)
	}
	p2 := valid() // url artifact, no github
	if _, err := p2.AssetName("v1", Platform{"linux", "amd64"}); err == nil {
		t.Fatal("want error when package has no github artifact")
	}
}

func TestBuildSourceURL(t *testing.T) {
	pkg := &Package{Build: &Build{}}
	pkg.Build.Source.URL = "https://ex.com/src-{{.Version}}.tar.gz"
	got, err := pkg.BuildSourceURL("v1.2.3", Platform{OS: "darwin", Arch: "arm64"})
	if err != nil {
		t.Fatalf("BuildSourceURL: %v", err)
	}
	if got != "https://ex.com/src-v1.2.3.tar.gz" {
		t.Fatalf("got %q", got)
	}
	if _, err := (&Package{}).BuildSourceURL("v1", Platform{}); err == nil {
		t.Fatal("want error when Build is nil")
	}
}

func TestSha256Lookup(t *testing.T) {
	p := valid()
	got, err := p.Sha256("v1.0.0", Platform{"linux", "amd64"})
	if err != nil || got != "d" {
		t.Fatalf("Sha256: %q, %v", got, err)
	}
	if _, err := p.Sha256("v9.9.9", Platform{"linux", "amd64"}); err == nil ||
		!strings.Contains(err.Error(), "v9.9.9") {
		t.Fatalf("missing version: %v", err)
	}
	// Test missing platform in sha256 map
	p.Versions[0].Sha256 = map[string]string{"linux/amd64": "d"}
	if _, err := p.Sha256("v1.0.0", Platform{"darwin", "arm64"}); err == nil ||
		!strings.Contains(err.Error(), "darwin/arm64") {
		t.Fatalf("missing platform: %v", err)
	}
}

func TestExpandActionPaths(t *testing.T) {
	plat := Platform{"linux", "arm64"}
	copyA := Action{Copy: &CopyAction{Src: "bin-{{.OS}}/tool-{{.Arch}}", Dst: "bin/{{replace .Arch \"arm64\" \"aa\"}}", Mode: 0o755}}
	got, err := ExpandActionPaths(copyA, "1.2.3", plat)
	if err != nil {
		t.Fatalf("copy: %v", err)
	}
	if got.Copy.Src != "bin-linux/tool-arm64" || got.Copy.Dst != "bin/aa" || got.Copy.Mode != 0o755 {
		t.Fatalf("copy expanded = %+v", got.Copy)
	}
	if copyA.Copy.Src != "bin-{{.OS}}/tool-{{.Arch}}" {
		t.Fatalf("original mutated: %+v", copyA.Copy)
	}

	move := Action{Move: &MoveAction{Src: "pkg-{{.Version}}/x", Dst: "y"}}
	if got, err = ExpandActionPaths(move, "1.2.3", plat); err != nil || got.Move.Src != "pkg-1.2.3/x" {
		t.Fatalf("move: %+v, %v", got.Move, err)
	}

	mk := Action{Mkdir: "cache/{{.OS}}"}
	if got, err = ExpandActionPaths(mk, "1.2.3", plat); err != nil || got.Mkdir != "cache/linux" {
		t.Fatalf("mkdir: %q, %v", got.Mkdir, err)
	}

	// The artifact token is itself template syntax and must survive
	// expansion verbatim for the runner's literal check.
	art := Action{Copy: &CopyAction{Src: ArtifactToken, Dst: "bin/t"}}
	if got, err = ExpandActionPaths(art, "1.2.3", plat); err != nil || got.Copy.Src != ArtifactToken {
		t.Fatalf("artifact token: %+v, %v", got.Copy, err)
	}

	bad := Action{Copy: &CopyAction{Src: "{{.Bogus}}", Dst: "d"}}
	if _, err = ExpandActionPaths(bad, "1.2.3", plat); err == nil {
		t.Fatal("want error for unknown template field")
	}
}
