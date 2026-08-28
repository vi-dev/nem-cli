// Package config holds nem's config.yaml document model: configured
// catalogs and per-host connection settings, with strict load/save for
// commands that read it and a lenient loader for the root command hook.
package config

import (
	"errors"
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

// CatalogEntry declares one configured catalog, listed under config.yaml's
// catalogs: key.
type CatalogEntry struct {
	Name     string `yaml:"name"`
	Type     string `yaml:"type"`
	Ref      string `yaml:"ref,omitempty"`
	Path     string `yaml:"path,omitempty"`
	Disabled bool   `yaml:"disabled,omitempty"`
}

// HostEntry declares one host's connection settings — trust today,
// credentials planned — as listed under config.yaml's hosts: key. Exactly
// one of CA, PlainHTTP, or Insecure applies.
type HostEntry struct {
	Host      string `yaml:"host"`
	CA        string `yaml:"ca,omitempty"`
	PlainHTTP bool   `yaml:"plainHTTP,omitempty"`
	Insecure  bool   `yaml:"insecure,omitempty"`
}

type Config struct {
	Catalogs []CatalogEntry `yaml:"catalogs"`
	Hosts    []HostEntry    `yaml:"hosts,omitempty"`
}

// OpenConfig loads config.yaml, writing the default (official) config first
// when the file does not exist.
func OpenConfig(h home.Home) (*Config, error) {
	data, err := os.ReadFile(h.Config())
	if os.IsNotExist(err) {
		cfg := &Config{Catalogs: []CatalogEntry{{Name: "official", Type: "oci", Ref: OfficialRef}}}
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
	for i, r := range c.Hosts {
		if err := validateHostEntry(r); err != nil {
			return fmt.Errorf("hosts[%d]: %w", i, err)
		}
	}
	return nil
}

// validateHostEntry: a host repeated across entries is not an error here
// — LoadHostSettingsLenient resolves repeats by last-entry-wins.
func validateHostEntry(r HostEntry) error {
	if r.Host == "" {
		return errors.New("host is required")
	}
	n := 0
	if r.CA != "" {
		n++
	}
	if r.PlainHTTP {
		n++
	}
	if r.Insecure {
		n++
	}
	if n != 1 {
		return errors.New("exactly one of ca, plainHTTP, or insecure is required")
	}
	if r.CA != "" && !filepath.IsAbs(r.CA) {
		return errors.New("ca must be an absolute path")
	}
	return nil
}

// LoadHostSettingsLenient reads config.yaml's hosts: list for the root
// command hook, without ever failing the command: unknown top-level keys
// are ignored, each invalid entry is dropped with its own warning, an
// unreadable or unparsable file yields zero entries and one warning, and
// a missing file (the normal pre-first-run state) yields neither.
func LoadHostSettingsLenient(h home.Home) (map[string]HostEntry, []string) {
	data, err := os.ReadFile(h.Config())
	switch {
	case os.IsNotExist(err):
		return nil, nil
	case err != nil:
		return nil, []string{fmt.Sprintf("host settings: read %s: %v (no host settings applied)", h.Config(), err)}
	}

	var raw struct {
		Hosts []HostEntry `yaml:"hosts"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, []string{fmt.Sprintf("host settings: parse %s: %v (no host settings applied)", h.Config(), err)}
	}

	entries := make(map[string]HostEntry, len(raw.Hosts))
	var warnings []string
	for i, r := range raw.Hosts {
		if err := validateHostEntry(r); err != nil {
			warnings = append(warnings, fmt.Sprintf("host settings: hosts[%d] (host %q): %v (entry ignored)", i, r.Host, err))
			continue
		}
		entries[r.Host] = r
	}
	return entries, warnings
}

// Find returns the entry with the given name, or nil if not found.
// The returned pointer is valid only until the next mutation of the config (append, Reorder);
// do not hold it across mutations.
func (c *Config) Find(name string) *CatalogEntry {
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
	out := make([]CatalogEntry, 0, len(names))
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
