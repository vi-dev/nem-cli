package main

import (
	"errors"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/vi-dev/nem-cli/internal/catalog"
	"github.com/vi-dev/nem-cli/internal/ocix"
)

func newSearchCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "search <query>",
		Short: "Search catalogs for packages",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSearch(cmd, args[0])
		},
	}
}

// searchHit pairs a matched summary with the catalog it came from.
type searchHit struct {
	catalog.Summary
	Catalog string
}

func runSearch(cmd *cobra.Command, query string) error {
	cfg, err := catalog.OpenConfig(nemHome)
	if err != nil {
		return err
	}
	sources, err := catalog.Open(cfg, nemHome)
	if err != nil {
		return err
	}

	q := strings.ToLower(query)
	seen := map[string]bool{}
	var hits []searchHit
	for _, n := range sources {
		summaries, err := n.Source.Summaries(cmd.Context())
		if err != nil {
			if errors.Is(err, ocix.ErrNotSynced) {
				console.Warn("Catalog %s is not synced (run nem catalog update)", n.Name)
				continue
			}
			return err
		}
		for _, s := range summaries {
			if seen[s.Name] || !searchMatches(s, q) {
				continue
			}
			seen[s.Name] = true
			hits = append(hits, searchHit{Summary: s, Catalog: n.Name})
		}
	}

	sort.SliceStable(hits, func(i, j int) bool {
		ri, rj := searchRank(hits[i].Summary, q), searchRank(hits[j].Summary, q)
		if ri != rj {
			return ri < rj
		}
		return hits[i].Name < hits[j].Name
	})

	if len(hits) == 0 {
		console.Warn("no packages match %q", query)
		return nil
	}

	rows := make([][]string, len(hits))
	for i, h := range hits {
		rows[i] = []string{h.Name, h.Latest, h.Catalog, h.Description}
	}
	console.Table([]string{"name", "version", "catalog", "description"}, rows)
	return nil
}

func searchMatches(s catalog.Summary, q string) bool {
	return strings.Contains(strings.ToLower(s.Name), q) || strings.Contains(strings.ToLower(s.Description), q)
}

// searchRank orders matches: exact name, then name-prefix, then
// name-substring, then description-only.
func searchRank(s catalog.Summary, q string) int {
	name := strings.ToLower(s.Name)
	switch {
	case name == q:
		return 0
	case strings.HasPrefix(name, q):
		return 1
	case strings.Contains(name, q):
		return 2
	default:
		return 3
	}
}
