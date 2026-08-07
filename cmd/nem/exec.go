package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/vi-dev/nem-cli/internal/envx"
	"github.com/vi-dev/nem-cli/internal/install"
	"github.com/vi-dev/nem-cli/internal/project"
)

// ExitError carries the exit code a command must terminate with, letting
// main map it straight to os.Exit(Code) instead of running it through the
// usual error/hint narration.
type ExitError struct{ Code int }

func (e *ExitError) Error() string { return fmt.Sprintf("exit status %d", e.Code) }

func newExecCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "exec [-- <cmd> [args...]]",
		Short: "Run a command in the composed environment",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExec(cmd, args)
		},
	}
	// Stops flag parsing at the first positional argument instead of
	// pflag's default of scanning the whole line for recognized flags: a
	// child command's own "-la"-style arguments must never be mistaken for
	// nem's, whether or not the user separated them with "--". A leading
	// "--color"/"--quiet"/etc. before the command name is still parsed
	// normally, since it comes before that first positional argument.
	cmd.Flags().SetInterspersed(false)
	return cmd
}

// composedPath loads the project and global manifest/lock layers, composes
// them with envx, narrates any compose warnings, and returns the resulting
// PATH value: the composed package dirs prepended onto the invoking
// process's own PATH. exec and which both run tools by bare name against
// this exact value, so they must resolve identically.
func composedPath() (envx.Result, string, error) {
	projManifest, projLock, err := loadProjectLayer()
	if err != nil {
		return envx.Result{}, "", err
	}
	globalManifest, err := project.LoadManifest(nemHome.GlobalManifest())
	if err != nil {
		return envx.Result{}, "", err
	}
	globalLock, err := project.LoadLock(nemHome.GlobalLock())
	if err != nil {
		return envx.Result{}, "", err
	}

	metaLookup := func(name, version string) (*install.Meta, bool) {
		meta, err := install.ReadMeta(nemHome, name, version)
		if err != nil {
			return nil, false
		}
		return meta, true
	}
	result := envx.Compose(projManifest, globalManifest, projLock, globalLock, nemHome, metaLookup, os.LookupEnv)
	for _, w := range result.Warnings {
		console.Warn("%s", w)
	}

	pathValue := prependPath(result.Path, currentPathValue(os.Environ()))
	return result, pathValue, nil
}

func runExec(cmd *cobra.Command, args []string) error {
	result, pathValue, err := composedPath()
	if err != nil {
		return err
	}

	base := os.Environ()
	childEnv := buildChildEnv(base, result, pathValue)

	// A *exec.Cmd built from a bare command name resolves it via LookPath
	// against the invoking process's own PATH, never against child.Env —
	// so the composed PATH would never be consulted for a package's own
	// tool unless exec resolves it here first.
	resolved, lookErr := lookPath(args[0], pathValue)
	if lookErr != nil {
		return &ExitError{Code: exitCodeFor(lookErr)}
	}

	child := exec.CommandContext(cmd.Context(), resolved, args[1:]...)
	child.Args[0] = args[0]
	child.Env = childEnv
	child.Stdin = cmd.InOrStdin()
	child.Stdout = cmd.OutOrStdout()
	child.Stderr = cmd.ErrOrStderr()

	if err := child.Run(); err != nil {
		return &ExitError{Code: exitCodeFor(err)}
	}
	return nil
}

// currentPathValue returns the PATH entry's value out of an os.Environ-style
// slice, or "" when unset.
func currentPathValue(env []string) string {
	for _, kv := range env {
		if name, value, ok := strings.Cut(kv, "="); ok && name == "PATH" {
			return value
		}
	}
	return ""
}

// buildChildEnv overlays res's composed vars onto base — the invoking
// process's own environment, as "name=value" pairs — replacing any existing
// entry for an overlaid name, and sets PATH to pathValue. It builds a fresh
// env slice for the child rather than mutating the invoking process's
// environment: exec runs a single child, not an interactive shell, so there
// is nothing to save or restore afterward.
func buildChildEnv(base []string, res envx.Result, pathValue string) []string {
	overlay := make(map[string]string, len(res.Vars))
	for _, v := range res.Vars {
		overlay[v.Name] = v.Value
	}

	env := make([]string, 0, len(base)+len(res.Vars)+1)
	applied := make(map[string]bool, len(overlay))
	for _, kv := range base {
		name, _, ok := strings.Cut(kv, "=")
		if !ok {
			env = append(env, kv)
			continue
		}
		if name == "PATH" {
			continue
		}
		if nv, ok := overlay[name]; ok {
			env = append(env, name+"="+nv)
			applied[name] = true
			continue
		}
		env = append(env, kv)
	}
	for _, v := range res.Vars {
		if applied[v.Name] {
			continue
		}
		env = append(env, v.Name+"="+v.Value)
	}

	env = append(env, "PATH="+pathValue)
	return env
}

// prependPath renders dirs prepended onto existing, joined with the
// platform's PATH separator; existing is returned unchanged when dirs is
// empty.
func prependPath(dirs []string, existing string) string {
	if len(dirs) == 0 {
		return existing
	}
	prefix := strings.Join(dirs, string(os.PathListSeparator))
	if existing == "" {
		return prefix
	}
	return prefix + string(os.PathListSeparator) + existing
}

// lookPath resolves name to the executable exec should run: unchanged when
// name already contains a path separator (exec.Command never searches PATH
// for those either), else the first executable regular file named name
// found walking pathValue's directories in order. The returned path is
// guaranteed to contain a path separator, whatever the directory: handing
// exec.CommandContext a separator-free name — even one that's genuinely
// absolute once resolved — makes *exec.Cmd run its own LookPath against the
// real process PATH, the exact ambient-PATH fallback this function exists
// to prevent, so a resolution like filepath.Join(".", name) (which
// filepath.Clean reduces to a bare name whenever a PATH entry is empty, a
// common trailing-colon shell artifact) must be re-anchored before it's
// returned.
func lookPath(name, pathValue string) (string, error) {
	if strings.ContainsRune(name, os.PathSeparator) {
		return name, nil
	}
	for _, dir := range filepath.SplitList(pathValue) {
		if dir == "" {
			dir = "."
		}
		candidate := filepath.Join(dir, name)
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() {
			continue
		}
		if info.Mode().Perm()&0o111 != 0 {
			return anchorPath(candidate), nil
		}
	}
	return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
}

// anchorPath guarantees path contains a path separator, prefixing it with
// "." + separator when it doesn't.
func anchorPath(path string) string {
	if strings.ContainsRune(path, os.PathSeparator) {
		return path
	}
	return "." + string(os.PathSeparator) + path
}

// exitCodeFor maps a *exec.Cmd.Run error to the exit code nem itself
// should terminate with: 130 when the failure is context cancellation
// itself (the child never got to start); the child's own code when it ran
// and exited; 128+signal when a signal killed it; 127 when the command
// couldn't be found; 126 for any other start failure (permission denied,
// not executable, and so on).
func exitCodeFor(err error) int {
	if errors.Is(err, context.Canceled) {
		return 130
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal())
		}
		return exitErr.ExitCode()
	}
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) ||
		strings.Contains(err.Error(), "executable file not found") {
		return 127
	}
	return 126
}
