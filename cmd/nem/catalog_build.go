package main

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/vi-dev/nem-cli/internal/build"
	"github.com/vi-dev/nem-cli/internal/catalog"
	"github.com/vi-dev/nem-cli/internal/spec"
)

func newCatalogBuildCmd() *cobra.Command {
	var version, output, sourceSha, push string
	var dryRun, force bool
	cmd := &cobra.Command{
		Use:   "build <pkg.yaml>",
		Short: "Build a source package's recipe on the host platform",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := readFile(args[0])
			if err != nil {
				return err
			}
			pkg, err := spec.Parse(data)
			if err != nil {
				return err
			}
			cfg, err := catalog.OpenConfig(nemHome)
			if err != nil {
				return err
			}
			sources, err := catalog.Open(cfg, nemHome)
			if err != nil {
				return err
			}
			res, err := build.Build(cmd.Context(), nemHome, cfg, sources, pkg,
				build.Options{Version: version, Output: output, SourceSha256: sourceSha,
					Push: push, DryRun: dryRun, Force: force},
				console, cmd.OutOrStdout(), cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			console.Success("Built %s → %s", pkg.Name, res.OutputDir)
			if res.Pushed {
				console.Success("Pushed %s:%s", res.PushedRef, res.Version)
			}
			if push != "" && !dryRun && !hasVersionEntry(pkg, res.Version) {
				console.Hint("record this version in " + args[0] + ":\n  - version: " + res.Version)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&version, "version", "", "version to build (default: latest)")
	cmd.Flags().StringVar(&output, "output", "", "output tree destination (default: under $NEM_HOME/tmp)")
	cmd.Flags().StringVar(&sourceSha, "source-sha256", "", "pin the source checksum for this build")
	cmd.Flags().StringVar(&push, "push", "", "publish the built archive to this catalog registry ref")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "with --push, report the plan without writing")
	cmd.Flags().BoolVar(&force, "force", false, "with --push, overwrite an unchanged platform entry")
	return cmd
}

func hasVersionEntry(pkg *spec.Package, v string) bool {
	for _, e := range pkg.Versions {
		if e.Version == v {
			return true
		}
	}
	return false
}

// readFile is a package var so tests need not touch disk when preferable.
var readFile = os.ReadFile
