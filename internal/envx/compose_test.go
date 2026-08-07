package envx

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/vi-dev/nem-cli/internal/home"
	"github.com/vi-dev/nem-cli/internal/install"
	"github.com/vi-dev/nem-cli/internal/project"
	"github.com/vi-dev/nem-cli/internal/spec"
)

func testHome() home.Home {
	return home.Resolve(func(k string) string {
		if k == "NEM_HOME" {
			return "/nemhome-test"
		}
		return ""
	})
}

func mapMetaLookup(m map[string]*install.Meta) func(string, string) (*install.Meta, bool) {
	return func(name, version string) (*install.Meta, bool) {
		v, ok := m[name+"@"+version]
		return v, ok
	}
}

func mapGetenv(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := m[k]
		return v, ok
	}
}

func metaKey(name, version string) string { return name + "@" + version }

func currentPlatform() string { return spec.Current().String() }

func TestComposePrecedencePackageExportOverriddenByEnvLayers(t *testing.T) {
	h := testHome()

	global := &project.Manifest{Env: []project.EnvVar{
		{Name: "LEVEL", Value: "global-env"},
		{Name: "GLOBAL_ENV_VAR", Value: "g"},
	}}
	proj := &project.Manifest{Env: []project.EnvVar{
		{Name: "LEVEL", Value: "project-env"},
		{Name: "PROJECT_ENV_VAR", Value: "p"},
	}}

	globalLock := &project.Lockfile{Packages: []project.LockEntry{
		{Name: "pkgg", Version: "1.0.0", Direct: true, Platforms: []string{currentPlatform()}},
	}}
	projectLock := &project.Lockfile{Packages: []project.LockEntry{
		{Name: "pkgp", Version: "1.0.0", Direct: true, Platforms: []string{currentPlatform()}},
	}}

	metaLookup := mapMetaLookup(map[string]*install.Meta{
		metaKey("pkgg", "1.0.0"): {Env: []spec.EnvExport{
			{Name: "LEVEL", Value: "global-pkg"},
			{Name: "GLOBAL_PKG_VAR", Value: "gp"},
		}},
		metaKey("pkgp", "1.0.0"): {Env: []spec.EnvExport{
			{Name: "LEVEL", Value: "project-pkg"},
			{Name: "PROJECT_PKG_VAR", Value: "pp"},
		}},
	})

	result := Compose(proj, global, projectLock, globalLock, h, metaLookup, mapGetenv(nil))

	got := map[string]string{}
	for _, v := range result.Vars {
		got[v.Name] = v.Value
	}

	if got["LEVEL"] != "project-env" {
		t.Fatalf("LEVEL = %q, want project-env (project [env] must win over all lower layers)", got["LEVEL"])
	}
	for name, want := range map[string]string{
		"GLOBAL_PKG_VAR":  "gp",
		"PROJECT_PKG_VAR": "pp",
		"GLOBAL_ENV_VAR":  "g",
		"PROJECT_ENV_VAR": "p",
	} {
		if got[name] != want {
			t.Errorf("%s = %q, want %q", name, got[name], want)
		}
	}
}

func TestComposePathOrderDirectThenIndirectAlphabetical(t *testing.T) {
	h := testHome()

	projectLock := &project.Lockfile{Packages: []project.LockEntry{
		{Name: "zebra", Version: "1.0.0", Direct: true, Platforms: []string{currentPlatform()}},
		{Name: "apple", Version: "1.0.0", Direct: true, Platforms: []string{currentPlatform()}},
		{Name: "mango", Version: "1.0.0", Direct: false, Platforms: []string{currentPlatform()}},
	}}
	globalLock := &project.Lockfile{}

	metaLookup := mapMetaLookup(map[string]*install.Meta{
		metaKey("zebra", "1.0.0"): {Bins: []string{"bin"}},
		metaKey("apple", "1.0.0"): {Bins: []string{"bin"}},
		metaKey("mango", "1.0.0"): {Bins: []string{"bin"}},
	})

	result := Compose(&project.Manifest{}, &project.Manifest{}, projectLock, globalLock, h, metaLookup, mapGetenv(nil))

	appleDir, _ := h.PackageDir("apple", "1.0.0")
	zebraDir, _ := h.PackageDir("zebra", "1.0.0")
	mangoDir, _ := h.PackageDir("mango", "1.0.0")
	want := []string{
		filepath.Join(appleDir, "bin"),
		filepath.Join(zebraDir, "bin"),
		filepath.Join(mangoDir, "bin"),
	}

	if len(result.Path) != len(want) {
		t.Fatalf("Path = %v, want %v", result.Path, want)
	}
	for i := range want {
		if result.Path[i] != want[i] {
			t.Fatalf("Path = %v, want %v", result.Path, want)
		}
	}
}

