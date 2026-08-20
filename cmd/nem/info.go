package main

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/vi-dev/nem-cli/internal/catalog"
	"github.com/vi-dev/nem-cli/internal/project"
)

func newInfoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "info [<catalog>:]<pkg>",
		Short: "Show a package's details and available versions",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInfo(cmd, args[0])
		},
	}
}

func runInfo(cmd *cobra.Command, arg string) error {
	key, err := project.ParseToolKey(arg)
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
	pkg, catalogName, _, err := catalog.Lookup(cmd.Context(), sources, key)
	if err != nil {
		return err
	}

	platforms := "all"
	if len(pkg.Platforms) > 0 {
		ss := make([]string, len(pkg.Platforms))
		for i, p := range pkg.Platforms {
			ss[i] = p.String()
		}
		platforms = strings.Join(ss, ", ")
	}
	versions := make([]string, len(pkg.Versions))
	for i, v := range pkg.Versions {
		versions[i] = v.Version
	}

	fields := []struct{ label, value string }{
		{"name", pkg.Name},
		{"catalog", catalogName},
		{"description", pkg.Description},
		{"homepage", pkg.Homepage},
		{"license", pkg.License},
		{"platforms", platforms},
		{"bins", strings.Join(pkg.Bins, ", ")},
		{"versions", strings.Join(versions, ", ")},
	}
	for _, f := range fields {
		if f.value == "" {
			continue
		}
		console.Data("%-12s %s\n", f.label+":", f.value)
	}
	return nil
}
