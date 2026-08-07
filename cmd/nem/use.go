package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/vi-dev/nem-cli/internal/catalog"
	"github.com/vi-dev/nem-cli/internal/fetch"
	"github.com/vi-dev/nem-cli/internal/fsx"
	"github.com/vi-dev/nem-cli/internal/install"
	"github.com/vi-dev/nem-cli/internal/project"
	"github.com/vi-dev/nem-cli/internal/resolve"
	"github.com/vi-dev/nem-cli/internal/spec"
)

func newUseCmd() *cobra.Command {
	var global bool
	cmd := &cobra.Command{
		Use:   "use [<catalog>:]<pkg>[@<version>]...",
		Short: "Declare and install tools",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUse(cmd, args, global)
		},
	}
	cmd.Flags().BoolVarP(&global, "global", "g", false, "target the global manifest")
	return cmd
}

func newUnuseCmd() *cobra.Command {
	var global bool
	cmd := &cobra.Command{
		Use:   "unuse <pkg>...",
		Short: "Remove declared tools",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUnuse(cmd, args, global)
		},
	}
	cmd.Flags().BoolVarP(&global, "global", "g", false, "target the global manifest")
	return cmd
}

type useArg struct {
	Key     project.ToolKey
	Version string
}

// parseUseArg splits a use argument at its last "@" into the tool key part
// and an optional version, then parses the key part as [<catalog>:]<pkg>.
func parseUseArg(arg string) (useArg, error) {
	keyPart, version := arg, ""
	if i := strings.LastIndex(arg, "@"); i != -1 {
		keyPart, version = arg[:i], arg[i+1:]
	}
	key, err := project.ParseToolKey(keyPart)
	if err != nil {
		return useArg{}, err
	}
	return useArg{Key: key, Version: version}, nil
}

// manifestPath resolves the manifest file use/unuse should operate on:
// nemHome's global manifest under -g, else the project manifest discovered
// from the working directory. createIfMissing lets use start a new project
// manifest in the working directory when none exists yet; unuse instead
// surfaces the missing-manifest error, since there is nothing to remove.
func manifestPath(global, createIfMissing bool) (string, error) {
	if global {
		return nemHome.GlobalManifest(), nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dir, err := project.Discover(cwd)
	if err != nil {
		if createIfMissing && errors.Is(err, project.ErrNoManifest) {
			return filepath.Join(cwd, "nem.toml"), nil
		}
		return "", err
	}
	return filepath.Join(dir, "nem.toml"), nil
}

// loadUseState loads the target manifest plus the catalog config and
// sources use/unuse resolve against.
func loadUseState(path string) (*project.Manifest, *catalog.Config, []catalog.Named, error) {
	manifest, err := project.LoadManifest(path)
	if err != nil {
		return nil, nil, nil, err
	}
	cfg, err := catalog.OpenConfig(nemHome)
	if err != nil {
		return nil, nil, nil, err
	}
	sources, err := catalog.Open(cfg, nemHome)
	if err != nil {
		return nil, nil, nil, err
	}
	return manifest, cfg, sources, nil
}

func manifestTools(m *project.Manifest) []resolve.Tool {
	tools := make([]resolve.Tool, len(m.Tools))
	for i, t := range m.Tools {
		tools[i] = resolve.Tool{Key: t.Key, Version: t.Version}
	}
	return tools
}

// writeManifestAndLock persists manifest and result's lock entries, the
// lockfile sitting next to the manifest.
func writeManifestAndLock(manifest *project.Manifest, result *resolve.Result) error {
	if err := project.WriteManifest(manifest); err != nil {
		return err
	}
	lockPath := filepath.Join(filepath.Dir(manifest.Path), "nem.lock")
	lf := &project.Lockfile{Path: lockPath, Packages: result.Entries}
	return project.WriteLock(lf)
}

// currentPlatformJobs builds install jobs for entries the running platform
// needs, pairing each with the oci ref of the catalog it came from (empty
// for a dir catalog).
func currentPlatformJobs(cfg *catalog.Config, result *resolve.Result) []install.Job {
	current := spec.Current().String()
	var jobs []install.Job
	for _, entry := range result.Entries {
		if !slices.Contains(entry.Platforms, current) {
			continue
		}
		ref := ""
		if e := cfg.Find(entry.Catalog); e != nil && e.Type == "oci" {
			ref = e.Ref
		}
		jobs = append(jobs, install.Job{
			Pkg:     result.Pkgs[entry.Name],
			Version: entry.Version,
			Catalog: entry.Catalog,
			Source:  fetch.Source{CatalogRef: ref},
		})
	}
	return jobs
}

func runUse(cmd *cobra.Command, args []string, global bool) error {
	parsedArgs := make([]useArg, len(args))
	for i, a := range args {
		p, err := parseUseArg(a)
		if err != nil {
			return err
		}
		parsedArgs[i] = p
	}

	release, err := fsx.Lock(nemHome.LockFile())
	if err != nil {
		return err
	}

	path, err := manifestPath(global, true)
	if err != nil {
		release()
		return err
	}
	manifest, cfg, sources, err := loadUseState(path)
	if err != nil {
		release()
		return err
	}

	for _, p := range parsedArgs {
		project.AddTool(manifest, p.Key, p.Version)
	}

	result, err := resolve.Resolve(cmd.Context(), manifestTools(manifest), sources)
	if err != nil {
		release()
		return err
	}

	if err := writeManifestAndLock(manifest, result); err != nil {
		release()
		return err
	}
	release()

	return install.Run(cmd.Context(), nemHome, console, currentPlatformJobs(cfg, result))
}

func runUnuse(cmd *cobra.Command, args []string, global bool) error {
	release, err := fsx.Lock(nemHome.LockFile())
	if err != nil {
		return err
	}
	defer release()

	path, err := manifestPath(global, false)
	if err != nil {
		return err
	}
	manifest, _, sources, err := loadUseState(path)
	if err != nil {
		return err
	}

	for _, name := range args {
		if !project.RemoveTool(manifest, name) {
			return fmt.Errorf("package %s is not declared", name)
		}
	}

	result, err := resolve.Resolve(cmd.Context(), manifestTools(manifest), sources)
	if err != nil {
		return err
	}

	return writeManifestAndLock(manifest, result)
}
