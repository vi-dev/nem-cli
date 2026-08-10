// Package catalog manages catalog configuration and package sources.
package catalog

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/goccy/go-yaml"

	"github.com/vi-dev/nem-cli/internal/fsx"
	"github.com/vi-dev/nem-cli/internal/home"
	"github.com/vi-dev/nem-cli/internal/spec"
)

// OfficialRef is the catalog written into config.yaml on first run.
const OfficialRef = "ghcr.io/vi-dev/nem-official-catalog:v2"

type Entry struct {
	Name     string `yaml:"name"`
	Type     string `yaml:"type"`
	Ref      string `yaml:"ref,omitempty"`
	Path     string `yaml:"path,omitempty"`
	Disabled bool   `yaml:"disabled,omitempty"`
}

type Config struct {
	Catalogs []Entry `yaml:"catalogs"`
}

// OpenConfig loads config.yaml, writing the default (official) config first
// when the file does not exist.
func OpenConfig(h home.Home) (*Config, error) {
	data, err := os.ReadFile(h.Config())
	if os.IsNotExist(err) {
		cfg := &Config{Catalogs: []Entry{{Name: "official", Type: "oci", Ref: OfficialRef}}}
		if err := SaveConfig(h, cfg); err != nil {
			return nil, err
		}
		return cfg, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", h.Config(), err)
	}
	var cfg Config
	if err := yaml.UnmarshalWithOptions(data, &cfg, yaml.Strict()); err != nil {
		return nil, fmt.Errorf("parse %s: %w", h.Config(), err)
	}
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("parse %s: %w", h.Config(), err)
	}
	return &cfg, nil
}

// SaveConfig validates and persists config to config.yaml using atomic writes.
func SaveConfig(h home.Home, cfg *Config) error {
	if err := cfg.validate(); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("render config: %w", err)
	}
	if err := fsx.WriteAtomic(h.Config(), data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", h.Config(), err)
	}
	return nil
}

func (c *Config) validate() error {
	seen := map[string]bool{}
	for i, e := range c.Catalogs {
		if !spec.NameRE.MatchString(e.Name) {
			return fmt.Errorf("catalogs[%d]: invalid name %q", i, e.Name)
		}
		if seen[e.Name] {
			return fmt.Errorf("catalogs[%d]: duplicate name %q", i, e.Name)
		}
		seen[e.Name] = true
		switch e.Type {
		case "oci":
			if e.Ref == "" {
				return fmt.Errorf("catalog %s: oci catalogs require ref", e.Name)
			}
			if e.Path != "" {
				return fmt.Errorf("catalog %s: oci catalogs must not set path", e.Name)
			}
		case "dir":
			if e.Path == "" {
				return fmt.Errorf("catalog %s: dir catalogs require path", e.Name)
			}
			if e.Ref != "" {
				return fmt.Errorf("catalog %s: dir catalogs must not set ref", e.Name)
			}
			if !filepath.IsAbs(e.Path) {
				return fmt.Errorf("catalog %s: path must be absolute", e.Name)
			}
		default:
			return fmt.Errorf("catalog %s: unknown type %q", e.Name, e.Type)
		}
	}
	return nil
}

// Find returns the entry with the given name, or nil if not found.
// The returned pointer is valid only until the next mutation of the config (append, Reorder);
// do not hold it across mutations.
func (c *Config) Find(name string) *Entry {
	for i := range c.Catalogs {
		if c.Catalogs[i].Name == name {
			return &c.Catalogs[i]
		}
	}
	return nil
}

// Reorder rewrites the catalog order; names must be an exact permutation of
// every configured catalog. It replaces the backing slice, invalidating any
// pointers previously returned by Find.
func (c *Config) Reorder(names []string) error {
	if len(names) != len(c.Catalogs) {
		return fmt.Errorf("reorder must list every catalog exactly once (%d configured, %d given)", len(c.Catalogs), len(names))
	}
	out := make([]Entry, 0, len(names))
	used := map[string]bool{}
	for _, n := range names {
		if used[n] {
			return fmt.Errorf("reorder lists %q twice", n)
		}
		used[n] = true
		e := c.Find(n)
		if e == nil {
			return fmt.Errorf("unknown catalog %q", n)
		}
		out = append(out, *e)
	}
	c.Catalogs = slices.Clip(out)
	return nil
}
