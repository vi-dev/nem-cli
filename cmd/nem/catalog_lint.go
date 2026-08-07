package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/vi-dev/nem-cli/internal/publish"
)

func newCatalogLintCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "lint [dir]",
		Short: "Validate every package manifest in a catalog",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			findings, err := publish.Lint(dir)
			if err != nil {
				return err
			}
			if len(findings) > 0 {
				for _, f := range findings {
					fmt.Fprintln(cmd.ErrOrStderr(), f.String())
				}
				return &ExitError{Code: 1}
			}
			fmt.Fprintln(cmd.OutOrStdout(), "catalog is clean: no findings")
			return nil
		},
	}
}
