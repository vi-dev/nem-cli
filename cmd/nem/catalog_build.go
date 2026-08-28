package main

import (
	"context"
	"os"

	"github.com/spf13/cobra"

	"github.com/vi-dev/nem-cli/internal/build"
	"github.com/vi-dev/nem-cli/internal/catalog"
	"github.com/vi-dev/nem-cli/internal/config"
	"github.com/vi-dev/nem-cli/internal/pkgtest"
	"github.com/vi-dev/nem-cli/internal/spec"
)

func newCatalogBuildCmd() *cobra.Command {
	var version, output, sourceSha, push string
	var dryRun, force, noTest bool
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
			if err := pkg.Validate(); err != nil {
				return err
			}
			// Checked before any recipe runs: a package that excludes this
			// platform has no recipe to run here.
			if plat := spec.Current(); !spec.PlatformsInclude(pkg.Platforms, plat) {
				console.Info("%s does not support %s", pkg.Name, plat)
				return nil
			}
			cfg, err := config.OpenConfig(nemHome)
			if err != nil {
				return err
			}
			sources, err := catalog.Open(cfg, nemHome)
			if err != nil {
				return err
			}
			opts := build.Options{Version: version, Output: output, SourceSha256: sourceSha,
				Push: push, DryRun: dryRun, Force: force}
			// Only set when the manifest declares tests: installing the hook
			// makes Build archive the output tree even for zero test steps.
			if !noTest && len(pkg.Test) > 0 {
				opts.Test = func(ctx context.Context, p *spec.Package, v, artifactPath string) error {
					// The local manifest is authoritative: the artifact under
					// test was just built from it.
					deps, err := build.ResolveDeps(ctx, nemHome, cfg, sources, console, p, p.Deps)
					if err != nil {
						return err
					}
					return runPkgTest(ctx, nemHome, deps, p, v, "", artifactPath,
						console, cmd.OutOrStdout(), cmd.ErrOrStderr())
				}
			}
			res, err := build.Build(cmd.Context(), nemHome, cfg, sources, pkg, opts,
				console, cmd.OutOrStdout(), cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			console.Success("Built %s → %s", pkg.Name, res.OutputDir)
			if res.Pushed {
				console.Success("Pushed %s:%s", res.PushedRef, res.Version)
			}
			if push != "" && !dryRun && !hasVersionEntry(pkg, res.Version) {
				console.Hint("Record this version in " + args[0] + ":\n  - version: " + res.Version)
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
	cmd.Flags().BoolVar(&noTest, "no-test", false, "skip the package's declared test steps")
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

// runPkgTest is a package var so tests can prove whether the build's test
// hook ran at all, without a live pkgtest install.
var runPkgTest = pkgtest.InstallAndRun
