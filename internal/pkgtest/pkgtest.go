// Package pkgtest installs a package under a throwaway aliased name and runs
// its declared test steps against that install.
package pkgtest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/vi-dev/nem-cli/internal/build"
	"github.com/vi-dev/nem-cli/internal/envx"
	"github.com/vi-dev/nem-cli/internal/fsx"
	"github.com/vi-dev/nem-cli/internal/home"
	"github.com/vi-dev/nem-cli/internal/install"
	"github.com/vi-dev/nem-cli/internal/report"
	"github.com/vi-dev/nem-cli/internal/spec"
	"github.com/vi-dev/nem-cli/internal/usage"
)

// InstallAndRun installs artifactPath as a throwaway alias of pkg, runs the
// test steps that apply to the running platform against that installation,
// and removes it. The alias sits at the same depth under packages/ as a real
// install, which is all a baked relative rpath needs — it names the
// dependency and the depth, never the package's own directory. deps are
// installed under their real names for exactly that reason; resolving them
// is the caller's job, since nem catalog build and nem catalog test draw the
// authoritative set from different places (the catalog vs. the local
// manifest).
//
// Nothing already on disk is moved: an existing real installation of pkg is
// neither used nor touched.
//
// No applicable step is success. A package that excludes this platform, or
// whose steps all do, asserts nothing here — which is the opposite of
// Build's rule for build steps, where running nothing would publish an
// unbuilt tree.
func InstallAndRun(ctx context.Context, h home.Home, deps []build.ResolvedDep,
	pkg *spec.Package, version, catalogName, artifactPath string,
	rep report.Reporter, stdout, stderr io.Writer) (err error) {

	plat := spec.Current()
	// A step runs only if pkg itself supports this platform: an unconstrained
	// step cannot run where the package installs nothing.
	var steps []spec.TestStep
	if spec.PlatformsInclude(pkg.Platforms, plat) {
		for _, s := range pkg.Test {
			if spec.PlatformsInclude(s.Platforms, plat) {
				steps = append(steps, s)
			}
		}
	}
	if len(steps) == 0 {
		if len(pkg.Test) > 0 {
			rep.Info("No test of %s applies to %s; nothing asserted", pkg.Name, plat)
		}
		return nil
	}

	if mkdirErr := os.MkdirAll(h.Packages(), 0o755); mkdirErr != nil {
		return fmt.Errorf("create packages dir: %w", mkdirErr)
	}
	aliasDir, mkTempErr := os.MkdirTemp(h.Packages(), pkg.Name+home.TestInstallInfix+"*")
	if mkTempErr != nil {
		return fmt.Errorf("create test install dir: %w", mkTempErr)
	}
	aliasName := filepath.Base(aliasDir)
	// Joined into the named return so a removal failure surfaces alongside
	// a step failure, and names aliasDir for manual cleanup. The usage row
	// is keyed on aliasName, not pkg.Name, because install.Install stamps
	// whatever name it's given — dropping it here keeps a tested package
	// from leaving a permanent row in usage.json.
	defer func() {
		if rmErr := os.RemoveAll(aliasDir); rmErr != nil {
			err = errors.Join(err, fmt.Errorf("remove test install %s: %w (remove it by hand)", aliasDir, rmErr))
		}
		key := usage.Key(aliasName, version)
		if rowErr := removeUsageRow(h, key); rowErr != nil {
			err = errors.Join(err, fmt.Errorf("remove usage row %s: %w", key, rowErr))
		}
	}()

	// install.Install derives the install dir from alias.Name, so this is
	// the only change needed to install under the alias, not pkg's own dir.
	alias := *pkg
	alias.Name = aliasName
	if installErr := install.Install(ctx, h, &alias, version, catalogName, artifactPath); installErr != nil {
		return fmt.Errorf("test-install %s@%s: %w", pkg.Name, version, installErr)
	}
	prefix := filepath.Join(aliasDir, version)

	self := build.ResolvedDep{
		Name: alias.Name, Version: version, Prefix: prefix,
		OnPath: true, OnLoaderPath: true, Bins: pkg.Bins, Libs: pkg.Libs,
	}
	entries := append([]build.ResolvedDep{self}, deps...)
	env := build.ComposeEnv(os.Environ(), entries,
		build.EnvContext{Version: version, Platform: plat, Prefix: prefix, AbsRpath: true})
	// ComposeEnv only knows deps, not pkg's own [env] exports; without this
	// a step could pick up a stale value from the invoking shell instead.
	env = applyPackageEnv(pkg, prefix, version, plat, env, rep)

	if mkdirErr := os.MkdirAll(h.Tmp(), 0o755); mkdirErr != nil {
		return fmt.Errorf("create tmp dir: %w", mkdirErr)
	}
	// The BuildStagingInfix in the name is what makes nem clean sweep this
	// directory: a killed run leaves it behind, probe binaries included.
	scratch, scratchErr := os.MkdirTemp(h.Tmp(), pkg.Name+home.BuildStagingInfix+"test-*")
	if scratchErr != nil {
		return fmt.Errorf("create test scratch dir: %w", scratchErr)
	}
	defer os.RemoveAll(scratch)
	// LD_LIBRARY_PATH/DYLD_LIBRARY_PATH are scrubbed so an ambient value
	// can't mask what the loader path below resolves, letting a broken
	// package pass by accident. PWD is forced to scratch because a POSIX
	// shell otherwise resolves it via getcwd(), which follows symlinks and
	// would disagree with the composed NEM_* vars. NEM_STAGING_DIR and
	// NEM_OUTPUT are scrubbed too, so a step never reads a stale value left
	// in the invoking shell.
	env = build.ScrubEnv(env,
		[]string{"PWD", "LD_LIBRARY_PATH", "DYLD_LIBRARY_PATH", "NEM_STAGING_DIR", "NEM_OUTPUT"},
		"PWD="+scratch)

	// Exported via a prologue prepended to the step's script, not set in
	// Cmd.Env, because macOS strips DYLD_* from a restricted binary's
	// environment at exec, and /bin/sh is restricted; a running sh process
	// can still export it for its own children.
	var loaderPrologue string
	if dirs := loaderPathDirs(entries); len(dirs) > 0 {
		value := strings.Join(dirs, string(filepath.ListSeparator))
		loaderPrologue = fmt.Sprintf("export %s=%s\n", envx.LoaderPathVar(), shellQuote(value))
	}

	for i, s := range steps {
		c := exec.CommandContext(ctx, "sh", "-c", loaderPrologue+s.Run)
		c.Dir = scratch
		c.Env = env
		c.Stdout = stdout
		c.Stderr = stderr
		if runErr := c.Run(); runErr != nil {
			return fmt.Errorf("test step %d (%q): %w", i+1, s.Run, runErr)
		}
	}
	noun := "steps"
	if len(steps) == 1 {
		noun = "step"
	}
	rep.Info("Tested %s %s (%d %s)", pkg.Name, version, len(steps), noun)
	return nil
}