func TestComposePathProjectBeforeGlobalWithDedup(t *testing.T) {
	h := testHome()

	// "shared" appears identically (same name+version) in both locks: the
	// install dir is the same, so it must dedup to a single PATH entry.
	projectLock := &project.Lockfile{Packages: []project.LockEntry{
		{Name: "shared", Version: "2.0.0", Direct: true, Platforms: []string{currentPlatform()}},
	}}
	globalLock := &project.Lockfile{Packages: []project.LockEntry{
		{Name: "shared", Version: "2.0.0", Direct: true, Platforms: []string{currentPlatform()}},
		{Name: "curl", Version: "1.0.0", Direct: true, Platforms: []string{currentPlatform()}},
	}}

	metaLookup := mapMetaLookup(map[string]*install.Meta{
		metaKey("shared", "2.0.0"): {Bins: []string{"bin"}},
		metaKey("curl", "1.0.0"):   {Bins: []string{"bin"}},
	})

	result := Compose(&project.Manifest{}, &project.Manifest{}, projectLock, globalLock, h, metaLookup, mapGetenv(nil))

	sharedDir, _ := h.PackageDir("shared", "2.0.0")
	curlDir, _ := h.PackageDir("curl", "1.0.0")
	want := []string{
		filepath.Join(sharedDir, "bin"),
		filepath.Join(curlDir, "bin"),
	}

	if len(result.Path) != len(want) {
		t.Fatalf("Path = %v, want %v (dedup across project/global expected)", result.Path, want)
	}
	for i := range want {
		if result.Path[i] != want[i] {
			t.Fatalf("Path = %v, want %v", result.Path, want)
		}
	}
}

func TestComposeReservedEnvNameSkippedWithWarning(t *testing.T) {
	h := testHome()

	proj := &project.Manifest{Env: []project.EnvVar{
		{Name: "PATH", Value: "/usr/bin"},
		{Name: "OK", Value: "fine"},
	}}

	result := Compose(proj, &project.Manifest{}, &project.Lockfile{}, &project.Lockfile{}, h,
		mapMetaLookup(nil), mapGetenv(nil))

	for _, v := range result.Vars {
		if v.Name == "PATH" {
			t.Fatalf("PATH must never appear in composed Vars, got %v", result.Vars)
		}
	}
	found := false
	for _, v := range result.Vars {
		if v.Name == "OK" && v.Value == "fine" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected OK=fine to survive, Vars = %v", result.Vars)
	}
	if !anyContains(result.Warnings, "PATH") {
		t.Fatalf("expected a warning mentioning PATH, got %v", result.Warnings)
	}
}

func TestComposeSelfReferentialEnvUsesSavedOriginalNotComposedValue(t *testing.T) {
	h := testHome()

	globalLock := &project.Lockfile{Packages: []project.LockEntry{
		{Name: "toolg", Version: "1.0.0", Direct: true, Platforms: []string{currentPlatform()}},
	}}
	metaLookup := mapMetaLookup(map[string]*install.Meta{
		metaKey("toolg", "1.0.0"): {Env: []spec.EnvExport{
			{Name: "FOO", Value: "from-package"},
		}},
	})

	proj := &project.Manifest{Env: []project.EnvVar{
		{Name: "FOO", Value: "$FOO:extra"},
	}}

	getenv := mapGetenv(map[string]string{
		"NEM_SAVED__FOO_SET": "1",
		"NEM_SAVED__FOO":     "saved-original",
		"FOO":                "live-value-should-be-ignored",
	})

	result := Compose(proj, &project.Manifest{}, &project.Lockfile{}, globalLock, h, metaLookup, getenv)

	got := map[string]string{}
	for _, v := range result.Vars {
		got[v.Name] = v.Value
	}
	if got["FOO"] != "saved-original:extra" {
		t.Fatalf("FOO = %q, want %q (must use saved original, not the package-export composed value or the live getenv value)", got["FOO"], "saved-original:extra")
	}
}

