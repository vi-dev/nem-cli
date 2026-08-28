package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vi-dev/nem-cli/internal/home"
)

func testHome(t *testing.T) home.Home {
	dir := t.TempDir()
	return home.Resolve(func(k string) string {
		if k == "NEM_HOME" {
			return dir
		}
		return ""
	})
}

func TestOpenConfigFirstRunWritesOfficial(t *testing.T) {
	h := testHome(t)
	cfg, err := OpenConfig(h)
	if err != nil {
		t.Fatalf("OpenConfig: %v", err)
	}
	if len(cfg.Catalogs) != 1 || cfg.Catalogs[0].Name != "official" ||
		cfg.Catalogs[0].Type != "oci" || cfg.Catalogs[0].Ref != OfficialRef {
		t.Fatalf("first-run config: %+v", cfg.Catalogs)
	}
	if _, err := os.Stat(h.Config()); err != nil {
		t.Fatalf("config not persisted: %v", err)
	}
	// second open loads the same file, does not duplicate
	cfg2, err := OpenConfig(h)
	if err != nil || len(cfg2.Catalogs) != 1 {
		t.Fatalf("reopen: %+v, %v", cfg2, err)
	}
}

func TestSaveAndReload(t *testing.T) {
	h := testHome(t)
	cfg, _ := OpenConfig(h)
	cfg.Catalogs = append(cfg.Catalogs, CatalogEntry{Name: "dev", Type: "dir", Path: "/x/catalog"})
	if err := SaveConfig(h, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	got, err := OpenConfig(h)
	if err != nil || len(got.Catalogs) != 2 || got.Catalogs[1].Name != "dev" {
		t.Fatalf("reload: %+v, %v", got, err)
	}
	if got.Find("dev") == nil || got.Find("absent") != nil {
		t.Fatal("Find broken")
	}
}

func TestOpenConfigValidation(t *testing.T) {
	cases := []struct{ name, yaml, want string }{
		{"unknown field", "catalogs:\n  - name: a\n    type: dir\n    path: /x\n    bogus: 1\n", "bogus"},
		{"bad name", "catalogs:\n  - name: BAD\n    type: dir\n    path: /x\n", "name"},
		{"dup name", "catalogs:\n  - name: a\n    type: dir\n    path: /x\n  - name: a\n    type: dir\n    path: /y\n", "duplicate"},
		{"oci needs ref", "catalogs:\n  - name: a\n    type: oci\n", "ref"},
		{"oci forbids path", "catalogs:\n  - name: a\n    type: oci\n    ref: r:v2\n    path: /x\n", "path"},
		{"dir needs path", "catalogs:\n  - name: a\n    type: dir\n", "path"},
		{"unknown type", "catalogs:\n  - name: a\n    type: git\n    path: /x\n", "type"},
		{"host missing host", "hosts:\n  - plainHTTP: true\n", "host"},
		{"host no option set", "hosts:\n  - host: r.corp\n", "exactly one"},
		{"host two options set", "hosts:\n  - host: r.corp\n    plainHTTP: true\n    insecure: true\n", "exactly one"},
		{"host relative ca", "hosts:\n  - host: r.corp\n    ca: certs/ca.pem\n", "absolute"},
	}
	for _, c := range cases {
		h := testHome(t)
		os.MkdirAll(filepath.Dir(h.Config()), 0o755)
		os.WriteFile(h.Config(), []byte(c.yaml), 0o644)
		_, err := OpenConfig(h)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want mention of %q", c.name, err, c.want)
		}
	}
}

func TestSaveAndReloadHosts(t *testing.T) {
	h := testHome(t)
	cfg, _ := OpenConfig(h)
	cfg.Hosts = []HostEntry{{Host: "r.corp:5000", CA: "/etc/nem/ca.pem"}}
	if err := SaveConfig(h, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	got, err := OpenConfig(h)
	if err != nil || len(got.Hosts) != 1 || got.Hosts[0].Host != "r.corp:5000" {
		t.Fatalf("reload: %+v, %v", got, err)
	}
}

func TestLoadHostSettingsLenientNoFileNeverCreatesOne(t *testing.T) {
	h := testHome(t)
	settings, warnings := LoadHostSettingsLenient(h)
	if len(settings) != 0 || len(warnings) != 0 {
		t.Fatalf("no config file: settings=%+v warnings=%v, want both empty", settings, warnings)
	}
	if _, err := os.Stat(h.Config()); !os.IsNotExist(err) {
		t.Fatalf("lenient load must never create config.yaml, stat err = %v", err)
	}
}

func TestLoadHostSettingsLenientValidFileLastWins(t *testing.T) {
	h := testHome(t)
	os.MkdirAll(filepath.Dir(h.Config()), 0o755)
	yaml := "hosts:\n" +
		"  - host: r.corp\n    insecure: true\n" +
		"  - host: other.corp\n    ca: /etc/nem/ca.pem\n" +
		"  - host: r.corp\n    plainHTTP: true\n"
	os.WriteFile(h.Config(), []byte(yaml), 0o644)

	settings, warnings := LoadHostSettingsLenient(h)
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none", warnings)
	}
	if want := (HostEntry{Host: "r.corp", PlainHTTP: true}); settings["r.corp"] != want {
		t.Fatalf("r.corp = %+v, want %+v (last entry wins)", settings["r.corp"], want)
	}
	if _, err := os.Stat(h.Config()); err != nil {
		t.Fatalf("pre-existing config.yaml should still be there: %v", err)
	}
}

