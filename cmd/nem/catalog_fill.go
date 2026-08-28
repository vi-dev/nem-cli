package main

import (
	"github.com/spf13/cobra"

	"github.com/vi-dev/nem-cli/internal/fill"
)

func newCatalogFillCmd() *cobra.Command {
	var pkgs []string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "fill <catalog-ref>",
		Short: "Download a catalog's upstream artifacts and publish them as archives",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			summary, err := fill.Run(cmd.Context(), nemHome, fill.Options{CatalogRef: args[0], Pkgs: pkgs, DryRun: dryRun}, console)
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
	cmd.Flags().StringArrayVar(&pkgs, "pkg", nil, "limit to this package (repeatable)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "report the fill plan without downloading or publishing anything")
	return cmd
}