func TestComposeSelfReferentialEnvFallsBackToLiveGetenvWithoutSavedFlag(t *testing.T) {
	h := testHome()

	proj := &project.Manifest{Env: []project.EnvVar{
		{Name: "FOO", Value: "$FOO:extra"},
	}}
	getenv := mapGetenv(map[string]string{
		"FOO": "shell-value",
	})

	result := Compose(proj, &project.Manifest{}, &project.Lockfile{}, &project.Lockfile{}, h, mapMetaLookup(nil), getenv)

	got := map[string]string{}
	for _, v := range result.Vars {
		got[v.Name] = v.Value
	}
	if got["FOO"] != "shell-value:extra" {
		t.Fatalf("FOO = %q, want %q", got["FOO"], "shell-value:extra")
	}
}

func TestComposePackageExportTemplateExpansion(t *testing.T) {
	h := testHome()

	globalLock := &project.Lockfile{Packages: []project.LockEntry{
		{Name: "toolg", Version: "9.9.9", Direct: true, Platforms: []string{currentPlatform()}},
	}}
	metaLookup := mapMetaLookup(map[string]*install.Meta{
		metaKey("toolg", "9.9.9"): {Env: []spec.EnvExport{
			{Name: "GOROOT", Value: "{{.InstallDir}}/go"},
			{Name: "GOVERSION", Value: "v{{.Version}}"},
		}},
	})

	result := Compose(&project.Manifest{}, &project.Manifest{}, &project.Lockfile{}, globalLock, h,
		metaLookup, mapGetenv(nil))

	installDir, _ := h.PackageDir("toolg", "9.9.9")
	got := map[string]string{}
	for _, v := range result.Vars {
		got[v.Name] = v.Value
	}
	if want := filepath.Join(installDir, "go"); got["GOROOT"] != want {
		t.Errorf("GOROOT = %q, want %q", got["GOROOT"], want)
	}
	if got["GOVERSION"] != "v9.9.9" {
		t.Errorf("GOVERSION = %q, want %q", got["GOVERSION"], "v9.9.9")
	}
}

func TestComposePackageExportBrokenTemplateWarning(t *testing.T) {
	h := testHome()

	globalLock := &project.Lockfile{Packages: []project.LockEntry{
		{Name: "toolg", Version: "1.0.0", Direct: true, Platforms: []string{currentPlatform()}},
	}}
	metaLookup := mapMetaLookup(map[string]*install.Meta{
		metaKey("toolg", "1.0.0"): {Env: []spec.EnvExport{
			{Name: "BAD", Value: "{{.NoSuchField}}"},
		}},
	})

	result := Compose(&project.Manifest{}, &project.Manifest{}, &project.Lockfile{}, globalLock, h,
		metaLookup, mapGetenv(nil))

	for _, v := range result.Vars {
		if v.Name == "BAD" {
			t.Fatalf("BAD should have been skipped on template failure, got Vars = %v", result.Vars)
		}
	}
	if !anyContains(result.Warnings, "reinstall toolg") {
		t.Fatalf("expected a warning about reinstalling toolg, got %v", result.Warnings)
	}
}

func TestComposePackageExportReservedNameSkippedBeforeTemplateRender(t *testing.T) {
	h := testHome()

	globalLock := &project.Lockfile{Packages: []project.LockEntry{
		{Name: "toolg", Version: "1.0.0", Direct: true, Platforms: []string{currentPlatform()}},
	}}
	metaLookup := mapMetaLookup(map[string]*install.Meta{
		metaKey("toolg", "1.0.0"): {Env: []spec.EnvExport{
			{Name: "PATH", Value: "{{.NoSuchField}}"},
		}},
	})

	result := Compose(&project.Manifest{}, &project.Manifest{}, &project.Lockfile{}, globalLock, h,
		metaLookup, mapGetenv(nil))

	if !anyContains(result.Warnings, "reserved") {
		t.Fatalf("expected a reserved-name warning, got %v", result.Warnings)
	}
	if anyContains(result.Warnings, "reinstall") {
		t.Fatalf("reserved-name check should short-circuit before template rendering, got %v", result.Warnings)
	}
}

