package project

import (
	"bytes"
	"fmt"
	"os"
	"sort"

	"github.com/pelletier/go-toml/v2"

	"github.com/vi-dev/nem-cli/internal/fsx"
)

// AddTool inserts or updates a tool entry; reports whether anything changed.
func AddTool(m *Manifest, key ToolKey, version string) bool {
	for i, t := range m.Tools {
		if t.Key.Name == key.Name {
			if t.Key == key && t.Version == version {
				return false
			}
			m.Tools[i] = ToolEntry{Key: key, Version: version}
			return true
		}
	}
	m.Tools = append(m.Tools, ToolEntry{Key: key, Version: version})
	sort.Slice(m.Tools, func(i, j int) bool { return m.Tools[i].Key.Name < m.Tools[j].Key.Name })
	return true
}

// RemoveTool deletes the entry whose package name matches.
func RemoveTool(m *Manifest, name string) bool {
	for i, t := range m.Tools {
		if t.Key.Name == name {
			m.Tools = append(m.Tools[:i], m.Tools[i+1:]...)
			return true
		}
	}
	return false
}

// WriteManifest renders the manifest and writes it atomically, skipping the
// write when on-disk content is already identical.
func WriteManifest(m *Manifest) error {
	rendered, err := renderManifest(m)
	if err != nil {
		return err
	}
	if existing, err := os.ReadFile(m.Path); err == nil && bytes.Equal(existing, rendered) {
		return nil
	}
	return fsx.WriteAtomic(m.Path, rendered, 0o644)
}

func renderManifest(m *Manifest) ([]byte, error) {
	raw := rawManifest{Tools: map[string]string{}, Env: map[string]string{}}
	for _, t := range m.Tools {
		raw.Tools[t.Key.String()] = t.Version
	}
	for _, e := range m.Env {
		raw.Env[e.Name] = e.Value
	}
	out, err := toml.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("render %s: %w", m.Path, err)
	}
	return out, nil
}
