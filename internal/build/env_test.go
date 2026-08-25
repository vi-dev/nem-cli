package build

import (
	"runtime"
	"strings"
	"testing"

	"github.com/vi-dev/nem-cli/internal/spec"
)

func envMap(env []string) map[string]string {
	m := map[string]string{}
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			m[k] = v
		}
	}
	return m
}

func TestComposeEnv(t *testing.T) {
	deps := []ResolvedDep{
		{Name: "make", Version: "v4", Prefix: "/nh/packages/make/v4", OnPath: true, Bins: []string{"bin"}},
		{Name: "openssl", Version: "v3.4.0", Prefix: "/nh/packages/openssl/v3.4.0", OnLoaderPath: true, Bins: []string{"bin"}, Libs: []string{"lib"}},
		{Name: "libgpg-error", Version: "1.51", Prefix: "/nh/packages/libgpg-error/1.51", OnLoaderPath: true},
	}
	ectx := EnvContext{
		Version: "v1.2.3", Platform: spec.Platform{OS: runtime.GOOS, Arch: runtime.GOARCH},
		Prefix: "/nh/packages/tool/v1.2.3", StagingDir: "/tmp/stg", OutputDir: "/tmp/stg/dist",
	}
	env := ComposeEnv([]string{"PATH=/usr/bin", "CPPFLAGS=-DBASE", "CGO_LDFLAGS=-lbase"}, deps, ectx)
	m := envMap(env)

	if !strings.HasPrefix(m["PATH"], "/nh/packages/make/v4/bin:") {
		t.Fatalf("PATH must prepend run-dep bin: %q", m["PATH"])
	}
	// A link dep's bins join the PATH too: C libraries ship foo-config
	// discovery scripts meant to be executed at build time.
	if !strings.Contains(m["PATH"], "/nh/packages/openssl/v3.4.0/bin") {
		t.Fatalf("PATH must include link-dep bin: %q", m["PATH"])
	}
	if strings.Contains(m["PATH"], "libgpg-error") {
		t.Fatalf("dep without bins must not add a PATH entry: %q", m["PATH"])
	}
	if !strings.Contains(m["CPPFLAGS"], "-DBASE") || !strings.Contains(m["CPPFLAGS"], "-I/nh/packages/openssl/v3.4.0/include") {
		t.Fatalf("CPPFLAGS must append to base and add link-dep include: %q", m["CPPFLAGS"])
	}
	if !strings.Contains(m["PKG_CONFIG_PATH"], "/nh/packages/openssl/v3.4.0/lib/pkgconfig") {
		t.Fatalf("PKG_CONFIG_PATH: %q", m["PKG_CONFIG_PATH"])
	}
	if !strings.Contains(m["LDFLAGS"], "-L/nh/packages/openssl/v3.4.0/lib") {
		t.Fatalf("LDFLAGS -L: %q", m["LDFLAGS"])
	}
	wantRpath := "-Wl,-rpath,@loader_path/../../../openssl/v3.4.0/lib"
	if runtime.GOOS == "linux" {
		// $ORIGIN doubled for make and single-quoted for the shell.
		wantRpath = "-Wl,-rpath,'$$ORIGIN/../../../openssl/v3.4.0/lib'"
	}
	if !strings.Contains(m["LDFLAGS"], wantRpath) {
		t.Fatalf("LDFLAGS rpath = %q, want %s", m["LDFLAGS"], wantRpath)
	}
	rpathLink := "-Wl,-rpath-link,/nh/packages/openssl/v3.4.0/lib"
	if runtime.GOOS == "linux" && !strings.Contains(m["LDFLAGS"], rpathLink) {
		t.Fatalf("linux LDFLAGS must carry -rpath-link: %q", m["LDFLAGS"])
	}
	if runtime.GOOS != "linux" && strings.Contains(m["LDFLAGS"], "-rpath-link") {
		t.Fatalf("-rpath-link is GNU ld only: %q", m["LDFLAGS"])
	}
	if !strings.Contains(m["CGO_CFLAGS"], "-I/nh/packages/openssl/v3.4.0/include") {
		t.Fatalf("CGO_CFLAGS: %q", m["CGO_CFLAGS"])
	}
	wantCgoRpath := "-Wl,-rpath,@loader_path/../../../openssl/v3.4.0/lib"
	if runtime.GOOS == "linux" {
		wantCgoRpath = "-Wl,-rpath,$ORIGIN/../../../openssl/v3.4.0/lib"
	}
	// cgo passes flags with no make or shell in between: single dollar, no quotes
	switch {
	case !strings.Contains(m["CGO_LDFLAGS"], "-lbase"):
		t.Fatalf("CGO_LDFLAGS must append to base: %q", m["CGO_LDFLAGS"])
	case !strings.Contains(m["CGO_LDFLAGS"], "-L/nh/packages/openssl/v3.4.0/lib"):
		t.Fatalf("CGO_LDFLAGS -L: %q", m["CGO_LDFLAGS"])
	case !strings.Contains(m["CGO_LDFLAGS"], wantCgoRpath),
		strings.Contains(m["CGO_LDFLAGS"], "$$"),
		strings.Contains(m["CGO_LDFLAGS"], "'"):
		t.Fatalf("CGO_LDFLAGS rpath = %q, want %s", m["CGO_LDFLAGS"], wantCgoRpath)
	}
	for k, want := range map[string]string{
		"NEM_VERSION": "v1.2.3", "NEM_PREFIX": "/nh/packages/tool/v1.2.3",
		"NEM_STAGING_DIR": "/tmp/stg", "NEM_OUTPUT": "/tmp/stg/dist",
	} {
		if m[k] != want {
			t.Errorf("%s = %q, want %q", k, m[k], want)
		}
	}
	for k, want := range map[string]string{
		"NEM_DEP_MAKE_PREFIX":         "/nh/packages/make/v4",
		"NEM_DEP_OPENSSL_PREFIX":      "/nh/packages/openssl/v3.4.0",
		"NEM_DEP_LIBGPG_ERROR_PREFIX": "/nh/packages/libgpg-error/1.51",
	} {
		if m[k] != want {
			t.Errorf("%s = %q, want %q", k, m[k], want)
		}
	}
}

