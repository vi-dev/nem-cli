package main

import (
	"os"
	"slices"

	"github.com/spf13/cobra"

	"github.com/vi-dev/nem-cli/internal/envx"
	"github.com/vi-dev/nem-cli/internal/install"
	"github.com/vi-dev/nem-cli/internal/project"
	"github.com/vi-dev/nem-cli/internal/spec"
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
	projLock, globalLock := lock, otherLock
	if global {
		projLock, globalLock = otherLock, lock
	}
	warnMissingInstalls(projLock, globalLock)

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

// warnMissingInstalls reports locked-but-not-installed packages as one
// actionable line per scope. Only status calls this: every other command
// either runs from the activation hook on directory changes (`nem env`)
// or narrates its own work, so status alone owns this warning.
func warnMissingInstalls(projLock, globalLock *project.Lockfile) {
	nProj, nGlobal := countMissing(projLock), countMissing(globalLock)
	if nProj == 0 && nGlobal == 0 {
		return
	}
	console.Info("") // blank line separating the table from the warnings
	if nProj > 0 {
		console.Warn("%d project %s not installed — run `nem sync`", nProj, packagesWord(nProj))
	}
	if nGlobal > 0 {
		console.Warn("%d global %s not installed — run `nem sync -g`", nGlobal, packagesWord(nGlobal))
	}
}

// countMissing counts lock entries valid on the current platform with no
// install on this machine — exactly the entries sync would install.
func countMissing(lock *project.Lockfile) int {
	current := spec.Current().String()
	n := 0
	for _, e := range lock.Packages {
		if slices.Contains(e.Platforms, current) && !install.IsInstalled(nemHome, e.Name, e.Version) {
			n++
		}
	}
	return n
}

func packagesWord(n int) string {
	if n == 1 {
		return "package"
	}
	return "packages"
}
