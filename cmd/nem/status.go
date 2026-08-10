package main

import (
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/vi-dev/nem-cli/internal/install"
	"github.com/vi-dev/nem-cli/internal/project"
)

func newStatusCmd() *cobra.Command {
	var global bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show declared tools and environment variables",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatus(cmd, global)
		},
	}
	cmd.Flags().BoolVarP(&global, "global", "g", false, "show the global scope")
	return cmd
}

func runStatus(cmd *cobra.Command, global bool) error {
	manifestP, err := manifestPath(global, false)
	if err != nil {
		return err
	}
	m, err := project.LoadManifest(manifestP)
	if err != nil {
		return err
	}

	lockPath := nemHome.GlobalLock()
	if !global {
		lockPath = filepath.Join(filepath.Dir(manifestP), "nem.lock")
	}
	lock, err := project.LoadLock(lockPath)
	if err != nil {
		return err
	}

	locked := map[string]bool{}
	lockedVersion := map[string]string{}
	for _, e := range lock.Packages {
		locked[e.Name+"@"+e.Version] = true
		lockedVersion[e.Name] = e.Version
	}

	rows := [][]string{}
	for _, tool := range m.Tools {
		catalog := tool.Key.Catalog
		if catalog == "" {
			catalog = "-"
		}
		lockedCell := "no"
		if locked[tool.Key.Name+"@"+tool.Version] {
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

	if len(m.Env) > 0 {
		envRows := [][]string{}
		for _, e := range m.Env {
			envRows = append(envRows, []string{e.Name, e.Value})
		}
		console.Table([]string{"variable", "value"}, envRows)
	}
	return nil
}