func TestComposeEnvOmitsEmptyStagingAndOutput(t *testing.T) {
	env := ComposeEnv(nil, nil, EnvContext{
		Version: "v1", Platform: spec.Platform{OS: "linux", Arch: "amd64"}, Prefix: "/p",
	})
	m := envMap(env)
	if _, ok := m["NEM_STAGING_DIR"]; ok {
		t.Fatal("NEM_STAGING_DIR must be omitted when StagingDir is empty")
	}
	if _, ok := m["NEM_OUTPUT"]; ok {
		t.Fatal("NEM_OUTPUT must be omitted when OutputDir is empty")
	}
	if v := m["NEM_PREFIX"]; v != "/p" {
		t.Fatalf("NEM_PREFIX = %q", v)
	}
}

func TestComposeEnvSetsStagingAndOutputWhenPresent(t *testing.T) {
	env := ComposeEnv(nil, nil, EnvContext{
		Version: "v1", Platform: spec.Platform{OS: "linux", Arch: "amd64"},
		Prefix: "/p", StagingDir: "/s", OutputDir: "/o",
	})
	m := envMap(env)
	if v := m["NEM_STAGING_DIR"]; v != "/s" {
		t.Fatalf("NEM_STAGING_DIR = %q", v)
	}
	if v := m["NEM_OUTPUT"]; v != "/o" {
		t.Fatalf("NEM_OUTPUT = %q", v)
	}
}

func TestComposeEnvAbsRpathUsesAbsolutePaths(t *testing.T) {
	deps := []ResolvedDep{{
		Name: "foo", Version: "v1", Prefix: "/nem/packages/foo/v1",
		OnLoaderPath: true, Libs: []string{"lib"},
	}}
	ectx := EnvContext{
		Version: "v1", Platform: spec.Platform{OS: "linux", Arch: "amd64"},
		Prefix: "/nem/packages/tool/v2", AbsRpath: true,
	}
	ldflags := envMap(ComposeEnv(nil, deps, ectx))["LDFLAGS"]
	if !strings.Contains(ldflags, "-Wl,-rpath,/nem/packages/foo/v1/lib") {
		t.Fatalf("want an absolute rpath, got %q", ldflags)
	}
	if strings.Contains(ldflags, "ORIGIN") || strings.Contains(ldflags, "@loader_path") {
		t.Fatalf("absolute mode must emit no relative rpath, got %q", ldflags)
	}
}

func TestComposeEnvRelativeRpathIsTheDefault(t *testing.T) {
	deps := []ResolvedDep{{
		Name: "foo", Version: "v1", Prefix: "/nem/packages/foo/v1",
		OnLoaderPath: true, Libs: []string{"lib"},
	}}
	ectx := EnvContext{
		Version: "v1", Platform: spec.Platform{OS: "linux", Arch: "amd64"},
		Prefix: "/nem/packages/tool/v2",
	}
	ldflags := envMap(ComposeEnv(nil, deps, ectx))["LDFLAGS"]
	if !strings.Contains(ldflags, "/../../../foo/v1/lib") {
		t.Fatalf("want a relative rpath by default, got %q", ldflags)
	}
}
