package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/vi-dev/nem-cli/internal/fsx"
	"github.com/vi-dev/nem-cli/internal/install"
	"github.com/vi-dev/nem-cli/internal/resolve"
)

func newLockCmd() *cobra.Command {
	var global bool
	cmd := &cobra.Command{
		Use:   "lock",
		Short: "Regenerate the lockfile from nem.toml and install",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLock(cmd, global)
		},
	}
	cmd.Flags().BoolVarP(&global, "global", "g", false, "target the global manifest")
	return cmd
}

// UnpinnedToolsError reports manifest tools declared without an exact
// version, which lock refuses to resolve: unlike use, it never writes
// versions back into nem.toml, so resolving a floating entry would
// produce a lock the manifest can't reproduce.
type UnpinnedToolsError struct {
	Path  string
	Names []string
}

func (e *UnpinnedToolsError) Error() string {
	return fmt.Sprintf("%s declares tools without a pinned version: %s", e.Path, strings.Join(e.Names, ", "))
}

// runLock re-resolves the manifest as written — after a manual nem.toml
// edit — and regenerates the lockfile beside it, then installs whatever
// the current platform is missing. The manifest itself is never
// rewritten, and every tool must carry an exact version.
func runLock(cmd *cobra.Command, global bool) error {
	release, err := fsx.Lock(nemHome.LockFile())
	if err != nil {
		return err
	}

	path, err := manifestPath(global, false)
	if err != nil {
		release()
		return err
	}
	if err := requireGlobalManifest(global, path); err != nil {
		release()
		return err
	}
	manifest, cfg, sources, err := loadUseState(path)
	if err != nil {
		release()
		return err
	}

	var unpinned []string
	for _, t := range manifest.Tools {
		if t.Version == "" {
			unpinned = append(unpinned, t.Key.Name)
		}
	}
	if len(unpinned) > 0 {
		release()
		return &UnpinnedToolsError{Path: path, Names: unpinned}
	}

	if err := autoSyncUnsyncedCatalogs(cmd.Context(), cfg, sources); err != nil {
		release()
		return err
	}

	result, err := resolve.Resolve(cmd.Context(), manifestTools(manifest), sources)
	if err != nil {
		release()
		return err
	}

	if err := writeLock(manifest, result); err != nil {
		release()
		return err
	}
	release()

	return install.Run(cmd.Context(), nemHome, console, currentPlatformJobs(cfg, result))
}