// removeUsageRow drops key from the usage index if present. It takes the
// store lock for the load-delete-save sequence, the same shape
// internal/clean's pruneIndex uses, so this does not race a concurrent nem
// clean run.
func removeUsageRow(h home.Home, key string) error {
	release, err := fsx.Lock(h.LockFile())
	if err != nil {
		return err
	}
	defer release()

	idx := usage.Load(h)
	if _, ok := idx[key]; !ok {
		return nil
	}
	delete(idx, key)
	return usage.Save(h, idx)
}

// applyPackageEnv overlays pkg's own [env] exports onto env, mirroring what
// envx.applyPackageExports does for a real install. installDir is the alias
// prefix, since that's where the package actually sits during the test —
// not pkg's real install dir, which may not even exist yet.
func applyPackageEnv(pkg *spec.Package, installDir, version string, plat spec.Platform, env []string, rep report.Reporter) []string {
	vars := map[string]string{}
	for _, export := range pkg.Env {
		if !spec.PlatformsInclude(export.Platforms, plat) {
			continue
		}
		if envx.IsReserved(export.Name) {
			rep.Warn("reserved env var %q from %s skipped", export.Name, pkg.Name)
			continue
		}
		value, err := envx.RenderEnvTemplate(export.Value, installDir, version)
		if err != nil {
			rep.Warn("env %q from %s: %v", export.Name, pkg.Name, err)
			continue
		}
		vars[export.Name] = value
	}
	if len(vars) == 0 {
		return env
	}
	drop := make([]string, 0, len(vars))
	overrides := make([]string, 0, len(vars))
	for name, value := range vars {
		drop = append(drop, name)
		overrides = append(overrides, name+"="+value)
	}
	return build.ScrubEnv(env, drop, overrides...)
}

// loaderPathDirs mirrors envx.buildLoaderPath: each on-loader-path entry
// contributes its Libs joined onto its Prefix, in entries order, deduplicated
// preserving first occurrence. entries here is self followed by deps, so the
// package under test's own lib dirs always precede its dependencies'.
func loaderPathDirs(entries []build.ResolvedDep) []string {
	seen := map[string]bool{}
	var out []string
	for _, d := range entries {
		if !d.OnLoaderPath {
			continue
		}
		for _, lib := range d.Libs {
			dir := filepath.Join(d.Prefix, lib)
			if seen[dir] {
				continue
			}
			seen[dir] = true
			out = append(out, dir)
		}
	}
	return out
}

// shellQuote wraps s in single quotes for safe embedding in a POSIX sh
// script, escaping an embedded single quote by closing the quote, emitting
// a backslash-escaped quote, then reopening it. s here is always a
// filesystem path.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
