package main

import (
	"bytes"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/vi-dev/nem-cli/internal/fsx"
	"github.com/vi-dev/nem-cli/internal/publish"
	"github.com/vi-dev/nem-cli/internal/spec"
)

func newCatalogFmtCmd() *cobra.Command {
	var check bool
	cmd := &cobra.Command{
		Use:   "fmt [dir|pkg.yaml]",
		Short: "Rewrite package manifests to canonical form",
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
			dirty := 0
			for _, p := range paths {
				data, err := os.ReadFile(p)
				if err != nil {
					return err
				}
				formatted, err := spec.Format(data)
				if err != nil {
					return fmt.Errorf("%s: %w", p, err)
				}
				if bytes.Equal(data, formatted) {
					continue
				}
				dirty++
				if check {
					console.Data("%s\n", p)
					continue
				}
				if err := fsx.WriteAtomic(p, formatted, 0o644); err != nil {
					return err
				}
				console.Success("Formatted %s", p)
			}
			if check && dirty > 0 {
				return &ExitError{Code: 1}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "list non-canonical manifests and exit 1 instead of rewriting")
	return cmd
}

// manifestPaths resolves target — a catalog dir laid out as
// pkgs/<name>/pkg.yaml, or a single manifest file — to the manifest
// paths that exist.
func manifestPaths(target string) ([]string, error) {
	info, err := os.Stat(target)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return []string{target}, nil
	}
	manifests, err := publish.Manifests(target)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, m := range manifests {
		if _, err := os.Stat(m.Path); err == nil {
			out = append(out, m.Path)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no package manifests under %s", target)
	}
	return out, nil
}
