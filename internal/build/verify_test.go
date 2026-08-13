package build

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestVerifyConformance(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("loader semantics differ off darwin/linux")
	}
	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("cc not available")
	}
	dir := t.TempDir()
	forbidden := filepath.Join(dir, "forbidden")
	if err := os.MkdirAll(forbidden, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "m.c")
	os.WriteFile(src, []byte("int main(void){return 0;}\n"), 0o644)

	// Conformant binary: default rpaths only.
	good := filepath.Join(dir, "good")
	run(t, cc, "-o", good, src)
	// Non-conformant: bakes an absolute rpath into the forbidden prefix.
	bad := filepath.Join(dir, "bad")
	run(t, cc, "-o", bad, src, "-Wl,-rpath,"+forbidden+"/lib")
	// Conformant: rpath shares "forbidden" as a string prefix but is a
	// distinct path component (a sibling directory), so it must pass.
	sibling := filepath.Join(dir, "forbidden-sibling")
	if err := os.MkdirAll(sibling, 0o755); err != nil {
		t.Fatal(err)
	}
	okSibling := filepath.Join(dir, "ok-sibling")
	run(t, cc, "-o", okSibling, src, "-Wl,-rpath,"+sibling+"/lib")

	vs, err := VerifyConformance(dir, []string{forbidden})
	if err != nil {
		t.Fatalf("VerifyConformance: %v", err)
	}
	if len(vs) != 1 || filepath.Base(vs[0].File) != "bad" {
		t.Fatalf("want exactly one violation on 'bad', got %+v", vs)
	}
}

// TestVerifyConformanceForbiddenPathWithSpace proves otoolRefs' operand
// parsing doesn't truncate at the first space: a naive strings.Fields split
// would read only "forbidden" out of an rpath like ".../for bidden/lib" and
// miss the violation.
func TestVerifyConformanceForbiddenPathWithSpace(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("otool is darwin-only")
	}
	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("cc not available")
	}
	dir := t.TempDir()
	forbidden := filepath.Join(dir, "for bidden")
	if err := os.MkdirAll(forbidden, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "m.c")
	os.WriteFile(src, []byte("int main(void){return 0;}\n"), 0o644)

	bad := filepath.Join(dir, "bad")
	run(t, cc, "-o", bad, src, "-Wl,-rpath,"+forbidden+"/lib")

	vs, err := VerifyConformance(dir, []string{forbidden})
	if err != nil {
		t.Fatalf("VerifyConformance: %v", err)
	}
	if len(vs) != 1 || filepath.Base(vs[0].File) != "bad" {
		t.Fatalf("want exactly one violation on 'bad', got %+v", vs)
	}
}

// run executes name with args, failing the test (with stderr) on error.
func run(t *testing.T, name string, args ...string) {
	t.Helper()
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}
