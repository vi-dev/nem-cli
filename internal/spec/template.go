package spec

import (
	"fmt"
	"strings"
	"text/template"
)

type templateCtx struct {
	Version string
	OS      string
	Arch    string
}

var helperFuncs = template.FuncMap{
	"trimPrefix": func(s, prefix string) string { return strings.TrimPrefix(s, prefix) },
	"trimSuffix": func(s, suffix string) string { return strings.TrimSuffix(s, suffix) },
	"replace":    func(s, old, new string) string { return strings.ReplaceAll(s, old, new) },
}

func expand(tmpl string, ctx templateCtx) (string, error) {
	t, err := template.New("").Funcs(helperFuncs).Option("missingkey=error").Parse(tmpl)
	if err != nil {
		return "", fmt.Errorf("parse template %q: %w", tmpl, err)
	}
	var b strings.Builder
	if err := t.Execute(&b, ctx); err != nil {
		return "", fmt.Errorf("expand template %q: %w", tmpl, err)
	}
	return b.String(), nil
}

func (p *Package) ArtifactURL(version string, plat Platform) (string, error) {
	return expand(p.Artifact.URL, templateCtx{Version: version, OS: plat.OS, Arch: plat.Arch})
}

func (p *Package) AssetName(version string, plat Platform) (string, error) {
	if p.Artifact.GitHub == nil {
		return "", fmt.Errorf("package %s has no github artifact", p.Name)
	}
	return expand(p.Artifact.GitHub.Asset, templateCtx{Version: version, OS: plat.OS, Arch: plat.Arch})
}

// Sha256 returns the pinned checksum for (version, platform).
func (p *Package) Sha256(version string, plat Platform) (string, error) {
	for _, v := range p.Versions {
		if v.Version != version {
			continue
		}
		sum := v.Sha256[plat.String()]
		if sum == "" {
			return "", fmt.Errorf("no sha256 for %s@%s on %s", p.Name, version, plat)
		}
		return sum, nil
	}
	return "", fmt.Errorf("version %s not offered by %s", version, p.Name)
}
