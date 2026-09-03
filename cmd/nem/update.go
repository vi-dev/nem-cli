package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/vi-dev/nem-cli/internal/config"
	"github.com/vi-dev/nem-cli/internal/fsx"
	"github.com/vi-dev/nem-cli/internal/install"
	"github.com/vi-dev/nem-cli/internal/ocix"
	"github.com/vi-dev/nem-cli/internal/project"
	"github.com/vi-dev/nem-cli/internal/resolve"
	"github.com/vi-dev/nem-cli/internal/spec"
)

func newUpdateCmd() *cobra.Command {
	var global, dryRun bool
	cmd := &cobra.Command{
		Use:     "update [<pkg>...]",
		Aliases: []string{"up"},
		Short:   "Update declared tools to their latest versions",
		Args:    cobra.ArbitraryArgs,
		ValidArgsFunction: func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			return completeDeclaredPackages(global, args, toComplete)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUpdate(cmd, args, global, dryRun)
		},
	}
	cmd.Flags().BoolVarP(&global, "global", "g", false, "target the global manifest")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "report the update plan without writing anything")
	return cmd
}

// target is one tool update floats: its manifest key and the version
// declared before floating.
type target struct {
	key      project.ToolKey
	declared string
}

// selectTargets maps args to declared tools in manifest order: no args
// selects every declared tool, otherwise each arg names a declared one,
// with or without its catalog qualifier.
func selectTargets(manifest *project.Manifest, args []string) ([]target, error) {
	declared := make(map[string]project.ToolEntry, len(manifest.Tools))
	for _, t := range manifest.Tools {
		declared[t.Key.Name] = t
	}
	selected := make(map[string]bool, len(args))
	for _, a := range args {
		if strings.Contains(a, "@") {
			return nil, fmt.Errorf("update takes package names without versions; run `nem use %s` to pin a version", a)
		}
		key, err := project.ParseToolKey(a)
		if err != nil {
			return nil, err
		}
		entry, ok := declared[key.Name]
		if !ok {
			return nil, fmt.Errorf("package %s is not declared", a)
		}
		if key.Catalog != "" && key.Catalog != entry.Key.Catalog {
			return nil, fmt.Errorf("package %s is declared as %s", a, entry.Key)
		}
		selected[key.Name] = true
	}
	var targets []target
	for _, t := range manifest.Tools {
		if len(args) == 0 || selected[t.Key.Name] {
			targets = append(targets, target{key: t.Key, declared: t.Version})
		}
	}
	return targets, nil
}

// planUpdate runs everything the process lock guards and, unless
// dryRun, persists manifest and lock.
func planUpdate(cmd *cobra.Command, args []string, global, dryRun bool) (*config.Config, *resolve.Result, [][]string, error) {
	release, err := fsx.Lock(nemHome.LockFile())
	if err != nil {
		return nil, nil, nil, err
	}
	defer release()

	path, err := manifestPath(global, false)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := requireGlobalManifest(global, path); err != nil {
		return nil, nil, nil, err
	}
	manifest, cfg, sources, err := loadUseState(path)
	if err != nil {
		return nil, nil, nil, err
	}
	targets, err := selectTargets(manifest, args)
	if err != nil {
		return nil, nil, nil, err
	}

	// A cleared version floats the tool at resolve time.
	for _, t := range targets {
		project.AddTool(manifest, t.key, "")
	}

	result, err := resolveManifest(cmd, manifest, cfg, sources)
	if err != nil {
		return nil, nil, nil, err
	}
	resolved := resolvedVersions(result)
	warnStaleCatalogs(cfg, result)

	// update never downgrades.
	for _, t := range targets {
		if t.declared == "" {
			continue
		}
		if v, ok := resolved[t.key.Name]; ok && spec.CompareVersions(v, t.declared) < 0 {
			return nil, nil, nil, fmt.Errorf("refusing to downgrade %s from %s to %s; run `nem use %s@%s` to accept the downgrade",
				t.key.Name, t.declared, v, t.key.String(), v)
		}
	}

	var rows [][]string
	var pinKeys []project.ToolKey
	for _, t := range targets {
		pinKeys = append(pinKeys, t.key)
		if v, ok := resolved[t.key.Name]; ok && v != t.declared {
			rows = append(rows, []string{t.key.Name, t.declared, v})
		}
	}

	if !dryRun {
		if err := pinResolved(manifest, result, pinKeys); err != nil {
			return nil, nil, nil, err
		}
	}
	return cfg, result, rows, nil
}

func runUpdate(cmd *cobra.Command, args []string, global, dryRun bool) error {
	cfg, result, rows, err := planUpdate(cmd, args, global, dryRun)
	if err != nil {
		return err
	}
	if dryRun {
		reportUpdatePlan(rows)
		return nil
	}

	if err := install.Run(cmd.Context(), nemHome, console, currentPlatformJobs(cfg, result)); err != nil {
		return err
	}

	// "Updated" states a completed fact, so it prints only after install.
	if len(rows) == 0 {
		console.Info("All tools up to date")
	}
	for _, r := range rows {
		console.Success("Updated %s %s → %s", r[0], r[1], r[2])
	}
	return nil
}

// staleSyncAfter is how old a catalog's local mirror may grow before
// update warns that "latest" may trail the registry.
const staleSyncAfter = 7 * 24 * time.Hour

// warnStaleCatalogs warns for each oci catalog the resolution drew from
// whose mirror was last synced more than staleSyncAfter ago.
func warnStaleCatalogs(cfg *config.Config, result *resolve.Result) {
	warned := false
	seen := map[string]bool{}
	for _, e := range result.Entries {
		if seen[e.Catalog] {
			continue
		}
		seen[e.Catalog] = true
		entry := cfg.Find(e.Catalog)
		if entry == nil || entry.Type != "oci" {
			continue
		}
		store, err := nemHome.CatalogStore(e.Catalog)
		if err != nil {
			continue
		}
		synced, err := ocix.LastSynced(store)
		if err != nil {
			continue
		}
		age := time.Since(synced)
		if age < staleSyncAfter {
			continue
		}
		console.Warn("Catalog %s last synced %s ago", e.Catalog, syncAgePhrase(age))
		warned = true
	}
	if warned {
		console.Hint("Run `nem catalog update` to refresh catalogs")
	}
}

// syncAgePhrase renders a mirror's sync age in whole days for the stale
// warning; callers only pass ages of a week or more.
func syncAgePhrase(age time.Duration) string {
	days := int(age.Hours() / 24)
	if days <= 1 {
		return "1 day"
	}
	return fmt.Sprintf("%d days", days)
}

// reportUpdatePlan prints the changed tools as a table, or narrates that
// nothing is outdated.
func reportUpdatePlan(rows [][]string) {
	if len(rows) == 0 {
		console.Info("All tools up to date")
		return
	}
	console.Table([]string{"tool", "current", "latest"}, rows)
}
