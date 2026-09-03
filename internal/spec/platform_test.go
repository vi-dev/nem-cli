package spec

import "testing"

func TestParsePlatform(t *testing.T) {
	cases := []struct {
		in      string
		os, arc string
		ok      bool
	}{
		{"linux/amd64", "linux", "amd64", true},
		{"darwin/arm64", "darwin", "arm64", true},
		{"linux", "linux", "", true},
		{"windows/amd64", "", "", false},
		{"linux/mips", "", "", false},
		{"", "", "", false},
	}
	for _, c := range cases {
		p, err := ParsePlatform(c.in)
		if c.ok != (err == nil) {
			t.Errorf("%q: err=%v, want ok=%v", c.in, err, c.ok)
			continue
		}
		if c.ok && (p.OS != c.os || p.Arch != c.arc) {
			t.Errorf("%q: got %v", c.in, p)
		}
	}
}

func TestPlatformMatches(t *testing.T) {
	full, _ := ParsePlatform("linux/amd64")
	osOnly, _ := ParsePlatform("linux")
	if !osOnly.Matches(full) {
		t.Error("linux should match linux/amd64")
	}
	if !full.Matches(full) {
		t.Error("exact should match")
	}
	other, _ := ParsePlatform("darwin/arm64")
	if osOnly.Matches(other) {
		t.Error("linux should not match darwin/arm64")
	}
}

func TestPlatformsInclude(t *testing.T) {
	target := Platform{OS: "linux", Arch: "amd64"}
	if !PlatformsInclude(nil, target) {
		t.Error("empty list should include every platform")
	}
	if !PlatformsInclude([]Platform{{OS: "darwin"}, {OS: "linux"}}, target) {
		t.Error("os-only constraint should cover linux/amd64")
	}
	if !PlatformsInclude([]Platform{{OS: "linux", Arch: "amd64"}}, target) {
		t.Error("exact constraint should cover linux/amd64")
	}
	if PlatformsInclude([]Platform{{OS: "darwin"}, {OS: "linux", Arch: "arm64"}}, target) {
		t.Error("disjoint constraints should not cover linux/amd64")
	}
}

func TestSupported(t *testing.T) {
	if len(SupportedPlatforms) != 4 {
		t.Fatalf("want 4 supported platforms, got %d", len(SupportedPlatforms))
	}
}
