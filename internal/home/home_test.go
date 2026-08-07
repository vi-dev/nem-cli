package home

import "testing"

func testHome(t *testing.T) Home {
	t.Helper()
	return Resolve(func(k string) string {
		if k == "NEM_HOME" {
			return "/x/nem"
		}
		return ""
	})
}

func TestResolve(t *testing.T) {
	h := testHome(t)
	cases := []struct{ got, want string }{
		{h.Config(), "/x/nem/config.yaml"},
		{h.GlobalManifest(), "/x/nem/nem.toml"},
		{h.GlobalLock(), "/x/nem/nem.lock"},
		{h.LockFile(), "/x/nem/lock"},
		{h.Tmp(), "/x/nem/tmp"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
}

func TestResolveDefault(t *testing.T) {
	h := Resolve(func(string) string { return "" })
	if h.Config() == "/config.yaml" || h.Root() == "" {
		t.Fatalf("default root empty: %q", h.Root())
	}
}

func TestPackageDir(t *testing.T) {
	h := testHome(t)
	got, err := h.PackageDir("go", "v1.26.5")
	if err != nil || got != "/x/nem/packages/go/v1.26.5" {
		t.Fatalf("PackageDir(go, v1.26.5) = %q, %v", got, err)
	}
	got, err = h.PackageDir("go", "v1.2.3")
	if err != nil || got != "/x/nem/packages/go/v1.2.3" {
		t.Fatalf("PackageDir(go, v1.2.3) = %q, %v", got, err)
	}
}

func TestPackageDirRejectsPathEscape(t *testing.T) {
	h := testHome(t)
	if _, err := h.PackageDir("go", "../../x"); err == nil {
		t.Fatal("PackageDir: path escape via version must error")
	}
	if _, err := h.PackageDir("../go", "v1.2.3"); err == nil {
		t.Fatal("PackageDir: path escape via name must error")
	}
	if _, err := h.PackageDir("go/x", "v1.2.3"); err == nil {
		t.Fatal("PackageDir: separator in name must error")
	}
}

func TestCatalogStore(t *testing.T) {
	h := testHome(t)
	got, err := h.CatalogStore("official")
	if err != nil || got != "/x/nem/catalogs/official/store" {
		t.Fatalf("CatalogStore(official) = %q, %v", got, err)
	}
	if _, err := h.CatalogStore("../evil"); err == nil {
		t.Fatal("CatalogStore: path escape must error")
	}
}

func TestCatalogDir(t *testing.T) {
	h := testHome(t)
	got, err := h.CatalogDir("dev")
	if err != nil || got != "/x/nem/catalogs/dev" {
		t.Fatalf("CatalogDir(dev) = %q, %v", got, err)
	}
}

func TestCatalogDirRejectsBadName(t *testing.T) {
	h := testHome(t)
	for _, bad := range []string{"../evil", ".hidden", "a/b", ""} {
		if _, err := h.CatalogDir(bad); err == nil {
			t.Errorf("CatalogDir(%q) should error", bad)
		}
	}
}
