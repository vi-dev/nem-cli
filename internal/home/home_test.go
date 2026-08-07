package home

import "testing"

func TestResolve(t *testing.T) {
	h := Resolve(func(k string) string {
		if k == "NEM_HOME" {
			return "/x/nem"
		}
		return ""
	})
	cases := []struct{ got, want string }{
		{h.Config(), "/x/nem/config.yaml"},
		{h.GlobalManifest(), "/x/nem/nem.toml"},
		{h.GlobalLock(), "/x/nem/nem.lock"},
		{h.LockFile(), "/x/nem/lock"},
		{h.Tmp(), "/x/nem/tmp"},
		{h.PackageDir("go", "v1.26.5"), "/x/nem/packages/go/v1.26.5"},
		{h.CatalogStore("official"), "/x/nem/catalogs/official/store"},
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
