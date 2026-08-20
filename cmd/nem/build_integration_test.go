package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// seedFooLinkDep installs a package "foo" straight into nemHome's package
// store, with a lib (for kind: link) and a bin/foocli script (for kind: run,
// or a PATH-visibility probe regardless of kind): install.Run's IsInstalled
// short-circuit means a job targeting an already-installed package never
// touches its catalog artifact. A one-package dir catalog is still
// registered so build-dep resolution can find foo and produce a lock entry,
// without any network fetch ever happening.
func seedFooLinkDep(t *testing.T, cc, nemHome string) {
	t.Helper()

	fooDir := filepath.Join(nemHome, "packages", "foo", "v1")
	includeDir := filepath.Join(fooDir, "include")
	if err := os.MkdirAll(includeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(includeDir, "foo.h"), "int foo_v(void);\n")

	libDir := filepath.Join(fooDir, "lib")
	if err := os.MkdirAll(libDir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "foo.c")
	writeFile(t, src, "int foo_v(void){return 7;}\n")
	switch runtime.GOOS {
	case "darwin":
		run(t, cc, "-dynamiclib", "-install_name", "@rpath/libfoo.dylib",
			"-o", filepath.Join(libDir, "libfoo.dylib"), src)
	case "linux":
		run(t, cc, "-shared", "-fPIC", "-Wl,-soname,libfoo.so",
			"-o", filepath.Join(libDir, "libfoo.so"), src)
	}

	binDir := filepath.Join(fooDir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "foocli"), []byte("#!/bin/sh\necho foocli\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	writeMetaFile(t, fooDir, "package: foo\nversion: v1\ncatalog: seed\nbins: [bin]\nlibs: [lib]\n")

	catDir := t.TempDir()
	pkgDir := filepath.Join(catDir, "pkgs", "foo")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(pkgDir, "pkg.yaml"), "schema: 2\nname: foo\nlibs: [lib]\n"+
		"artifact: {oci: \":{{.Version}}\"}\ninstall: [{extract: {}}]\nversions: [v1]\n")

	if _, errb, err := runNem(t, nemHome, "catalog", "add", "seed", catDir); err != nil {
		t.Fatalf("catalog add seed: %v\n%s", err, errb)
	}
}

// useCTarball serves a tarball whose sole source file calls the fixture
// dep's foo_v(), declared via the header seedFooLinkDep installs.
func useCTarball(t *testing.T) *httptest.Server {
	t.Helper()
	tgz := makeTarGz(t, map[string]string{
		"src/use.c": "#include <foo.h>\nint foo_v(void);\nint main(void){return foo_v();}\n",
	})
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(tgz) }))
}

// buildRecipe writes a "tool" pkg.yaml whose build depends on foo (kind:
// link) and whose one step is buildStep.
func buildRecipe(t *testing.T, dir, sourceURL, buildStep string) string {
	t.Helper()
	path := filepath.Join(dir, "pkg.yaml")
	writeFile(t, path, "schema: 2\nname: tool\n"+
		"artifact: {oci: \":{{.Version}}\"}\ninstall: [{extract: {}}]\n"+
		"versions: [v1.0.0]\nbuild:\n  source: {url: \""+sourceURL+"\"}\n"+
		"  deps: [{name: foo, kind: link}]\n  output: out\n"+
		"  steps:\n    - run: "+buildStep+"\n")
	return path
}

// buildRoleRecipe writes a "tool" pkg.yaml whose build depends on foo with
// the given kind and whose one step is buildStep.
func buildRoleRecipe(t *testing.T, dir, sourceURL, kind, buildStep string) string {
	t.Helper()
	path := filepath.Join(dir, "pkg.yaml")
	writeFile(t, path, "schema: 2\nname: tool\n"+
		"artifact: {oci: \":{{.Version}}\"}\ninstall: [{extract: {}}]\n"+
		"versions: [v1.0.0]\nbuild:\n  source: {url: \""+sourceURL+"\"}\n"+
		"  deps: [{name: foo, kind: "+kind+"}]\n  output: out\n"+
		"  steps:\n    - run: "+buildStep+"\n")
	return path
}

// TestCatalogBuildLinksAgainstDepViaScaffold proves the runner's env
// scaffold end to end: a recipe's build step links against a real
// dependency using only $CPPFLAGS/$LDFLAGS, and the resulting output tree
// passes conformance verification (Build fails on a non-conformant tree, so
// a successful exit here already proves verify passed).
func TestCatalogBuildLinksAgainstDepViaScaffold(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("loader semantics differ off darwin/linux")
	}
	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("cc not available")
	}

	nemHome := t.TempDir()
	seedFooLinkDep(t, cc, nemHome)

	srv := useCTarball(t)
	defer srv.Close()

	dir := t.TempDir()
	recipe := buildRecipe(t, dir, srv.URL,
		`mkdir -p "$NEM_OUTPUT/bin" && cc $CPPFLAGS -o "$NEM_OUTPUT/bin/usefoo" use.c $LDFLAGS -lfoo`)

	outDir := filepath.Join(t.TempDir(), "out")
	_, errb, err := runNem(t, nemHome, "catalog", "build", recipe, "--version", "v1.0.0", "--output", outDir)
	if err != nil {
		t.Fatalf("catalog build: %v\n%s", err, errb)
	}
	if _, err := os.Stat(filepath.Join(outDir, "bin", "usefoo")); err != nil {
		t.Fatalf("usefoo missing from conformant build output: %v", err)
	}
}

