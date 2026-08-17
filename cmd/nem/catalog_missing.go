package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/vi-dev/nem-cli/internal/ocix"
	"github.com/vi-dev/nem-cli/internal/spec"
)

// openArchives is a package var so tests can stub the remote archives repo.
var openArchives = ocix.RemoteArchives

// missingRow is one absent archive in the --json output, listed under the
// platform it is absent for; the shape is the contract catalog automation
// consumes as its build matrix.
type missingRow struct {
	Package string `json:"package"`
	Version string `json:"version"`
}

// missingEntry is one declared version with at least one absent platform
// archive.
type missingEntry struct {
	name, version string
	absent        []spec.Platform
}

func newCatalogMissingCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "missing <registry-ref> [dir|pkg.yaml]",
		Short: "Report declared versions whose platform archives are absent from the registry",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := args[0]
			target := "."
			if len(args) == 2 {
				target = args[1]
			}
			if err := ocix.ValidateBaseRef(ref); err != nil {
				return err
			}
			paths, err := manifestPaths(target)
			if err != nil {
				return err
			}

			checked, skipped := 0, 0
			var entries []missingEntry
			for _, p := range paths {
				data, err := os.ReadFile(p)
				if err != nil {
					return err
				}
				pkg, err := spec.Parse(data)
				if err != nil {
					return fmt.Errorf("%s: %w", p, err)
				}
				if pkg.Artifact.OCI == "" {
					skipped++
					console.Debug("Skipping %s: no oci artifact", pkg.Name)
					continue
				}
				checked++
				src, err := openArchives(ref, pkg.Name)
				if err != nil {
					return err
				}
				plats := pkg.SupportedBy()
				for _, v := range pkg.Versions {
					have, err := ocix.ArchivePlatforms(cmd.Context(), src, v.Version)
					if err != nil && !errors.Is(err, ocix.ErrArchiveNotFound) {
						return fmt.Errorf("%s@%s: %w", pkg.Name, v.Version, err)
					}
					var absent []spec.Platform
					for _, plat := range plats {
						if !slices.Contains(have, plat) {
							absent = append(absent, plat)
						}
					}
					if len(absent) > 0 {
						entries = append(entries, missingEntry{pkg.Name, v.Version, absent})
					}
				}
			}

			if jsonOut {
				grouped := make(map[string][]missingRow, len(spec.Supported))
				for _, plat := range spec.Supported {
					grouped[plat.String()] = []missingRow{}
				}
				for _, e := range entries {
					for _, plat := range e.absent {
						grouped[plat.String()] = append(grouped[plat.String()], missingRow{e.name, e.version})
					}
				}
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				if err := enc.Encode(grouped); err != nil {
					return err
				}
			} else if len(entries) > 0 {
				rows := make([][]string, len(entries))
				for i, e := range entries {
					ss := make([]string, len(e.absent))
					for j, plat := range e.absent {
						ss[j] = plat.String()
					}
					rows[i] = []string{e.name, e.version, strings.Join(ss, ", ")}
				}
				console.Table([]string{"PACKAGE", "VERSION", "MISSING"}, rows)
			}
			console.Success("Checked %d oci packages: %d incomplete versions, %d prebuilt skipped",
				checked, len(entries), skipped)
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit absent archives grouped by platform as JSON")
	return cmd
}
