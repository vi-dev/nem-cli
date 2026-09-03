package resolve

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/vi-dev/nem-cli/internal/spec"
)

// CompatConflictError reports a package's irreconcilable requirements —
// compat ranges or exact versions, deduplicated and canonically ordered.
type CompatConflictError struct {
	Name    string
	Compats []string
}

func (e *CompatConflictError) Error() string {
	return fmt.Sprintf("package %s: conflicting requirements %s", e.Name, strings.Join(e.Compats, ", "))
}

// newCompatConflictError deduplicates and canonically orders the
// requirements — by version/range, then text — so the same conflict
// reports identically for every walk order.
func newCompatConflictError(name string, compats ...string) *CompatConflictError {
	sorted := slices.Clone(compats)
	sort.Slice(sorted, func(i, j int) bool {
		if c := spec.CompareVersions(sorted[i], sorted[j]); c != 0 {
			return c < 0
		}
		return sorted[i] < sorted[j]
	})
	return &CompatConflictError{Name: name, Compats: slices.Compact(sorted)}
}

// compatComponents splits a version or compat string into dotted components,
// dropping an optional leading "v".
func compatComponents(s string) []string {
	return strings.Split(strings.TrimPrefix(s, "v"), ".")
}

// matchesCompat reports whether version has compat's components as a prefix.
func matchesCompat(version, compat string) bool {
	vs, cs := compatComponents(version), compatComponents(compat)
	if len(cs) > len(vs) {
		return false
	}
	for i := range cs {
		if vs[i] != cs[i] {
			return false
		}
	}
	return true
}

// selectHighest returns the highest version matching compat, or "" if none.
func selectHighest(versions []spec.VersionEntry, compat string) string {
	best := ""
	for _, v := range versions {
		if !matchesCompat(v.Version, compat) {
			continue
		}
		if best == "" || spec.CompareVersions(v.Version, best) > 0 {
			best = v.Version
		}
	}
	return best
}
