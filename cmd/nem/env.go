package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/vi-dev/nem-cli/internal/envx"
	"github.com/vi-dev/nem-cli/internal/install"
	"github.com/vi-dev/nem-cli/internal/project"
	"github.com/vi-dev/nem-cli/internal/shell"
)

func newEnvCmd() *cobra.Command {
	var shellName string
	cmd := &cobra.Command{
		Use:   "env",
		Short: "Print the shell script that applies the composed environment",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runEnv(cmd, shellName)
		},
	}
	cmd.Flags().StringVar(&shellName, "shell", "", "shell dialect to render for: bash, zsh, or fish (default: $SHELL)")
	return cmd
}

// defaultShellName reports the basename of $SHELL, falling back to bash
// when $SHELL is unset or empty.
func defaultShellName() string {
	s := os.Getenv("SHELL")
	if s == "" {
		return "bash"
	}
	return filepath.Base(s)
}

// envDialect maps a shell name to the dialect shell.EnvScript renders.
// Fish resolves to shell.Fish rather than erroring here, so
// shell.EnvScript's own "not supported yet" message is the single source
// of that error.
func envDialect(name string) (shell.Dialect, error) {
	switch name {
	case "bash":
		return shell.Bash, nil
	case "zsh":
		return shell.Zsh, nil
	case "fish":
		return shell.Fish, nil
	default:
		return 0, fmt.Errorf("unsupported shell %q (want bash, zsh, or fish)", name)
	}
}

// loadProjectLayer loads the project manifest and lock for the current
// directory. A directory with no nem.toml anywhere above it is not an
// error here: env still needs to render the global layer, so an absent
// project simply contributes nothing.
func loadProjectLayer() (*project.Manifest, *project.Lockfile, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, nil, err
	}
	projDir, err := project.Discover(cwd)
	if err != nil {
		if errors.Is(err, project.ErrNoManifest) {
			return &project.Manifest{}, &project.Lockfile{}, nil
		}
		return nil, nil, err
	}
	manifest, err := project.LoadManifest(filepath.Join(projDir, "nem.toml"))
	if err != nil {
		return nil, nil, err
	}
	lock, err := project.LoadLock(filepath.Join(projDir, "nem.lock"))
	if err != nil {
		return nil, nil, err
	}
	return manifest, lock, nil
}

func installMetaLookup(name, version string) (*install.Meta, bool) {
	meta, err := install.ReadMeta(nemHome, name, version)
	if err != nil {
		return nil, false
	}
	return meta, true
}

func runEnv(cmd *cobra.Command, shellName string) error {
	if shellName == "" {
		shellName = defaultShellName()
	}
	dialect, err := envDialect(shellName)
	if err != nil {
		return err
	}

	projManifest, projLock, err := loadProjectLayer()
	if err != nil {
		return err
	}
	globalManifest, err := project.LoadManifest(nemHome.GlobalManifest())
	if err != nil {
		return err
	}
	globalLock, err := project.LoadLock(nemHome.GlobalLock())
	if err != nil {
		return err
	}

	result := envx.Compose(projManifest, globalManifest, projLock, globalLock, nemHome, installMetaLookup, os.LookupEnv)
	for _, w := range result.Warnings {
		console.Warn("%s", w)
	}

	script, err := shell.EnvScript(dialect, result, os.LookupEnv)
	if err != nil {
		return err
	}
	fmt.Fprint(cmd.OutOrStdout(), script)
	return nil
}
