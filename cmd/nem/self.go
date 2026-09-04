package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/vi-dev/nem-cli/internal/selfupdate"
)

func newSelfCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "self",
		Short: "Manage this nem installation",
	}
	cmd.AddCommand(newSelfUpdateCmd())
	return cmd
}

func newSelfUpdateCmd() *cobra.Command {
	var (
		targetVersion string
		check         bool
	)
	cmd := &cobra.Command{
		Use:     "update",
		Aliases: []string{"up"},
		Short:   "Update nem to the latest build on its channel",
		Long: "Download a nem release build, verify its checksum, and replace " +
			"the running binary with it. Without --version, updates within " +
			"the current channel: stable builds to the latest release, " +
			"unstable builds to the current rolling build from main.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runSelfUpdate(cmd.Context(), targetVersion, check)
		},
	}
	cmd.Flags().StringVar(&targetVersion, "version", "", `release tag to install, like v1.2.3, "stable" for the latest release, or "unstable"`)
	cmd.Flags().BoolVar(&check, "check", false, "only report whether an update is available")
	return cmd
}

func runSelfUpdate(ctx context.Context, targetVersion string, check bool) error {
	if version == "dev" {
		return errors.New("this nem build did not come from a release; update it the way it was built (e.g. make install)")
	}
	if targetVersion != "" && targetVersion != "unstable" && targetVersion != "stable" && !strings.HasPrefix(targetVersion, "v") {
		return fmt.Errorf(`invalid --version %q: want a release tag like v1.2.3, "stable", or "unstable"`, targetVersion)
	}

	updater := newSelfUpdater()
	target := targetVersion
	if target == "" {
		if channel == "unstable" {
			target = "unstable"
		} else {
			target = "stable"
		}
	}
	if target == "stable" {
		resolved, err := updater.ResolveLatest(ctx)
		if err != nil {
			return err
		}
		target = resolved
	}

	upToDate := false
	if target == "unstable" {
		// Only a build from the unstable pipeline can match the rolling
		// build; a release build from the same commit still differs in its
		// stamped version and channel.
		if channel == "unstable" {
			remote, err := updater.ResolveUnstableCommit(ctx)
			if err != nil {
				return err
			}
			commit := currentCommit()
			upToDate = commit != "" && commit == remote
		}
	} else {
		// Older release builds report their version without the leading v.
		upToDate = strings.TrimPrefix(target, "v") == strings.TrimPrefix(version, "v")
	}

	if check {
		if upToDate {
			console.Data("nem %s is up to date\n", version)
		} else {
			console.Data("Update available: nem %s → %s\n", version, target)
		}
		return nil
	}

	if upToDate {
		console.Success("nem %s is up to date", version)
		return nil
	}
	if target == "unstable" {
		if channel == "unstable" {
			console.Info("Following the unstable channel; `nem self update --version stable` switches back to releases")
		} else {
			console.Warn("Installing the unstable build from main — not a released version")
		}
	}

	exePath, err := selfExecutablePath()
	if err != nil {
		return err
	}
	task := console.Task("Updating nem to " + target)
	if err := updater.Update(ctx, target, exePath, task); err != nil {
		task.Fail("Update failed")
		return err
	}
	task.Done("nem " + version + " → " + target)
	if target != "unstable" {
		console.Info("Release notes: https://github.com/vi-dev/nem-cli/releases/tag/%s", target)
	}
	return nil
}

// newSelfUpdater, selfExecutablePath, and currentCommit are package vars so
// tests can point the command at a local release server, a scratch binary,
// and a fixed build commit.
var (
	newSelfUpdater = func() *selfupdate.Updater {
		return selfupdate.NewUpdater(os.Getenv("GITHUB_TOKEN"))
	}
	selfExecutablePath = selfupdate.ExecutablePath
	currentCommit      = func() string {
		// A modified tree makes the revision an unreliable identity.
		rev, modified := vcsRevision()
		if modified {
			return ""
		}
		return rev
	}
)
