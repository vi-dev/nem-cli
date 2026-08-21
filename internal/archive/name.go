package archive

import (
	"net/url"
	"path"
	"strings"
)

// SingleNameFromRef derives the output name a compressed single-file
// artifact extracts to from the URL or file name it was fetched as: the
// path basename minus one single-stream compression extension. It returns
// "" when no meaningful basename can be derived.
func SingleNameFromRef(ref string) string {
	u, err := url.Parse(ref)
	if err != nil {
		return ""
	}
	base := path.Base(u.Path)
	if base == "." || base == "/" {
		return ""
	}
	for _, ext := range []string{".gz", ".bz2", ".xz", ".zst"} {
		if trimmed, ok := strings.CutSuffix(base, ext); ok {
			return trimmed
		}
	}
	return base
}
