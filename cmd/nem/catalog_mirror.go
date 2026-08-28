package main

import (
	"github.com/spf13/cobra"

	"github.com/vi-dev/nem-cli/internal/mirror"
)

func newCatalogMirrorCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "mirror <src-ref> <dst-ref>",
		Short: "Replicate a catalog and its archives to another registry",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			summary, err := mirror.Run(cmd.Context(), mirror.Options{SrcRef: args[0], DstRef: args[1], DryRun: dryRun}, console)
			if err != nil {
				return err
			}
			console.Success("%s", summary.String())
			if summary.Failed > 0 {
				return &ExitError{Code: 1}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "report the mirror plan without writing anything")
	return cmd
}
