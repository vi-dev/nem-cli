package main

import (
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/vi-dev/nem-cli/internal/install"
	"github.com/vi-dev/nem-cli/internal/project"
)

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show declared tools and environment variables",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			projDir, err := project.Discover(cwd)
			if err != nil {
				return err
			}
			proj, err := project.LoadManifest(filepath.Join(projDir, "nem.toml"))
			if err != nil {
				return err
			}
			global, err := project.LoadManifest(nemHome.GlobalManifest())
			if err != nil {
				return err
			}
			lock, err := project.LoadLock(filepath.Join(projDir, "nem.lock"))
			if err != nil {
				return err
			}

			locked := map[string]bool{}
			lockedVersion := map[string]string{}
			for _, e := range lock.Packages {
				locked[e.Name+"@"+e.Version] = true
				lockedVersion[e.Name] = e.Version
			}

			// project shadows global by package name
			rows := [][]string{}
			seen := map[string]bool{}
			for _, src := range []*project.Manifest{proj, global} {
				for _, tool := range src.Tools {
					if seen[tool.Key.Name] {
						continue
					}
					seen[tool.Key.Name] = true
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
			}
			console.Table([]string{"package", "version", "catalog", "locked", "installed"}, rows)

			if len(proj.Env)+len(global.Env) > 0 {
				envRows := [][]string{}
				seenEnv := map[string]bool{}
				for _, src := range []*project.Manifest{proj, global} {
					for _, e := range src.Env {
						if seenEnv[e.Name] {
							continue
						}
						seenEnv[e.Name] = true
						envRows = append(envRows, []string{e.Name, e.Value})
					}
				}
				console.Table([]string{"variable", "value"}, envRows)
			}
			return nil
		},
	}
}
