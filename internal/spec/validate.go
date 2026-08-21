package spec

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
)

// EnvNameRE validates environment variable names.
var EnvNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// CompatRE validates a dep's soname-compat range: a dotted numeric prefix.
var CompatRE = regexp.MustCompile(`^\d+(\.\d+)*$`)

// TagRE validates versions against the OCI tag grammar: a version doubles
// as the tag on the catalog's archives repo (<base>/archives/<name>:<version>),
// so characters like "+" would break every registry fetch.
var TagRE = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9._-]{0,127}$`)

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

func validateHTTPURL(s string) error {
	u, err := url.Parse(s)
	if err != nil {
		return err
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("must be an http(s) URL, got %q", s)
	}
	return nil
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
		if _, err := ExpandActionPaths(a, "v0", Platform{OS: "darwin", Arch: "arm64"}); err != nil {
			return fmt.Errorf("install[%d]: %w", i, err)
		}
	}
	if len(p.Versions) == 0 {
		return errors.New("versions is required and must be non-empty")
	}
	for i, v := range p.Versions {
		if v.Version == "" {
			return fmt.Errorf("versions[%d]: missing version", i)
		}
		if !TagRE.MatchString(v.Version) {
			return fmt.Errorf("versions[%d] (%s): version is not a valid oci tag (it names the package's archive tag)", i, v.Version)
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
		sources, filter := 0, ""
		if d.GitHub != nil {
			sources++
			filter = d.GitHub.Filter
			if d.GitHub.Repo == "" {
				return errors.New("versionDiscovery github requires repo")
			}
		}
		if d.GitLab != nil {
			sources++
			filter = d.GitLab.Filter
			if d.GitLab.Repo == "" {
				return errors.New("versionDiscovery gitlab requires repo")
			}
		}
		if d.Git != nil {
			sources++
			filter = d.Git.Filter
			if err := validateHTTPURL(d.Git.URL); err != nil {
				return fmt.Errorf("versionDiscovery git url: %w", err)
			}
		}
		if d.HTTP != nil {
			sources++
			filter = d.HTTP.Filter
			if err := validateHTTPURL(d.HTTP.URL); err != nil {
				return fmt.Errorf("versionDiscovery http url: %w", err)
			}
			if d.HTTP.Filter == "" {
				return errors.New("versionDiscovery http requires filter")
			}
		}
		if d.OCI != "" {
			sources++
		}
		if sources != 1 {
			return errors.New("versionDiscovery must set exactly one of github, gitlab, git, http, oci")
		}
		if filter != "" {
			if _, err := regexp.Compile(filter); err != nil {
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
	if b := p.Build; b != nil {
		if b.Source.URL == "" {
			return errors.New("build.source.url is required")
		}
		if _, err := expand(b.Source.URL, templateCtx{Version: "v0", OS: "darwin", Arch: "arm64"}); err != nil {
			return fmt.Errorf("build.source.url: %w", err)
		}
		if b.Output == "" {
			return errors.New("build.output is required")
		}
		if len(b.Steps) == 0 {
			return errors.New("build.steps is required and must be non-empty")
		}
		for i, s := range b.Steps {
			if s.Run == "" {
				return fmt.Errorf("build.steps[%d]: run is required", i)
			}
		}
		for i, d := range b.Deps {
			if !NameRE.MatchString(d.Name) {
				return fmt.Errorf("build.deps[%d]: invalid name %q", i, d.Name)
			}
			if d.Compat != "" && d.Kind != DepKindLink {
				return fmt.Errorf("build.deps[%d] (%s): compat requires kind: link", i, d.Name)
			}
			if d.Compat != "" && !CompatRE.MatchString(d.Compat) {
				return fmt.Errorf("build.deps[%d] (%s): invalid compat %q", i, d.Name, d.Compat)
			}
		}
	}
	return nil
}
