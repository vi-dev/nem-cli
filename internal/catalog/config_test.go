package catalog

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
	cfg.Catalogs = append(cfg.Catalogs, Entry{Name: "dev", Type: "dir", Path: "/x/catalog"})
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

func TestReorder(t *testing.T) {
	cfg := &Config{Catalogs: []Entry{{Name: "a", Type: "dir", Path: "/a"}, {Name: "b", Type: "dir", Path: "/b"}}}
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
