package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/vi-dev/nem-cli/internal/catalog"
	"github.com/vi-dev/nem-cli/internal/fsx"
	"github.com/vi-dev/nem-cli/internal/ocix"
)

// syncCatalog is swapped in tests; production uses the real remote.
var syncCatalog = func(ctx context.Context, ref, storePath string) (string, error) {
	src, srcRef, err := ocix.RemoteCatalog(ref)
	if err != nil {
		return "", err
	}
	return ocix.SyncFrom(ctx, src, srcRef, storePath)
}

func newCatalogCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "catalog",
		Short: "Manage catalogs",
	}
	cmd.AddCommand(newCatalogAddCmd(), newCatalogListCmd(), newCatalogRemoveCmd(), newCatalogUpdateCmd(), newCatalogReorderCmd())
	return cmd
}

func newCatalogAddCmd() *cobra.Command {
	var typeFlag string
	cmd := &cobra.Command{
		Use:   "add <name> <ref>",
		Short: "Add a catalog",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, ref := args[0], args[1]
			entryType := typeFlag
			if entryType == "" {
				if looksLikeDir(ref) {
					entryType = "dir"
				} else {
					entryType = "oci"
				}
			}
			release, err := fsx.Lock(nemHome.LockFile())
			if err != nil {
				return err
			}
			defer release()
			cfg, err := catalog.OpenConfig(nemHome)
			if err != nil {
				return err
			}
			if cfg.Find(name) != nil {
				return fmt.Errorf("catalog %s already exists", name)
			}
			entry := catalog.Entry{Name: name, Type: entryType}
			switch entryType {
			case "dir":
				abs, err := filepath.Abs(ref)
				if err != nil {
					return err
				}
				entry.Path = abs
			case "oci":
				entry.Ref = ref
			default:
				return fmt.Errorf("invalid --type %q (want oci or dir)", entryType)
			}
			cfg.Catalogs = append(cfg.Catalogs, entry)
			if err := catalog.SaveConfig(nemHome, cfg); err != nil {
				return err
			}
			console.Success("Added catalog %s", name)
			if entryType == "oci" {
				console.Hint("Run `nem catalog update " + name + "` to sync it")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&typeFlag, "type", "", "catalog type: oci or dir (default: auto-detect)")
	return cmd
}

func newCatalogListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List configured catalogs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := catalog.OpenConfig(nemHome)
			if err != nil {
				return err
			}
			rows := make([][]string, 0, len(cfg.Catalogs))
			for _, e := range cfg.Catalogs {
				source := e.Ref
				if source == "" {
					source = e.Path
				}
				rows = append(rows, []string{e.Name, e.Type, source})
			}
			console.Table([]string{"name", "type", "source"}, rows)
			return nil
		},
	}
}

func newCatalogRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a catalog",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			release, err := fsx.Lock(nemHome.LockFile())
			if err != nil {
				return err
			}
			defer release()
			cfg, err := catalog.OpenConfig(nemHome)
			if err != nil {
				return err
			}
			idx := slices.IndexFunc(cfg.Catalogs, func(e catalog.Entry) bool { return e.Name == name })
			if idx == -1 {
				return fmt.Errorf("catalog %s is not configured", name)
			}
			// Delete the mirror before the config entry: a missing mirror
			// with a lingering entry is the normal not-synced state and
			// self-heals on update, while the reverse orphans the mirror.
			if err := os.RemoveAll(filepath.Join(nemHome.Root(), "catalogs", name)); err != nil {
				return err
			}
			cfg.Catalogs = slices.Delete(cfg.Catalogs, idx, idx+1)
			if err := catalog.SaveConfig(nemHome, cfg); err != nil {
				return err
			}
			console.Success("Removed catalog %s", name)
			return nil
		},
	}
}

func newCatalogUpdateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "update [name]",
		Short: "Sync oci catalogs from their remote",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			release, err := fsx.Lock(nemHome.LockFile())
			if err != nil {
				return err
			}
			defer release()
			cfg, err := catalog.OpenConfig(nemHome)
			if err != nil {
				return err
			}
			if len(args) == 1 {
				name := args[0]
				entry := cfg.Find(name)
				if entry == nil {
					return fmt.Errorf("catalog %s is not configured", name)
				}
				if entry.Type == "dir" {
					console.Warn("Catalog %s is a dir catalog (nothing to sync)", name)
					return nil
				}
				return syncOne(cmd.Context(), *entry)
			}
			for _, e := range cfg.Catalogs {
				if e.Type != "oci" {
					continue
				}
				if err := syncOne(cmd.Context(), e); err != nil {
					return err
				}
			}
			return nil
		},
	}
}

func syncOne(ctx context.Context, e catalog.Entry) error {
	start := time.Now()
	if _, err := syncCatalog(ctx, e.Ref, nemHome.CatalogStore(e.Name)); err != nil {
		return err
	}
	console.Success("Synced catalog %s%s", e.Name, durSuffix(time.Since(start)))
	return nil
}

func newCatalogReorderCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reorder <name>...",
		Short: "Reorder catalog precedence",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			release, err := fsx.Lock(nemHome.LockFile())
			if err != nil {
				return err
			}
			defer release()
			cfg, err := catalog.OpenConfig(nemHome)
			if err != nil {
				return err
			}
			if err := cfg.Reorder(args); err != nil {
				return err
			}
			if err := catalog.SaveConfig(nemHome, cfg); err != nil {
				return err
			}
			console.Success("Reordered catalogs")
			return nil
		},
	}
}

// durSuffix renders the output convention's duration suffix: empty under 1s,
// else e.g. " (3s)" or " (1m23s)".
func durSuffix(d time.Duration) string {
	if d < time.Second {
		return ""
	}
	d = d.Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf(" (%ds)", int(d.Seconds()))
	}
	return fmt.Sprintf(" (%dm%02ds)", int(d.Minutes()), int(d.Seconds())%60)
}

func looksLikeDir(ref string) bool {
	if strings.HasPrefix(ref, "/") || strings.HasPrefix(ref, "./") || strings.HasPrefix(ref, "../") {
		return true
	}
	info, err := os.Stat(ref)
	return err == nil && info.IsDir()
}
