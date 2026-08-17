package spec

import (
	"fmt"
	"strings"

	"github.com/goccy/go-yaml/parser"
)

// Format renders a pkg.yaml in canonical form: the goccy AST round-trip
// with comments preserved and a guaranteed trailing newline. Canonical
// form is defined as Format's own output, so Format(Format(x)) == Format(x)
// is the correctness contract, not equality with some external layout.
func Format(data []byte) ([]byte, error) {
	f, err := parser.ParseBytes(data, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}
	out := f.String()
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return []byte(out), nil
}
