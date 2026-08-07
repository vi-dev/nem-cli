package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/vi-dev/nem-cli/internal/shell"
)

// stdoutIsTTY reports whether the process's real stdout is a terminal; a
// package var so tests can force either branch of activate's TTY check
// without needing a real terminal attached to the test binary.
var stdoutIsTTY = func() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}

func newActivateCmd() *cobra.Command {
	var printOnly bool
	cmd := &cobra.Command{
		Use:   "activate [zsh|bash]",
		Short: "Activate nem for the current shell",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runActivate(cmd, args, printOnly)
		},
	}
	cmd.Flags().BoolVar(&printOnly, "print", false, "print the hook block to stdout instead of installing it into the rc file")
	return cmd
}

func newDeactivateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "deactivate [zsh|bash]",
		Short: "Deactivate nem for the current shell",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDeactivate(args)
		},
	}
}

// shellArg resolves the shell name an activate/deactivate invocation
// targets: args[0] when given, else the basename of $SHELL.
func shellArg(args []string) string {
	if len(args) > 0 {
		return args[0]
	}
	return defaultShellName()
}

// activationDialect maps a shell name to Bash or Zsh, the only dialects
// HookBlock, InstallBlock, and RemoveBlock support. Fish (and anything
// else) is rejected here, before it ever reaches HookBlock.
func activationDialect(name string) (shell.Dialect, error) {
	switch name {
	case "bash":
		return shell.Bash, nil
	case "zsh":
		return shell.Zsh, nil
	default:
		return 0, fmt.Errorf("unsupported shell %q (want bash or zsh)", name)
	}
}

// rcPathFor returns the rc file nem's activation block installs into for
// d, honoring $HOME.
func rcPathFor(d shell.Dialect) (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	switch d {
	case shell.Bash:
		return filepath.Join(homeDir, ".bashrc"), nil
	case shell.Zsh:
		return filepath.Join(homeDir, ".zshrc"), nil
	default:
		return "", fmt.Errorf("no rc file for shell dialect %d", int(d))
	}
}

func runActivate(cmd *cobra.Command, args []string, printOnly bool) error {
	name := shellArg(args)
	dialect, err := activationDialect(name)
	if err != nil {
		return err
	}

	if printOnly || !stdoutIsTTY() {
		fmt.Fprint(cmd.OutOrStdout(), shell.HookBlock(dialect))
		return nil
	}

	rcPath, err := rcPathFor(dialect)
	if err != nil {
		return err
	}
	if err := shell.InstallBlock(rcPath, dialect); err != nil {
		return err
	}
	console.Success("Activated nem for %s", name)
	console.Hint(fmt.Sprintf("Restart your shell or run: source %s", rcPath))
	return nil
}

func runDeactivate(args []string) error {
	name := shellArg(args)
	dialect, err := activationDialect(name)
	if err != nil {
		return err
	}

	rcPath, err := rcPathFor(dialect)
	if err != nil {
		return err
	}
	if err := shell.RemoveBlock(rcPath); err != nil {
		return err
	}
	console.Success("Deactivated nem for %s", name)
	return nil
}