// TestCatalogBuildRejectsAbsoluteRpathIntoPackages proves the verify gate
// rejects a non-conformant recipe: one whose step bakes an absolute rpath
// into $NEM_HOME/packages rather than linking through $LDFLAGS's relative
// one.
func TestCatalogBuildRejectsAbsoluteRpathIntoPackages(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("loader semantics differ off darwin/linux")
	}
	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("cc not available")
	}

	nemHome := t.TempDir()
	seedFooLinkDep(t, cc, nemHome)

	srv := useCTarball(t)
	defer srv.Close()

	badRpath := filepath.Join(nemHome, "packages", "foo", "v1", "lib")
	dir := t.TempDir()
	recipe := buildRecipe(t, dir, srv.URL,
		`mkdir -p "$NEM_OUTPUT/bin" && cc $CPPFLAGS -o "$NEM_OUTPUT/bin/usefoo" use.c $LDFLAGS -lfoo -Wl,-rpath,`+badRpath)

	outDir := filepath.Join(t.TempDir(), "out")
	_, errb, err := runNem(t, nemHome, "catalog", "build", recipe, "--version", "v1.0.0", "--output", outDir)
	if err == nil {
		t.Fatal("catalog build must fail: recipe bakes an absolute rpath into packages")
	}
	if !strings.Contains(err.Error(), badRpath) {
		t.Fatalf("error should mention the offending path %q: %v", badRpath, err)
	}
	if !strings.Contains(errb, badRpath) {
		t.Fatalf("stderr should mention the offending path %q:\n%s", badRpath, errb)
	}
}

// probeStep writes ON_PATH or NOT_ON_PATH to $NEM_OUTPUT/probe depending on
// whether foocli (installed by seedFooLinkDep) resolves on the build step's
// PATH.
const probeStep = `mkdir -p "$NEM_OUTPUT" && (command -v foocli >/dev/null && echo ON_PATH || echo NOT_ON_PATH) > "$NEM_OUTPUT/probe"`

// TestCatalogBuildPutsDirectLinkDepBinsOnPath proves a kind: link build.dep
// contributes its bins to the build PATH: C libraries ship foo-config
// discovery scripts that configure scripts execute at build time.
func TestCatalogBuildPutsDirectLinkDepBinsOnPath(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("loader semantics differ off darwin/linux")
	}
	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("cc not available")
	}

	nemHome := t.TempDir()
	seedFooLinkDep(t, cc, nemHome)

	srv := useCTarball(t)
	defer srv.Close()

	dir := t.TempDir()
	recipe := buildRoleRecipe(t, dir, srv.URL, "link", probeStep)

	outDir := filepath.Join(t.TempDir(), "out")
	_, errb, err := runNem(t, nemHome, "catalog", "build", recipe, "--version", "v1.0.0", "--output", outDir)
	if err != nil {
		t.Fatalf("catalog build: %v\n%s", err, errb)
	}
	got, err := os.ReadFile(filepath.Join(outDir, "probe"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "ON_PATH") || strings.Contains(string(got), "NOT_ON_PATH") {
		t.Fatalf("a kind: link build dep's bins must join the build PATH; probe=%q", got)
	}
}

// TestCatalogBuildPutsDirectRunDepBinsOnPath proves a direct build.dep
// declared kind: run (the default) contributes its bins to the build PATH.
func TestCatalogBuildPutsDirectRunDepBinsOnPath(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("loader semantics differ off darwin/linux")
	}
	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("cc not available")
	}

	nemHome := t.TempDir()
	seedFooLinkDep(t, cc, nemHome)

	srv := useCTarball(t)
	defer srv.Close()

	dir := t.TempDir()
	recipe := buildRoleRecipe(t, dir, srv.URL, "run", probeStep)

	outDir := filepath.Join(t.TempDir(), "out")
	_, errb, err := runNem(t, nemHome, "catalog", "build", recipe, "--version", "v1.0.0", "--output", outDir)
	if err != nil {
		t.Fatalf("catalog build: %v\n%s", err, errb)
	}
	got, err := os.ReadFile(filepath.Join(outDir, "probe"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "ON_PATH") {
		t.Fatalf("a kind: run build dep's bins must be on PATH; probe=%q", got)
	}
}
