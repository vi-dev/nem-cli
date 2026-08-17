package resolve

import "testing"

func TestHigherOrdersNonSemverNumerically(t *testing.T) {
	cases := []struct {
		candidate, current string
		want               bool
	}{
		{"1.2.4", "1.2.3", true},  // plain semver still ranks
		{"v1.2.4", "1.2.3", true}, // mixed spelling
		{"1.2.3-rc1", "1.2.3", false},
		{"25.0.4_10", "25.0.4_9", true}, // numeric build suffix, not lexical
		{"2.10", "2.8", true},           // two-part versions rank numerically
		{"1.2.3.4", "1.2.3", true},      // four-part versions rank
	}
	for _, c := range cases {
		if got := higher(c.candidate, c.current); got != c.want {
			t.Errorf("higher(%q, %q) = %v, want %v", c.candidate, c.current, got, c.want)
		}
	}
}