func TestComposeManagedKeysIncludePackageExportsForSavedOriginal(t *testing.T) {
	h := testHome()

	globalLock := &project.Lockfile{Packages: []project.LockEntry{
		{Name: "toolg", Version: "1.0.0", Direct: true, Platforms: []string{currentPlatform()}},
	}}
	metaLookup := mapMetaLookup(map[string]*install.Meta{
		metaKey("toolg", "1.0.0"): {Env: []spec.EnvExport{
			{Name: "GOROOT", Value: "/opt/go"},
		}},
	})

	proj := &project.Manifest{Env: []project.EnvVar{
		{Name: "EXTRA", Value: "$GOROOT/tools"},
	}}

	// GOROOT is never declared in [env] — it only exists as a package
	// export. Its live value here stands in for nem's own prior export
	// (what a previous activation left in the shell); the saved bookkeeping
	// holds the true pre-nem original.
	getenv := mapGetenv(map[string]string{
		"NEM_SAVED__GOROOT_SET": "1",
		"NEM_SAVED__GOROOT":     "/pre-nem/go",
		"GOROOT":                "/opt/go",
	})

	result := Compose(proj, &project.Manifest{}, &project.Lockfile{}, globalLock, h, metaLookup, getenv)

	got := map[string]string{}
	for _, v := range result.Vars {
		got[v.Name] = v.Value
	}
	if want := "/pre-nem/go/tools"; got["EXTRA"] != want {
		t.Fatalf("EXTRA = %q, want %q (a reference to a package-exported name must resolve to its saved pre-nem original, not the live nem-set value)", got["EXTRA"], want)
	}
}

func TestComposePackageExportPlatformConstraintExcludesOtherPlatforms(t *testing.T) {
	h := testHome()

	globalLock := &project.Lockfile{Packages: []project.LockEntry{
		{Name: "toolg", Version: "1.0.0", Direct: true, Platforms: []string{currentPlatform()}},
	}}
	metaLookup := mapMetaLookup(map[string]*install.Meta{
		metaKey("toolg", "1.0.0"): {Env: []spec.EnvExport{
			{Name: "OTHERPLAT_ONLY", Value: "x", Platforms: []spec.Platform{{OS: "plan9", Arch: "mips"}}},
			{Name: "ALWAYS", Value: "y"},
		}},
	})

	result := Compose(&project.Manifest{}, &project.Manifest{}, &project.Lockfile{}, globalLock, h,
		metaLookup, mapGetenv(nil))

	got := map[string]string{}
	for _, v := range result.Vars {
		got[v.Name] = v.Value
	}
	if _, ok := got["OTHERPLAT_ONLY"]; ok {
		t.Fatalf("OTHERPLAT_ONLY should be excluded (platform-constrained to a platform we're not on), got Vars = %v", result.Vars)
	}
	if got["ALWAYS"] != "y" {
		t.Fatalf("ALWAYS = %q, want %q (unconstrained export must still apply)", got["ALWAYS"], "y")
	}
}

func TestComposeSkipsLockEntriesNotOnCurrentPlatform(t *testing.T) {
	h := testHome()

	projectLock := &project.Lockfile{Packages: []project.LockEntry{
		{Name: "otherplat", Version: "1.0.0", Direct: true, Platforms: []string{"plan9/mips"}},
	}}

	calls := 0
	metaLookup := func(name, version string) (*install.Meta, bool) {
		calls++
		return nil, false
	}

	result := Compose(&project.Manifest{}, &project.Manifest{}, projectLock, &project.Lockfile{}, h,
		metaLookup, mapGetenv(nil))

	if calls != 0 {
		t.Fatalf("metaLookup should never be called for an entry not on the current platform, got %d calls", calls)
	}
	if len(result.Path) != 0 {
		t.Fatalf("Path = %v, want empty", result.Path)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("Warnings = %v, want empty (platform-excluded entries are not an error)", result.Warnings)
	}
}

func anyContains(list []string, substr string) bool {
	for _, s := range list {
		if strings.Contains(s, substr) {
			return true
		}
	}
	return false
}
