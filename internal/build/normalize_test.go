package build

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func writeTree(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readTree(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestNormalizeOutputFiles(t *testing.T) {
	out := t.TempDir()
	writeTree(t, out, "lib/libfoo.la", "libtool junk")
	writeTree(t, out, "lib/pkgconfig/foo.pc",
		"prefix=/\nexec_prefix=/\nlibdir=/lib\nincludedir=${prefix}/include\n\nName: foo\nLibs: -L${libdir} -lfoo\n")
	writeTree(t, out, "lib64/pkgconfig/bar.pc", "prefix=/x\nlibdir=/x/lib64\n")
	rel := "prefix=${pcfiledir}/../..\nlibdir=${prefix}/lib\n"
	writeTree(t, out, "share/pkgconfig/rel.pc", rel)

	if err := normalizeOutput(out); err != nil {
		t.Fatalf("normalizeOutput: %v", err)
	}

	if _, err := os.Stat(filepath.Join(out, "lib/libfoo.la")); !os.IsNotExist(err) {
		t.Fatalf("libfoo.la must be removed: %v", err)
	}
	want := "prefix=${pcfiledir}/../..\nexec_prefix=${prefix}\nlibdir=${prefix}/lib\nincludedir=${prefix}/include\n\nName: foo\nLibs: -L${libdir} -lfoo\n"
	if got := readTree(t, out, "lib/pkgconfig/foo.pc"); got != want {
		t.Fatalf("foo.pc = %q\nwant %q", got, want)
	}
	if got := readTree(t, out, "lib64/pkgconfig/bar.pc"); got != "prefix=${pcfiledir}/../..\nlibdir=${prefix}/lib64\n" {
		t.Fatalf("bar.pc = %q", got)
	}
	if got := readTree(t, out, "share/pkgconfig/rel.pc"); got != rel {
		t.Fatalf("already-relative rel.pc changed: %q", got)
	}

	// second run is a no-op
	before := readTree(t, out, "lib/pkgconfig/foo.pc")
	if err := normalizeOutput(out); err != nil {
		t.Fatalf("second normalizeOutput: %v", err)
	}
	if got := readTree(t, out, "lib/pkgconfig/foo.pc"); got != before {
		t.Fatalf("not idempotent: %q", got)
	}
}

// TestNormalizeMacho builds a dylib with an absolute install name and a
// binary referencing it — the shape a configure-driven DESTDIR install
// produces — and checks both are rewritten to @rpath and re-signed.
func TestNormalizeMacho(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Mach-O normalization is darwin-only")
	}
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}

	out := t.TempDir()
	src := t.TempDir()
	writeTree(t, src, "demo.c", "int demo(void) { return 42; }\n")
	writeTree(t, src, "main.c", "int demo(void); int main(void) { return demo(); }\n")
	if err := os.MkdirAll(filepath.Join(out, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(out, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	lib := filepath.Join(out, "lib", "libdemo.1.dylib")
	run := func(name string, args ...string) {
		t.Helper()
		if b, err := exec.Command(name, args...).CombinedOutput(); err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, b)
		}
	}
	run("clang", "-dynamiclib", "-install_name", "/lib/libdemo.1.dylib", "-o", lib, filepath.Join(src, "demo.c"))
	run("clang", "-o", filepath.Join(out, "bin", "demo"), filepath.Join(src, "main.c"), "-L", filepath.Join(out, "lib"), "-ldemo.1")

	fixes, err := planMachoFixes(out)
	if err != nil {
		t.Fatalf("planMachoFixes: %v", err)
	}
	if len(fixes) != 2 {
		t.Fatalf("fixes = %+v, want id fix and reference fix", fixes)
	}

	if err := normalizeOutput(out); err != nil {
		t.Fatalf("normalizeOutput: %v", err)
	}
	fixes, err = planMachoFixes(out)
	if err != nil {
		t.Fatalf("re-plan: %v", err)
	}
	if len(fixes) != 0 {
		t.Fatalf("normalization not idempotent, still planned: %+v", fixes)
	}
}
