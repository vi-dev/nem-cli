package main

import (
	"github.com/spf13/cobra"

	"github.com/vi-dev/nem-cli/internal/ocix"
	"github.com/vi-dev/nem-cli/internal/publish"
)

func newCatalogPublishCmd() *cobra.Command {
	var tags []string
	var dryRun, force bool
	cmd := &cobra.Command{
		Use:   "publish <registry-ref> [dir]",
		Short: "Publish a catalog directory to an OCI registry",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := args[0]
			dir := "."
			if len(args) == 2 {
				dir = args[1]
			}
			if err := ocix.ValidateRef(ref); err != nil {
				return err
			}
			return publish.Publish(cmd.Context(), dir, ref, publish.Options{Tags: tags, DryRun: dryRun, Force: force}, console)
		},
	}
	cmd.Flags().StringArrayVar(&tags, "tag", nil, "tag to move to the published index (repeatable; default v2)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "report the publish plan without writing anything")
	cmd.Flags().BoolVar(&force, "force", false, "push every package manifest even when unchanged")
	return cmd
}
