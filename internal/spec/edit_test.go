package spec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mustFormat(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "format", name))
	if err != nil {
		t.Fatal(err)
	}
	canon, err := Format(data)
	if err != nil {
		t.Fatal(err)
	}
	return canon
}

func TestInsertVersionSha256Map(t *testing.T) {
	canon := mustFormat(t, "prebuilt.yaml")
	entry := VersionEntry{Version: "1.8.3", Sha256: map[string]string{
		"darwin/arm64": "aa11", "darwin/amd64": "bb22",
		"linux/arm64": "cc33", "linux/amd64": "dd44",
	}}
	out, err := InsertVersion(canon, entry)
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := Parse(out)
	if err != nil {
		t.Fatalf("edited manifest no longer parses: %v", err)
	}
	if pkg.Versions[0].Version != "1.8.3" {
		t.Fatalf("versions[0] = %q, want 1.8.3", pkg.Versions[0].Version)
	}
	if pkg.Versions[0].Sha256["linux/amd64"] != "dd44" {
		t.Fatalf("sha256 map = %v", pkg.Versions[0].Sha256)
	}
	if pkg.Versions[1].Version != "1.8.2" {
		t.Fatal("existing entry lost")
	}
	// Surgical diff: on canonical input, output = input + inserted lines.
	added := diffAddedLines(t, canon, out)
	for _, l := range added {
		if !strings.Contains(l, "1.8.3") && !strings.Contains(l, "sha256") &&
			!strings.Contains(l, "arm64") && !strings.Contains(l, "amd64") {
			t.Errorf("unexpected added line %q", l)
		}
	}
	if len(added) != 6 { // "- version" + "sha256:" + 4 platforms
		t.Errorf("added %d lines, want 6: %q", len(added), added)
	}
}

func TestInsertVersionSourceSha256(t *testing.T) {
	canon := mustFormat(t, "source-commented.yaml")
	out, err := InsertVersion(canon, VersionEntry{Version: "v3.4.2", SourceSha256: "ee55"})
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Versions[0].Version != "v3.4.2" || pkg.Versions[0].SourceSha256 != "ee55" {
		t.Fatalf("versions[0] = %+v", pkg.Versions[0])
	}
	if !strings.Contains(string(out), "keep both libs consistent") {
		t.Fatal("comment lost by InsertVersion")
	}
}

func TestInsertVersionBareScalar(t *testing.T) {
	yamlIn := []byte(`schema: 2
name: tool
artifact:
  oci: ":{{.Version}}"
install:
  - extract: {strip: 0}
versions:
  - v1.0.0
`)
	canon, err := Format(yamlIn)
	if err != nil {
		t.Fatal(err)
	}
	out, err := InsertVersion(canon, VersionEntry{Version: "v1.1.0"})
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Versions[0].Version != "v1.1.0" || pkg.Versions[0].Sha256 != nil {
		t.Fatalf("versions[0] = %+v, want bare v1.1.0", pkg.Versions[0])
	}
}

func TestInsertVersionAtMiddle(t *testing.T) {
	canon := mustFormat(t, "prebuilt.yaml")
	head, err := InsertVersion(canon, VersionEntry{Version: "1.8.4", Sha256: map[string]string{
		"darwin/arm64": "a1", "darwin/amd64": "a2", "linux/arm64": "a3", "linux/amd64": "a4",
	}})
	if err != nil {
		t.Fatal(err)
	}
	// Now [1.8.4, 1.8.2]; insert 1.8.3 between them.
	out, err := InsertVersionAt(head, VersionEntry{Version: "1.8.3", Sha256: map[string]string{
		"darwin/arm64": "b1", "darwin/amd64": "b2", "linux/arm64": "b3", "linux/amd64": "b4",
	}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := Parse(out)
	if err != nil {
		t.Fatalf("edited manifest no longer parses: %v", err)
	}
	want := []string{"1.8.4", "1.8.3", "1.8.2"}
	for i, v := range want {
		if pkg.Versions[i].Version != v {
			t.Fatalf("versions[%d] = %q, want %q", i, pkg.Versions[i].Version, v)
		}
	}
	added := diffAddedLines(t, head, out)
	if len(added) != 6 {
		t.Errorf("added %d lines, want 6: %q", len(added), added)
	}
}

func TestInsertVersionAtEnd(t *testing.T) {
	canon := mustFormat(t, "source-commented.yaml")
	out, err := InsertVersionAt(canon, VersionEntry{Version: "v3.4.0", SourceSha256: "ff00"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkg.Versions) != 2 || pkg.Versions[0].Version != "v3.4.1" || pkg.Versions[1].Version != "v3.4.0" {
		t.Fatalf("versions = %+v, want [v3.4.1 v3.4.0]", pkg.Versions)
	}
	if !strings.Contains(string(out), "keep both libs consistent") {
		t.Fatal("comment lost by InsertVersionAt")
	}
	diffAddedLines(t, canon, out) // asserts every original line survives in order
}

const flowVersionsFixture = `schema: 2
name: tool
artifact:
  oci: ":{{.Version}}"
install:
  - extract: {strip: 0}
versions: ["1.0.0"]
`

func TestInsertVersionAtRejectsFlowSequence(t *testing.T) {
	_, err := InsertVersionAt([]byte(flowVersionsFixture), VersionEntry{Version: "2.0.0"}, 0)
	if err == nil || !strings.Contains(err.Error(), "block sequence") {
		t.Fatalf("err = %v, want block-sequence rejection", err)
	}
}

func TestValidateEditable(t *testing.T) {
	if err := ValidateEditable([]byte(flowVersionsFixture)); err == nil {
		t.Fatal("want error for flow-style versions")
	}
	canon := mustFormat(t, "prebuilt.yaml")
	if err := ValidateEditable(canon); err != nil {
		t.Fatalf("block-style manifest must be editable: %v", err)
	}
}

func TestInsertVersionAtOutOfRange(t *testing.T) {
	canon := mustFormat(t, "prebuilt.yaml")
	if _, err := InsertVersionAt(canon, VersionEntry{Version: "9.9.9"}, 5); err == nil {
		t.Fatal("want error for out-of-range position")
	}
	if _, err := InsertVersionAt(canon, VersionEntry{Version: "9.9.9"}, -1); err == nil {
		t.Fatal("want error for negative position")
	}
}

// diffAddedLines returns lines present in after but not before, assuming
// before's lines all survive in order (the surgical-diff property).
func diffAddedLines(t *testing.T, before, after []byte) []string {
	t.Helper()
	b := strings.Split(string(before), "\n")
	a := strings.Split(string(after), "\n")
	var added []string
	i := 0
	for _, line := range a {
		if i < len(b) && line == b[i] {
			i++
			continue
		}
		added = append(added, line)
	}
	if i != len(b) {
		t.Fatalf("original line %q missing from edited output", b[i])
	}
	return added
}
