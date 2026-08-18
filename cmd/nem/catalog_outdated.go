package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"github.com/vi-dev/nem-cli/internal/discover"
	"github.com/vi-dev/nem-cli/internal/spec"
)

// discoverLatest is a package var so tests can stub upstream listing.
var discoverLatest = discover.Latest

// outdatedRow is one package's check result; the --json shape is the
// contract catalog automation consumes.
type outdatedRow struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Current  string `json:"current"`
	Latest   string `json:"latest,omitempty"`
	Outdated bool   `json:"outdated"`
	Error    string `json:"error,omitempty"`
}

func newCatalogOutdatedCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "outdated [dir|pkg.yaml]",
		Short: "Report packages whose upstream has a newer version",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			target := "."
			if len(args) == 1 {
				target = args[0]
			}
			paths, err := manifestPaths(target)
			if err != nil {
				return err
			}

			type item struct {
				path string
				pkg  *spec.Package
			}
			var checked []item
			skipped := 0
			for _, p := range paths {
				data, err := os.ReadFile(p)
				if err != nil {
					return err
				}
				pkg, err := spec.Parse(data)
				if err != nil {
					return fmt.Errorf("%s: %w", p, err)
				}
				if pkg.VersionDiscovery == nil {
					skipped++
					console.Debug("Skipping %s: no versionDiscovery", pkg.Name)
					continue
				}
				checked = append(checked, item{p, pkg})
			}

			rows := make([]outdatedRow, len(checked))
			g, gctx := errgroup.WithContext(cmd.Context())
			g.SetLimit(8)
			for i, it := range checked {
				g.Go(func() error {
					row := outdatedRow{Name: it.pkg.Name, Path: it.path}
					if len(it.pkg.Versions) > 0 {
						row.Current = it.pkg.Versions[0].Version
					}
					latest, err := discoverLatest(gctx, it.pkg)
					if err != nil {
						row.Error = err.Error()
					} else {
						row.Latest = latest
						row.Outdated = spec.CompareVersions(latest, row.Current) > 0
					}
					rows[i] = row
					return nil
				})
			}
			_ = g.Wait() // workers record errors in rows and never fail the group

			outdated := 0
			var tableRows [][]string
			for _, r := range rows {
				if r.Error != "" {
					console.Warn("%s: %s", r.Name, r.Error)
				}
				if r.Outdated {
					outdated++
					tableRows = append(tableRows, []string{r.Name, r.Current, r.Latest})
				}
			}
			if jsonOut {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				if err := enc.Encode(rows); err != nil {
					return err
				}
			} else if len(tableRows) > 0 {
				console.Table([]string{"PACKAGE", "CURRENT", "LATEST"}, tableRows)
			}
			console.Success("Checked %d packages: %d outdated, %d without discovery",
				len(checked), outdated, skipped)
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit every checked package as JSON")
	return cmd
}
