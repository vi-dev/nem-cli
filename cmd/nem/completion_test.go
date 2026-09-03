package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"oras.land/oras-go/v2/content/oci"

	"github.com/vi-dev/nem-cli/internal/home"
	"github.com/vi-dev/nem-cli/internal/ocix"
)

// runNemComplete drives cobra's __complete verb. Unlike runNem it appends
// no extra flags: __complete treats the final argument as the word being
// completed, so the word list must arrive verbatim.
func runNemComplete(t *testing.T, nemHomeDir string, words ...string) string {
	t.Helper()
	t.Setenv("NEM_HOME", nemHomeDir)
	root := newRoot()
	var out, errb bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errb)
	root.SetArgs(append([]string{"__complete"}, words...))
	if err := root.Execute(); err != nil {
		t.Fatalf("__complete %v: %v\nstderr: %s", words, err, errb.String())
	}
	return out.String()
}

func writeTestConfig(t *testing.T, nemHomeDir string) {
	t.Helper()
	cfg := `catalogs:
  - name: official
    type: oci
    ref: ghcr.io/vi-dev/nem-official-catalog:v2
  - name: extras
    type: oci
    ref: ghcr.io/x/extras:v2
    disabled: true
  - name: local
    type: dir
    path: /nem-test/catalog
`
	if err := os.WriteFile(filepath.Join(nemHomeDir, "config.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCompleteCatalogNames(t *testing.T) {
	nemHomeDir := t.TempDir()
	writeTestConfig(t, nemHomeDir)

	tests := []struct {
		name    string
		words   []string
		want    []string
		exclude []string
	}{
		{"disable lists enabled", []string{"catalog", "disable", ""}, []string{"official", "local"}, []string{"extras"}},
		{"enable lists disabled", []string{"catalog", "enable", ""}, []string{"extras"}, []string{"official", "local"}},
		{"update lists enabled oci only", []string{"catalog", "update", ""}, []string{"official"}, []string{"extras", "local"}},
		{"remove lists all", []string{"catalog", "remove", ""}, []string{"official", "extras", "local"}, nil},
		{"remove second arg suppressed", []string{"catalog", "remove", "official", ""}, nil, []string{"official", "extras", "local"}},
		{"reorder lists all", []string{"catalog", "reorder", ""}, []string{"official", "extras", "local"}, nil},
		{"reorder excludes typed", []string{"catalog", "reorder", "official", ""}, []string{"extras", "local"}, []string{"official"}},
		{"prefix filters", []string{"catalog", "disable", "off"}, []string{"official"}, []string{"local"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := runNemComplete(t, nemHomeDir, tt.words...)
			for _, w := range tt.want {
				if !strings.Contains(out, w+"\n") {
					t.Errorf("missing %q:\n%s", w, out)
				}
			}
			for _, w := range tt.exclude {
				if strings.Contains(out, w+"\n") {
					t.Errorf("unexpected %q:\n%s", w, out)
				}
			}
			if !strings.Contains(out, ":4\n") {
				t.Errorf("want NoFileComp directive:\n%s", out)
			}
		})
	}
}

func TestCompletionNeverCreatesConfigFile(t *testing.T) {
	nemHomeDir := t.TempDir()
	out := runNemComplete(t, nemHomeDir, "catalog", "disable", "")
	if !strings.Contains(out, "official\n") {
		t.Errorf("default catalog not suggested:\n%s", out)
	}
	h := home.Resolve(func(k string) string {
		if k == "NEM_HOME" {
			return nemHomeDir
		}
		return ""
	})
	if _, err := os.Stat(h.Config()); !os.IsNotExist(err) {
		t.Fatalf("completion must never create config.yaml, stat err = %v", err)
	}
}

func TestCompleteUnuseDeclaredPackages(t *testing.T) {
	nemHomeDir := t.TempDir()
	projDir := t.TempDir()
	chdir(t, projDir)
	manifest := "[tools]\ngo = \"1.24.0\"\n\"official:node\" = \"22.0.0\"\n"
	if err := os.WriteFile(filepath.Join(projDir, "nem.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	out := runNemComplete(t, nemHomeDir, "unuse", "")
	for _, w := range []string{"go\n", "node\n"} {
		if !strings.Contains(out, w) {
			t.Errorf("missing %q:\n%s", w, out)
		}
	}

	out = runNemComplete(t, nemHomeDir, "unuse", "go", "")
	if strings.Contains(out, "go\n") {
		t.Errorf("already-typed package suggested again:\n%s", out)
	}
	if !strings.Contains(out, "node\n") {
		t.Errorf("missing node:\n%s", out)
	}
}

func TestCompleteUpdateDeclaredPackages(t *testing.T) {
	nemHomeDir := t.TempDir()
	projDir := t.TempDir()
	chdir(t, projDir)
	manifest := "[tools]\ngo = \"1.24.0\"\n\"official:node\" = \"22.0.0\"\n"
	if err := os.WriteFile(filepath.Join(projDir, "nem.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	out := runNemComplete(t, nemHomeDir, "update", "")
	for _, w := range []string{"go\n", "node\n"} {
		if !strings.Contains(out, w) {
			t.Errorf("missing %q:\n%s", w, out)
		}
	}

	out = runNemComplete(t, nemHomeDir, "update", "go", "")
	if strings.Contains(out, "go\n") {
		t.Errorf("already-typed package suggested again:\n%s", out)
	}
}

func TestCompleteQualifiedArgNotResuggested(t *testing.T) {
	nemHomeDir := t.TempDir()
	projDir := t.TempDir()
	chdir(t, projDir)
	manifest := "[tools]\n\"demo:tool\" = \"1.0.0\"\ngo = \"1.24.0\"\n"
	if err := os.WriteFile(filepath.Join(projDir, "nem.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	out := runNemComplete(t, nemHomeDir, "update", "demo:tool", "")
	if strings.Contains(out, "tool\n") {
		t.Errorf("a package typed with its qualifier must not be suggested again:\n%s", out)
	}
	if !strings.Contains(out, "go\n") {
		t.Errorf("other declared packages stay suggested:\n%s", out)
	}
}

func TestCompleteUnuseNoManifestIsSilent(t *testing.T) {
	nemHomeDir := t.TempDir()
	projDir := t.TempDir()
	chdir(t, projDir)
	out := runNemComplete(t, nemHomeDir, "unuse", "")
	if strings.Count(out, "\n") != 1 || !strings.HasPrefix(out, ":") {
		t.Errorf("want directive line only:\n%s", out)
	}
}

// seedSyncedCatalog publishes a fake catalog and syncs it into
// nemHomeDir's local mirror for catalogName, so completion sees a synced
// store without any network.
func seedSyncedCatalog(t *testing.T, nemHomeDir, catalogName string, entries []ocix.FakeEntry) {
	t.Helper()
	src, err := oci.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ocix.PushFakeCatalogForTest(t, src, entries, ocix.SchemaVersion)
	storePath := filepath.Join(nemHomeDir, "catalogs", catalogName, "store")
	if _, err := ocix.SyncLocalCatalog(context.Background(), src, "v2", storePath, nil); err != nil {
		t.Fatal(err)
	}
}

const completionPkgYAML = `
schema: 2
name: %s
platforms: [darwin/arm64, darwin/amd64, linux/arm64, linux/amd64]
artifact:
  oci: ":{{.Version}}"
install:
  - extract: {}
versions:
  - v1.2.0
  - v1.0.0
`

func TestCompleteUsePackagesAndVersions(t *testing.T) {
	nemHomeDir := t.TempDir()
	seedSyncedCatalog(t, nemHomeDir, "official", []ocix.FakeEntry{
		{Name: "go", Description: "Go toolchain", Latest: "v1.2.0", YAML: []byte(fmt.Sprintf(completionPkgYAML, "go"))},
		{Name: "node", Description: "Node.js", Latest: "v22.0.0", YAML: []byte(fmt.Sprintf(completionPkgYAML, "node"))},
	})

	out := runNemComplete(t, nemHomeDir, "use", "")
	if !strings.Contains(out, "go\tGo toolchain\n") || !strings.Contains(out, "node\tNode.js\n") {
		t.Errorf("package suggestions missing:\n%s", out)
	}
	if !strings.Contains(out, ":6\n") { // NoSpace|NoFileComp
		t.Errorf("want NoSpace|NoFileComp directive:\n%s", out)
	}

	out = runNemComplete(t, nemHomeDir, "use", "go@")
	for _, w := range []string{"go@v1.2.0\n", "go@v1.0.0\n"} {
		if !strings.Contains(out, w) {
			t.Errorf("missing %q:\n%s", w, out)
		}
	}

	out = runNemComplete(t, nemHomeDir, "use", "go@v1.2")
	if !strings.Contains(out, "go@v1.2.0\n") || strings.Contains(out, "go@v1.0.0\n") {
		t.Errorf("version prefix filter wrong:\n%s", out)
	}

	out = runNemComplete(t, nemHomeDir, "use", "go", "")
	if strings.Contains(out, "go\t") {
		t.Errorf("already-typed package suggested again:\n%s", out)
	}

	out = runNemComplete(t, nemHomeDir, "use", "official:g")
	if !strings.Contains(out, "official:go\tGo toolchain\n") {
		t.Errorf("catalog-prefixed suggestion missing:\n%s", out)
	}

	out = runNemComplete(t, nemHomeDir, "use", "official:go@v1.0")
	if !strings.Contains(out, "official:go@v1.0.0\n") {
		t.Errorf("catalog-prefixed version missing:\n%s", out)
	}
}

func TestCompleteUseUnsyncedCatalogIsSilent(t *testing.T) {
	nemHomeDir := t.TempDir()
	out := runNemComplete(t, nemHomeDir, "use", "")
	if !strings.HasPrefix(out, ":") {
		t.Errorf("unsynced catalog must yield no suggestions and no error:\n%s", out)
	}
}

func TestCompleteUseDirCatalogListsNamesWithoutParsing(t *testing.T) {
	nemHomeDir := t.TempDir()
	catDir := t.TempDir()
	// malformed on purpose: membership is a stat, never a parse
	writeCatalogPkg(t, catDir, "mytool", "not: [valid")
	cfg := fmt.Sprintf("catalogs:\n  - name: local\n    type: dir\n    path: %s\n", catDir)
	if err := os.WriteFile(filepath.Join(nemHomeDir, "config.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	out := runNemComplete(t, nemHomeDir, "use", "")
	if !strings.Contains(out, "mytool\n") {
		t.Errorf("dir catalog name missing:\n%s", out)
	}
}

func TestCompleteInfoAndSearch(t *testing.T) {
	nemHomeDir := t.TempDir()
	seedSyncedCatalog(t, nemHomeDir, "official", []ocix.FakeEntry{
		{Name: "go", Description: "Go toolchain", Latest: "v1.2.0", YAML: []byte(fmt.Sprintf(completionPkgYAML, "go"))},
	})
	for _, cmd := range []string{"info", "search"} {
		out := runNemComplete(t, nemHomeDir, cmd, "")
		if !strings.Contains(out, "go\tGo toolchain\n") || !strings.Contains(out, ":4\n") {
			t.Errorf("%s: want package suggestion with NoFileComp:\n%s", cmd, out)
		}
		out = runNemComplete(t, nemHomeDir, cmd, "go", "")
		if strings.Contains(out, "go\t") {
			t.Errorf("%s: second arg must not be completed:\n%s", cmd, out)
		}
	}
}

func TestCompleteStaticValues(t *testing.T) {
	nemHomeDir := t.TempDir()
	tests := []struct {
		words []string
		want  []string
	}{
		{[]string{"activate", ""}, []string{"bash", "zsh"}},
		{[]string{"deactivate", ""}, []string{"bash", "zsh"}},
		{[]string{"env", "--shell", ""}, []string{"bash", "zsh"}},
		{[]string{"--color", ""}, []string{"auto", "always", "never"}},
		{[]string{"catalog", "add", "--type", ""}, []string{"oci", "dir"}},
	}
	for _, tt := range tests {
		out := runNemComplete(t, nemHomeDir, tt.words...)
		for _, w := range tt.want {
			if !strings.Contains(out, w+"\n") {
				t.Errorf("__complete %v missing %q:\n%s", tt.words, w, out)
			}
		}
	}

	// env rejects fish, so completion must not offer it
	out := runNemComplete(t, nemHomeDir, "env", "--shell", "")
	if strings.Contains(out, "fish\n") {
		t.Errorf("env --shell offered a value env rejects:\n%s", out)
	}
}

func TestCompletePathFilters(t *testing.T) {
	nemHomeDir := t.TempDir()
	out := runNemComplete(t, nemHomeDir, "catalog", "build", "")
	if !strings.Contains(out, "yaml\n") || !strings.Contains(out, ":8\n") {
		t.Errorf("build should filter to yaml files:\n%s", out)
	}
	out = runNemComplete(t, nemHomeDir, "catalog", "test", "")
	if !strings.Contains(out, ":8\n") {
		t.Errorf("test should filter to yaml files:\n%s", out)
	}
	out = runNemComplete(t, nemHomeDir, "catalog", "lint", "")
	if !strings.Contains(out, ":16\n") {
		t.Errorf("lint should complete directories:\n%s", out)
	}
}

func TestCompleteUseDedupeFirstCatalogWins(t *testing.T) {
	nemHomeDir := t.TempDir()
	seedSyncedCatalog(t, nemHomeDir, "official", []ocix.FakeEntry{
		{Name: "go", Description: "Go toolchain", Latest: "v1.2.0", YAML: []byte(fmt.Sprintf(completionPkgYAML, "go"))},
	})
	catDir := t.TempDir()
	for _, name := range []string{"go", "other"} {
		writeCatalogPkg(t, catDir, name, "name: "+name)
	}
	cfg := fmt.Sprintf(`catalogs:
  - name: official
    type: oci
    ref: ghcr.io/vi-dev/nem-official-catalog:v2
  - name: local
    type: dir
    path: %s
`, catDir)
	if err := os.WriteFile(filepath.Join(nemHomeDir, "config.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	out := runNemComplete(t, nemHomeDir, "use", "")
	if !strings.Contains(out, "go\tGo toolchain\n") {
		t.Errorf("first catalog's entry missing:\n%s", out)
	}
	if strings.Contains(out, "\ngo\n") || strings.HasPrefix(out, "go\n") {
		t.Errorf("duplicate bare suggestion for go leaked from second catalog:\n%s", out)
	}
	if !strings.Contains(out, "other\n") {
		t.Errorf("second catalog's unique package missing:\n%s", out)
	}
}

func TestCompleteUseCornerTokens(t *testing.T) {
	nemHomeDir := t.TempDir()
	catDir := t.TempDir()
	writeCatalogPkg(t, catDir, "mytool", "name: mytool")
	cfg := fmt.Sprintf("catalogs:\n  - name: local\n    type: dir\n    path: %s\n", catDir)
	if err := os.WriteFile(filepath.Join(nemHomeDir, "config.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	// bare "@" and a second "@" in the version part parse to an invalid
	// key, so both must degrade to a directive-only reply
	for _, token := range []string{"@", "go@v1.0.0@x"} {
		out := runNemComplete(t, nemHomeDir, "use", token)
		if strings.Count(out, "\n") != 1 || !strings.HasPrefix(out, ":") {
			t.Errorf("use %q: want directive line only:\n%s", token, out)
		}
	}

	// an unknown catalog prefix matches no source: directive only
	out := runNemComplete(t, nemHomeDir, "use", "ghost:")
	if strings.Count(out, "\n") != 1 || !strings.HasPrefix(out, ":") {
		t.Errorf("want directive line only for unknown catalog prefix:\n%s", out)
	}

	// a bare ":" keeps the documented fallback: no catalog narrowing
	out = runNemComplete(t, nemHomeDir, "use", ":")
	if !strings.Contains(out, "mytool\n") || !strings.Contains(out, ":6\n") {
		t.Errorf("bare colon should fall back to the full listing:\n%s", out)
	}
}

func TestCompleteUseVersionsStopsOnBrokenCatalog(t *testing.T) {
	nemHomeDir := t.TempDir()
	first, second := t.TempDir(), t.TempDir()
	writeCatalogPkg(t, first, "go", "not: [valid")
	writeCatalogPkg(t, second, "go", fmt.Sprintf(completionPkgYAML, "go"))
	cfg := fmt.Sprintf(`catalogs:
  - name: first
    type: dir
    path: %s
  - name: second
    type: dir
    path: %s
`, first, second)
	if err := os.WriteFile(filepath.Join(nemHomeDir, "config.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	// use would abort on first's broken manifest, so suggest nothing
	out := runNemComplete(t, nemHomeDir, "use", "go@")
	if strings.Count(out, "\n") != 1 || !strings.HasPrefix(out, ":") {
		t.Errorf("want directive line only:\n%s", out)
	}
}

func TestCompleteUseVersionsFallsThroughMissingPackage(t *testing.T) {
	nemHomeDir := t.TempDir()
	first, second := t.TempDir(), t.TempDir()
	writeCatalogPkg(t, first, "other", fmt.Sprintf(completionPkgYAML, "other"))
	writeCatalogPkg(t, second, "go", fmt.Sprintf(completionPkgYAML, "go"))
	cfg := fmt.Sprintf(`catalogs:
  - name: first
    type: dir
    path: %s
  - name: second
    type: dir
    path: %s
`, first, second)
	if err := os.WriteFile(filepath.Join(nemHomeDir, "config.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	out := runNemComplete(t, nemHomeDir, "use", "go@")
	if !strings.Contains(out, "go@v1.2.0\n") {
		t.Errorf("versions from a later catalog missing:\n%s", out)
	}
}

func TestCompleteSearchIgnoresCatalogGrammar(t *testing.T) {
	nemHomeDir := t.TempDir()
	seedSyncedCatalog(t, nemHomeDir, "official", []ocix.FakeEntry{
		{Name: "go", Description: "Go toolchain", Latest: "v1.2.0", YAML: []byte(fmt.Sprintf(completionPkgYAML, "go"))},
	})

	// a "catalog:pkg" token would be substring-matched literally and never hit
	out := runNemComplete(t, nemHomeDir, "search", "official:g")
	if strings.Count(out, "\n") != 1 || !strings.HasPrefix(out, ":") {
		t.Errorf("want directive line only for a colon query:\n%s", out)
	}

	out = runNemComplete(t, nemHomeDir, "search", "g")
	if !strings.Contains(out, "go\tGo toolchain\n") {
		t.Errorf("bare-name suggestion missing:\n%s", out)
	}
}
