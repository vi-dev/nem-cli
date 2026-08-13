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

func TestComposeBuildEnv(t *testing.T) {
	deps := []resolvedDep{
		{Name: "make", Version: "v4", Prefix: "/nh/packages/make/v4", OnPath: true, Bins: []string{"bin"}},
		{Name: "openssl", Version: "v3.4.0", Prefix: "/nh/packages/openssl/v3.4.0", OnLoaderPath: true, Libs: []string{"lib"}},
	}
	bctx := buildContext{
		Version: "v1.2.3", Platform: spec.Platform{OS: runtime.GOOS, Arch: runtime.GOARCH},
		Prefix: "/nh/packages/tool/v1.2.3", StagingDir: "/tmp/stg", OutputDir: "/tmp/stg/dist",
	}
	env := composeBuildEnv([]string{"PATH=/usr/bin", "CPPFLAGS=-DBASE"}, deps, bctx)
	m := envMap(env)

	if !strings.HasPrefix(m["PATH"], "/nh/packages/make/v4/bin:") {
		t.Fatalf("PATH must prepend run-dep bin: %q", m["PATH"])
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
	wantRpath := "@loader_path/../../../openssl/v3.4.0/lib"
	if runtime.GOOS == "linux" {
		wantRpath = "$ORIGIN/../../../openssl/v3.4.0/lib"
	}
	if !strings.Contains(m["LDFLAGS"], "-Wl,-rpath,"+wantRpath) {
		t.Fatalf("LDFLAGS rpath = %q, want %s", m["LDFLAGS"], wantRpath)
	}
	for k, want := range map[string]string{
		"NEM_VERSION": "v1.2.3", "NEM_PREFIX": "/nh/packages/tool/v1.2.3",
		"NEM_STAGING_DIR": "/tmp/stg", "NEM_OUTPUT": "/tmp/stg/dist",
	} {
		if m[k] != want {
			t.Errorf("%s = %q, want %q", k, m[k], want)
		}
	}
}
