package clean

import (
	"strings"
	"testing"
	"time"
)

func TestAgeSetValid(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"30d", 30 * 24 * time.Hour},
		{"1d", 24 * time.Hour},
		{"12h", 12 * time.Hour},
		{"1h", time.Hour},
		{"365d", 365 * 24 * time.Hour},
		{"36500d", 36500 * 24 * time.Hour},
		{"876000h", 876000 * time.Hour},
	}
	for _, c := range cases {
		var a Age
		if err := a.Set(c.in); err != nil {
			t.Errorf("Set(%q): unexpected error %v", c.in, err)
			continue
		}
		if a.Duration() != c.want {
			t.Errorf("Set(%q).Duration() = %v, want %v", c.in, a.Duration(), c.want)
		}
	}
}

func TestAgeSetInvalid(t *testing.T) {
	for _, in := range []string{"30", "30m", "45s", "1d6h", "0h", "0d", "-1d", "", "d", "h", "1.5d", "30D"} {
		var a Age
		err := a.Set(in)
		if err == nil {
			t.Errorf("Set(%q) should have failed", in)
			continue
		}
		if !strings.Contains(err.Error(), "30d") {
			t.Errorf("Set(%q) error must name the accepted shape, got %q", in, err)
		}
	}
}

// TestAgeSetRejectsOverflow covers the band where days times an hour
// count wraps int64 back into a small positive duration. 213504d wraps to
// roughly 25 minutes, so accepting it would have evicted every stamped
// version rather than none.
func TestAgeSetRejectsOverflow(t *testing.T) {
	for _, in := range []string{"36501d", "213504d", "213505d", "876001h", "999999999d"} {
		var a Age
		err := a.Set(in)
		if err == nil {
			t.Errorf("Set(%q) = %v, want an error", in, a.Duration())
			continue
		}
		if !strings.Contains(err.Error(), "30d") {
			t.Errorf("Set(%q) error must name the accepted shape, got %q", in, err)
		}
		if a.Duration() != 0 {
			t.Errorf("Set(%q) left Duration() = %v; a rejected value must set nothing", in, a.Duration())
		}
	}
}

func TestAgeStringRoundTrips(t *testing.T) {
	var a Age
	if got := a.String(); got != "" {
		t.Fatalf("zero Age String() = %q, want empty", got)
	}
	if err := a.Set("30d"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got := a.String(); got != "30d" {
		t.Fatalf("String() = %q, want %q", got, "30d")
	}
}

func TestAgeType(t *testing.T) {
	var a Age
	if got := a.Type(); got != "days|hours" {
		t.Fatalf("Type() = %q, want %q", got, "days|hours")
	}
}
