package main

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/vi-dev/nem-cli/internal/envx"
	"github.com/vi-dev/nem-cli/internal/install"
	"github.com/vi-dev/nem-cli/internal/project"
)

func newStatusCmd() *cobra.Command {
	var global bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show declared tools and composed environment variables",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatus(global)
		},
	}
	cmd.Flags().BoolVarP(&global, "global", "g", false, "show the global scope")
	return cmd
}

func runStatus(global bool) error {
	manifestP, err := manifestPath(global, false)
	if err != nil {
		return err
	}
	m, err := project.LoadManifest(manifestP)
	if err != nil {
		return err
	}

	lock, err := project.LoadLock(lockPathFor(manifestP))
	if err != nil {
		return err
	}

	lockedVersion := map[string]string{}
	for _, e := range lock.Packages {
		lockedVersion[e.Name] = e.Version
	}

	rows := [][]string{}
	for _, tool := range m.Tools {
		catalog := tool.Key.Catalog
		if catalog == "" {
			catalog = "-"
		}
		lockedCell := "no"
		if lock.Covers(tool) {
			lockedCell = "yes"
		}
		installedCell := "-"
		if lv, ok := lockedVersion[tool.Key.Name]; ok {
			installedCell = "no"
			if install.IsInstalled(nemHome, tool.Key.Name, lv) {
				installedCell = "yes"
			}
		}
		rows = append(rows, []string{tool.Key.Name, tool.Version, catalog, lockedCell, installedCell})
	}
	console.Table([]string{"package", "version", "catalog", "locked", "installed"}, rows)

	// The other scope's layer is loaded only so [env] references to its
	// variables resolve to their saved pre-nem originals.
	var other *project.Manifest
	var otherLock *project.Lockfile
	if global {
		other, otherLock, err = loadProjectLayer()
		if err != nil {
			return err
		}
	} else {
		other, err = project.LoadManifest(nemHome.GlobalManifest())
		if err != nil {
			return err
		}
		otherLock, err = project.LoadLock(nemHome.GlobalLock())
		if err != nil {
			return err
		}
	}
	result := envx.ComposeScope(m, other, lock, otherLock, nemHome, installMetaLookup, os.LookupEnv)
	for _, w := range result.Warnings {
		console.Warn("%s", w)
	}
	if len(result.Vars) > 0 {
		console.Blank()
		envRows := [][]string{}
		for _, v := range result.Vars {
			envRows = append(envRows, []string{v.Name, v.Value, v.Source})
		}
		console.Table([]string{"variable", "value", "source"}, envRows)
	}
	return nil
}
