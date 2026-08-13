package spec

import (
	"errors"
	"fmt"
	"regexp"
)

// EnvNameRE validates environment variable names.
var EnvNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// CompatRE validates a dep's soname-compat range: a dotted numeric prefix.
var CompatRE = regexp.MustCompile(`^\d+(\.\d+)*$`)

// SupportedBy expands the package's platform constraint to full os/arch
// pairs: the declared subset, or all four when unconstrained.
func (p *Package) SupportedBy() []Platform {
	if len(p.Platforms) == 0 {
		return Supported
	}
	var out []Platform
	for _, sup := range Supported {
		for _, c := range p.Platforms {
			if c.Matches(sup) {
				out = append(out, sup)
				break
			}
		}
	}
	return out
}

// Validate checks schema rules beyond YAML shape. First violation wins.
func (p *Package) Validate() error {
	if p.Schema != 2 {
		return fmt.Errorf("schema must be 2, got %d", p.Schema)
	}
	if !NameRE.MatchString(p.Name) {
		return fmt.Errorf("invalid name %q", p.Name)
	}
	fetchers := 0
	if p.Artifact.URL != "" {
		fetchers++
	}
	if p.Artifact.GitHub != nil {
		fetchers++
	}
	if p.Artifact.OCI != "" {
		fetchers++
	}
	if fetchers != 1 {
		return errors.New("artifact must set exactly one of url, github, oci")
	}
	if len(p.Install) == 0 {
		return errors.New("install is required and must be non-empty")
	}
	for i, a := range p.Install {
		switch {
		case a.Copy != nil:
			if a.Copy.Src == "" {
				return fmt.Errorf("install[%d]: copy requires src", i)
			}
			if a.Copy.Dst == "" {
				return fmt.Errorf("install[%d]: copy requires dst", i)
			}
		case a.Move != nil:
			if a.Move.Src == "" {
				return fmt.Errorf("install[%d]: move requires src", i)
			}
			if a.Move.Dst == "" {
				return fmt.Errorf("install[%d]: move requires dst", i)
			}
		case a.Extract == nil && a.Mkdir == "":
			return fmt.Errorf("install[%d]: empty action", i)
		}
	}
	if len(p.Versions) == 0 {
		return errors.New("versions is required and must be non-empty")
	}
	for i, v := range p.Versions {
		if v.Version == "" {
			return fmt.Errorf("versions[%d]: missing version", i)
		}
		if v.Sha256 == nil {
			if p.Artifact.OCI == "" {
				return fmt.Errorf("versions[%d] (%s): sha256 required for url/github artifacts", i, v.Version)
			}
			continue
		}
		for _, plat := range p.SupportedBy() {
			if v.Sha256[plat.String()] == "" {
				return fmt.Errorf("versions[%d] (%s): sha256 missing platform %s", i, v.Version, plat)
			}
		}
	}
	if d := p.VersionDiscovery; d != nil {
		sources := 0
		if d.GitHub != nil {
			sources++
		}
		if d.OCI != "" {
			sources++
		}
		if sources != 1 {
			return errors.New("versionDiscovery must set exactly one of github, oci")
		}
		if d.GitHub != nil && d.GitHub.Filter != "" {
			if _, err := regexp.Compile(d.GitHub.Filter); err != nil {
				return fmt.Errorf("versionDiscovery filter: %w", err)
			}
		}
	}
	for _, e := range p.Env {
		if !EnvNameRE.MatchString(e.Name) {
			return fmt.Errorf("env export %q: invalid name", e.Name)
		}
	}
	for _, d := range p.Deps {
		if !NameRE.MatchString(d.Name) {
			return fmt.Errorf("dep %q: invalid name", d.Name)
		}
		if d.Compat != "" && d.Kind != DepKindLink {
			return fmt.Errorf("dep %q: compat requires kind: link", d.Name)
		}
		if d.Compat != "" && !CompatRE.MatchString(d.Compat) {
			return fmt.Errorf("dep %q: invalid compat %q", d.Name, d.Compat)
		}
	}
	return nil
}
