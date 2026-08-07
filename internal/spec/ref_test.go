package spec

import "testing"

func TestParseRef(t *testing.T) {
	cases := []struct {
		in      string
		name, v string
		ok      bool
	}{
		{"go@v1.26.5", "go", "v1.26.5", true},
		{"go", "go", "", true},
		{"go@", "", "", false},
		{"@v1", "", "", false},
		{"Go", "", "", false},   // uppercase name
		{"-bad", "", "", false}, // bad lead char
		{"node-lts", "node-lts", "", true},
	}
	for _, c := range cases {
		r, err := ParseRef(c.in)
		if c.ok != (err == nil) {
			t.Errorf("%q: err=%v, want ok=%v", c.in, err, c.ok)
			continue
		}
		if c.ok && (r.Name != c.name || r.Version != c.v) {
			t.Errorf("%q: got %+v", c.in, r)
		}
	}
}
