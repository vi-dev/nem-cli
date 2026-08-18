package main

import (
	"slices"

	"github.com/spf13/cobra"

	"github.com/vi-dev/nem-cli/internal/catalog"
	"github.com/vi-dev/nem-cli/internal/fetch"
	"github.com/vi-dev/nem-cli/internal/install"
	"github.com/vi-dev/nem-cli/internal/project"
	"github.com/vi-dev/nem-cli/internal/spec"
)

func newSyncCmd() *cobra.Command {
	var global bool
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Install locked tools missing on this machine",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSync(cmd, global)
		},
	}
	cmd.Flags().BoolVarP(&global, "global", "g", false, "target the global scope")
	return cmd
}

// runSync installs whatever the current platform's lock entries call for
// but this machine is missing. It never resolves versions: the lock is
// authoritative, and catalog.Lookup is consulted only to fetch each
// entry's package data and verify its content digest hasn't drifted since
// it was locked.
func runSync(cmd *cobra.Command, global bool) error {
	manifestP, err := manifestPath(global, false)
	if err != nil {
		return err
	}
	lock, err := project.LoadLock(lockPathFor(manifestP))
	if err != nil {
		return err
	}

	warnManifestDrift(manifestP, lock)

	cfg, err := catalog.OpenConfig(nemHome)
	if err != nil {
		return err
	}
	sources, err := catalog.Open(cfg, nemHome)
	if err != nil {
		return err
	}

	current := spec.Current().String()
	var jobs []install.Job
	for _, entry := range lock.Packages {
		if !slices.Contains(entry.Platforms, current) {
			continue
		}
		if install.IsInstalled(nemHome, entry.Name, entry.Version) {
			continue
		}

		pkg, _, dig, err := catalog.Lookup(cmd.Context(), sources, project.ToolKey{Catalog: entry.Catalog, Name: entry.Name})
		if err != nil {
			return err
		}
		if entry.Digest != "" && dig != entry.Digest {
			return &catalog.DigestMismatchError{
				Name: entry.Name, Version: entry.Version,
				Locked: entry.Digest, Current: dig,
			}
		}

		ref := ""
		if e := cfg.Find(entry.Catalog); e != nil && e.Type == "oci" {
			ref = e.Ref
		}
		jobs = append(jobs, install.Job{
			Pkg:     pkg,
			Version: entry.Version,
			Catalog: entry.Catalog,
			Source:  fetch.Source{CatalogRef: ref},
		})
	}

	if len(jobs) == 0 {
		return nil
	}
	return install.Run(cmd.Context(), nemHome, console, jobs)
}

// warnManifestDrift flags manifest tools the lock doesn't cover — the
// aftermath of a hand-edited manifest — without failing sync: the lock
// stays authoritative, and drift is advisory until `nem lock` re-locks.
func warnManifestDrift(manifestPath string, lock *project.Lockfile) {
	m, err := project.LoadManifest(manifestPath)
	if err != nil {
		console.Warn("Skipping drift check, cannot read %s: %v", manifestPath, err)
		return
	}
	for _, tool := range m.Tools {
		if tool.Version == "" {
			console.Warn("%s declares %s without a version — pin one in nem.toml or run `nem use %s@<version>`",
				manifestPath, tool.Key.Name, tool.Key.String())
			continue
		}
		if !lock.Covers(tool) {
			console.Warn("%s declares %s@%s, which %s does not cover — run `nem lock`",
				manifestPath, tool.Key.String(), tool.Version, lock.Path)
		}
	}
}
