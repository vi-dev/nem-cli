// Package project reads and writes nem.toml and nem.lock.
package project

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/vi-dev/nem-cli/internal/spec"
)

// ToolKey is a [tools] key: an optional catalog pin plus the package name.
type ToolKey struct{ Catalog, Name string }

func ParseToolKey(s string) (ToolKey, error) {
	catalog, name, found := strings.Cut(s, ":")
	if !found {
		catalog, name = "", catalog
	}
	if found && !spec.NameRE.MatchString(catalog) {
		return ToolKey{}, fmt.Errorf("invalid catalog name %q in tool key %q", catalog, s)
	}
	if !spec.NameRE.MatchString(name) {
		return ToolKey{}, fmt.Errorf("invalid package name %q in tool key %q", name, s)
	}
	return ToolKey{Catalog: catalog, Name: name}, nil
}

func (k ToolKey) String() string {
	if k.Catalog == "" {
		return k.Name
	}
	return k.Catalog + ":" + k.Name
}

type ToolEntry struct {
	Key     ToolKey
	Version string
}

type EnvVar struct{ Name, Value string }

type Manifest struct {
	Path  string // file path this was loaded from
	Tools []ToolEntry
	Env   []EnvVar
}

type rawManifest struct {
	Tools map[string]string `toml:"tools"`
	Env   map[string]string `toml:"env"`
}

// LoadManifest reads a nem.toml. A missing file is an empty manifest.
func LoadManifest(path string) (*Manifest, error) {
	m := &Manifest{Path: path}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return m, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var raw rawManifest
	d := toml.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&raw); err != nil {
		return nil, wrapTOMLError(path, err)
	}
	seen := map[string]string{} // package name → full key
	for k, v := range raw.Tools {
		key, err := ParseToolKey(k)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		if prev, dup := seen[key.Name]; dup {
			return nil, fmt.Errorf("parse %s: package %q declared twice (%q and %q)", path, key.Name, prev, k)
		}
		seen[key.Name] = k
		m.Tools = append(m.Tools, ToolEntry{Key: key, Version: v})
	}
	sort.Slice(m.Tools, func(i, j int) bool { return m.Tools[i].Key.Name < m.Tools[j].Key.Name })
	for k, v := range raw.Env {
		if !spec.EnvNameRE.MatchString(k) {
			return nil, fmt.Errorf("parse %s: invalid env var name %q", path, k)
		}
		m.Env = append(m.Env, EnvVar{Name: k, Value: v})
	}
	sort.Slice(m.Env, func(i, j int) bool { return m.Env[i].Name < m.Env[j].Name })
	return m, nil
}

// wrapTOMLError wraps a TOML decode error, naming the offending key when the
// failure came from DisallowUnknownFields strict mode.
func wrapTOMLError(path string, err error) error {
	var strictErr *toml.StrictMissingError
	if errors.As(err, &strictErr) && len(strictErr.Errors) > 0 {
		return fmt.Errorf("parse %s: unknown key %v: %w", path, strictErr.Errors[0].Key(), err)
	}
	return fmt.Errorf("parse %s: %w", path, err)
}