func TestLoadHostSettingsLenientDropsInvalidEntriesWithWarning(t *testing.T) {
	h := testHome(t)
	os.MkdirAll(filepath.Dir(h.Config()), 0o755)
	yaml := "hosts:\n" +
		"  - host: good.corp\n    plainHTTP: true\n" +
		"  - host: bad.corp\n" + // no option set
		"  - plainHTTP: true\n" // missing host
	os.WriteFile(h.Config(), []byte(yaml), 0o644)

	settings, warnings := LoadHostSettingsLenient(h)
	if len(settings) != 1 || !settings["good.corp"].PlainHTTP {
		t.Fatalf("settings = %+v, want only good.corp", settings)
	}
	if len(warnings) != 2 {
		t.Fatalf("warnings = %v, want 2 (one per bad entry)", warnings)
	}
	// Each warning names its offending host so a user doesn't have to count
	// list positions; the missing-host entry still renders (empty host).
	if !strings.Contains(warnings[0], `host "bad.corp"`) {
		t.Errorf("warnings[0] = %q, want to mention host %q", warnings[0], "bad.corp")
	}
	if !strings.Contains(warnings[1], `host ""`) {
		t.Errorf("warnings[1] = %q, want to mention the empty host", warnings[1])
	}
}

func TestLoadHostSettingsLenientUnparsableFile(t *testing.T) {
	h := testHome(t)
	os.MkdirAll(filepath.Dir(h.Config()), 0o755)
	os.WriteFile(h.Config(), []byte("hosts: [this is not: valid yaml"), 0o644)

	settings, warnings := LoadHostSettingsLenient(h)
	if len(settings) != 0 {
		t.Fatalf("settings = %+v, want none", settings)
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %v, want exactly 1", warnings)
	}
}

func TestLoadHostSettingsLenientUnreadableFile(t *testing.T) {
	h := testHome(t)
	// A directory where config.yaml is expected fails os.ReadFile
	// deterministically, without relying on permission bits or uid.
	os.MkdirAll(h.Config(), 0o755)

	settings, warnings := LoadHostSettingsLenient(h)
	if len(settings) != 0 {
		t.Fatalf("settings = %+v, want none", settings)
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %v, want exactly 1", warnings)
	}
}

func TestLoadHostSettingsLenientIgnoresUnknownKeys(t *testing.T) {
	h := testHome(t)
	os.MkdirAll(filepath.Dir(h.Config()), 0o755)
	yaml := "offline: true\n" +
		"hosts:\n" +
		"  - host: r.corp\n    plainHTTP: true\n    futureField: yes\n"
	os.WriteFile(h.Config(), []byte(yaml), 0o644)

	settings, warnings := LoadHostSettingsLenient(h)
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none (unknown keys degrade silently)", warnings)
	}
	if !settings["r.corp"].PlainHTTP {
		t.Fatalf("settings = %+v, want r.corp plainHTTP", settings)
	}
}

func TestReorder(t *testing.T) {
	cfg := &Config{Catalogs: []CatalogEntry{{Name: "a", Type: "dir", Path: "/a"}, {Name: "b", Type: "dir", Path: "/b"}}}
	if err := cfg.Reorder([]string{"b", "a"}); err != nil {
		t.Fatalf("Reorder: %v", err)
	}
	if cfg.Catalogs[0].Name != "b" {
		t.Fatalf("order: %+v", cfg.Catalogs)
	}
	for _, bad := range [][]string{{"a"}, {"a", "b", "c"}, {"a", "a"}} {
		if err := cfg.Reorder(bad); err == nil {
			t.Errorf("Reorder(%v) should fail", bad)
		}
	}
}
