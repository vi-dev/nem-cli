// Package spec defines the pkg.yaml v2 schema and nem's shared vocabulary:
// package refs and platforms.
package spec

import (
	"fmt"
	"runtime"
	"strings"
)

// Platform is an os/arch pair. Arch may be empty in constraints, meaning
// "any supported arch of this OS".
type Platform struct{ OS, Arch string }

// SupportedPlatforms is the exact platform matrix nem targets.
var SupportedPlatforms = []Platform{
	{"darwin", "arm64"}, {"darwin", "amd64"},
	{"linux", "arm64"}, {"linux", "amd64"},
}

func (p Platform) String() string {
	if p.Arch == "" {
		return p.OS
	}
	return p.OS + "/" + p.Arch
}

// Matches reports whether p (possibly OS-only) covers full.
func (p Platform) Matches(full Platform) bool {
	return p.OS == full.OS && (p.Arch == "" || p.Arch == full.Arch)
}

// Current returns the running platform.
func Current() Platform { return Platform{OS: runtime.GOOS, Arch: runtime.GOARCH} }

// PlatformsInclude reports whether a platform constraint list (empty =
// unconstrained) covers p.
func PlatformsInclude(list []Platform, p Platform) bool {
	if len(list) == 0 {
		return true
	}
	for _, c := range list {
		if c.Matches(p) {
			return true
		}
	}
	return false
}

// ParsePlatform parses "os/arch" or a bare "os" constraint, restricted to
// the supported matrix.
func ParsePlatform(s string) (Platform, error) {
	osPart, arch, _ := strings.Cut(s, "/")
	p := Platform{OS: osPart, Arch: arch}
	for _, sup := range SupportedPlatforms {
		if p.Matches(sup) {
			return p, nil
		}
	}
	return Platform{}, fmt.Errorf("unsupported platform %q", s)
}
