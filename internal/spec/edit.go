package spec

import (
	"fmt"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
)

// InsertVersion returns data with e prepended to the top-level versions
// sequence (newest-first).
func InsertVersion(data []byte, e VersionEntry) ([]byte, error) {
	return InsertVersionAt(data, e, 0)
}

// InsertVersionAt returns data with e inserted into the top-level
// versions sequence at pos (0 = head, len = append at the tail). Input
// should already be in Format's canonical form; the output is canonical,
// so the textual diff is exactly the inserted lines.
func InsertVersionAt(data []byte, e VersionEntry, pos int) ([]byte, error) {
	f, err := parser.ParseBytes(data, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}
	seq, err := locateVersions(f)
	if err != nil {
		return nil, err
	}
	if pos < 0 || pos > len(seq.Values) {
		return nil, fmt.Errorf("insert position %d out of range [0,%d]", pos, len(seq.Values))
	}

	snippet := "versions:\n" + renderVersionEntry(e)
	sf, err := parser.ParseBytes([]byte(snippet), 0)
	if err != nil {
		return nil, fmt.Errorf("render new entry: %w", err)
	}
	p, err := yaml.PathString("$.versions")
	if err != nil {
		return nil, fmt.Errorf("versions path: %w", err)
	}
	sNode, err := p.FilterFile(sf)
	if err != nil {
		return nil, fmt.Errorf("locate rendered entry: %w", err)
	}
	newSeq, ok := sNode.(*ast.SequenceNode)
	if !ok || len(newSeq.Values) != 1 {
		return nil, fmt.Errorf("render new entry: unexpected shape")
	}

	vals := make([]ast.Node, 0, len(seq.Values)+1)
	vals = append(vals, seq.Values[:pos]...)
	vals = append(vals, newSeq.Values[0])
	vals = append(vals, seq.Values[pos:]...)
	seq.Values = vals
	out := f.String()
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return []byte(out), nil
}

// ValidateEditable reports whether data's versions sequence can accept
// InsertVersionAt edits, so callers can fail before doing expensive work.
func ValidateEditable(data []byte) error {
	f, err := parser.ParseBytes(data, parser.ParseComments)
	if err != nil {
		return fmt.Errorf("parse yaml: %w", err)
	}
	_, err = locateVersions(f)
	return err
}

// locateVersions finds the document's top-level versions sequence,
// rejecting flow style — grafting a block-style entry into a flow
// sequence renders invalid YAML.
func locateVersions(f *ast.File) (*ast.SequenceNode, error) {
	p, err := yaml.PathString("$.versions")
	if err != nil {
		return nil, fmt.Errorf("versions path: %w", err)
	}
	node, err := p.FilterFile(f)
	if err != nil {
		return nil, fmt.Errorf("locate versions: %w", err)
	}
	seq, ok := node.(*ast.SequenceNode)
	if !ok || seq.IsFlowStyle {
		return nil, fmt.Errorf("versions is not a block sequence")
	}
	return seq, nil
}

// renderVersionEntry renders e as block-style YAML indented to sit
// directly under a top-level "versions:" key. Platform keys follow the
// SupportedPlatforms order; hash values are quoted.
func renderVersionEntry(e VersionEntry) string {
	var b strings.Builder
	if len(e.Sha256) == 0 && e.SourceSha256 == "" {
		fmt.Fprintf(&b, "  - %s\n", e.Version)
		return b.String()
	}
	fmt.Fprintf(&b, "  - version: %s\n", e.Version)
	if e.SourceSha256 != "" {
		fmt.Fprintf(&b, "    sourceSha256: %q\n", e.SourceSha256)
	}
	if len(e.Sha256) > 0 {
		b.WriteString("    sha256:\n")
		for _, p := range SupportedPlatforms {
			if sum, ok := e.Sha256[p.String()]; ok {
				fmt.Fprintf(&b, "      %s: %q\n", p, sum)
			}
		}
	}
	return b.String()
}
