package spec

import (
	"fmt"
	"regexp"
	"strings"
)

// NameRE validates package names: a single flat lowercase segment.
var NameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// Ref names a package, optionally at an exact version.
type Ref struct{ Name, Version string }

// ParseRef parses "name" or "name@version". "name@" is invalid.
func ParseRef(s string) (Ref, error) {
	name, version, found := strings.Cut(s, "@")
	if !NameRE.MatchString(name) {
		return Ref{}, fmt.Errorf("invalid package name %q", name)
	}
	if found && version == "" {
		return Ref{}, fmt.Errorf("invalid ref %q: empty version after @", s)
	}
	return Ref{Name: name, Version: version}, nil
}

func (r Ref) String() string {
	if r.Version == "" {
		return r.Name
	}
	return r.Name + "@" + r.Version
}
