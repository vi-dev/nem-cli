package main

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/vi-dev/nem-cli/internal/build"
	"github.com/vi-dev/nem-cli/internal/catalog"
	"github.com/vi-dev/nem-cli/internal/spec"
)

func newCatalogBuildCmd() *cobra.Command {
	var version, output, sourceSha string
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
				build.Options{Version: version, Output: output, SourceSha256: sourceSha},
				console, cmd.OutOrStdout(), cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			console.Success("Built %s → %s", pkg.Name, res.OutputDir)
			return nil
		},
	}
	cmd.Flags().StringVar(&version, "version", "", "version to build (default: latest)")
	cmd.Flags().StringVar(&output, "output", "", "output tree destination (default: under $NEM_HOME/tmp)")
	cmd.Flags().StringVar(&sourceSha, "source-sha256", "", "pin the source checksum for this build")
	return cmd
}

// readFile is a package var so tests need not touch disk when preferable.
var readFile = os.ReadFile
