package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"sort"

	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"github.com/vi-dev/nem-cli/internal/discover"
	"github.com/vi-dev/nem-cli/internal/fetch"
	"github.com/vi-dev/nem-cli/internal/spec"
)

// bumpList and bumpDownload are package vars so tests can stub
// upstream listing and artifact downloads.
var (
	bumpList     = discover.List
	bumpDownload = fetch.DownloadUnverified
)

func newCatalogBumpCmd() *cobra.Command {
	var version string
	var backfill int
	cmd := &cobra.Command{
		Use:   "bump <pkg.yaml>",
		Short: "Add newer upstream versions to a package manifest",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if version != "" && backfill > 0 {
				return fmt.Errorf("--version and --backfill cannot be combined")
			}
			path := args[0]
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			pkg, err := spec.Parse(data)
			if err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
			if err := spec.ValidateEditable(data); err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
			current := "none"
			if len(pkg.Versions) > 0 {
				current = pkg.Versions[0].Version
			}

			var targets []string
			if version != "" {
				if hasEqualVersion(pkg, version) {
					console.Success("%s %s already present", pkg.Name, version)
					return nil
				}
				targets = []string{version}
			} else {
				if targets, err = candidateVersions(cmd.Context(), pkg, backfill); err != nil {
					return err
				}
				if len(targets) == 0 {
					console.Success("%s up to date (%s)", pkg.Name, current)
					return nil
				}
			}

			// Hash every target; a target whose artifacts can't all be
			// fetched is skipped with a warning so the rest still land.
			edited := data
			var added []string
			var lastErr error
			notFound, sourceBackfill := false, false
			for _, target := range targets {
				entry, err := buildEntry(cmd.Context(), pkg, target)
				if err != nil {
					lastErr = err
					var nf *fetch.ArtifactNotFoundError
					if errors.As(err, &nf) {
						notFound = true
					}
					console.Warn("%s %s: %v", pkg.Name, target, err)
					continue
				}
				pos, err := insertPos(edited, target)
				if err != nil {
					return err
				}
				if edited, err = spec.InsertVersionAt(edited, entry, pos); err != nil {
					return err
				}
				added = append(added, target)
				if pkg.Build != nil && len(pkg.Versions) > 0 && spec.CompareVersions(target, pkg.Versions[0].Version) < 0 {
					console.Warn("%s %s is source-built and its archive is not published yet", pkg.Name, target)
					sourceBackfill = true
				}
			}
			if notFound {
				console.Hint("Upstream release assets may not be uploaded yet; retry later")
			}
			if sourceBackfill {
				console.Hint("Backfilled source versions need `nem catalog build --version <v> --push` before installs work")
			}
			if len(added) == 0 {
				return fmt.Errorf("no versions could be added: %w", lastErr)
			}

			check, err := spec.Parse(edited)
			if err != nil {
				return fmt.Errorf("edited manifest is invalid: %w", err)
			}
			if err := check.Validate(); err != nil {
				return fmt.Errorf("edited manifest is invalid: %w", err)
			}
			// The whole edited list must be strictly newest-first — a
			// manifest that already violates the ordering is a lint
			// finding to fix, not state bump papers over.
			for _, v := range added {
				if !hasVersionEntry(check, v) {
					return fmt.Errorf("edited manifest is missing %s", v)
				}
			}
			for i := 1; i < len(check.Versions); i++ {
				if spec.CompareVersions(check.Versions[i-1].Version, check.Versions[i].Version) <= 0 {
					return fmt.Errorf("edited manifest is not newest-first at %s", check.Versions[i].Version)
				}
			}
			if err := writeManifest(path, edited); err != nil {
				return err
			}

			head := check.Versions[0].Version
			switch {
			case head == current:
				word := "versions"
				if len(added) == 1 {
					word = "version"
				}
				console.Success("Backfilled %s (%d %s)", pkg.Name, len(added), word)
			case len(added) > 1:
				console.Success("Bumped %s %s → %s (%d versions)", pkg.Name, current, head, len(added))
			default:
				console.Success("Bumped %s %s → %s", pkg.Name, current, head)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&version, "version", "", "version to add (default: every version newer than the current latest)")
	cmd.Flags().IntVar(&backfill, "backfill", 0, "also ensure the newest <n> discovered versions have entries")
	return cmd
}

// candidateVersions returns the discovered versions to add, oldest first:
// everything newer than the manifest's current latest, plus — when
// backfill > 0 — whichever of the newest backfill discovered versions
// are missing from the manifest.
func candidateVersions(ctx context.Context, pkg *spec.Package, backfill int) ([]string, error) {
	discovered, err := bumpList(ctx, pkg)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var all []string
	for _, v := range discovered {
		if !seen[v] {
			seen[v] = true
			all = append(all, v)
		}
	}
	sort.Slice(all, func(i, j int) bool { return spec.CompareVersions(all[i], all[j]) > 0 })

	current := ""
	if len(pkg.Versions) > 0 {
		current = pkg.Versions[0].Version
	}
	var out []string
	for i, v := range all {
		newer := current != "" && spec.CompareVersions(v, current) > 0
		if !newer && (backfill <= 0 || i >= backfill) {
			continue
		}
		if hasEqualVersion(pkg, v) {
			continue
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return spec.CompareVersions(out[i], out[j]) < 0 })
	return out, nil
}

// hasEqualVersion reports whether pkg already carries v under any
// spelling — "1.3.1" matches an existing "v1.3.1" — so an equal version
// is never added twice.
func hasEqualVersion(pkg *spec.Package, v string) bool {
	return slices.ContainsFunc(pkg.Versions, func(e spec.VersionEntry) bool {
		return spec.CompareVersions(v, e.Version) == 0
	})
}

// insertPos returns the index in data's versions list where version
// belongs to keep the list newest-first.
func insertPos(data []byte, version string) (int, error) {
	pkg, err := spec.Parse(data)
	if err != nil {
		return 0, fmt.Errorf("edited manifest is invalid: %w", err)
	}
	for i, v := range pkg.Versions {
		if spec.CompareVersions(version, v.Version) > 0 {
			return i, nil
		}
	}
	return len(pkg.Versions), nil
}

// buildEntry assembles the version entry for target: per-platform
// artifact hashes for url/github artifacts, a pinned source hash for
// source-built packages, a bare version for external oci artifacts.
func buildEntry(ctx context.Context, pkg *spec.Package, target string) (spec.VersionEntry, error) {
	e := spec.VersionEntry{Version: target}
	if pkg.Artifact.OCI != "" {
		return buildOCIEntry(ctx, pkg, target)
	}

	platforms := pkg.SupportedBy()
	tmpDir, cleanup, err := bumpTmpDir()
	if err != nil {
		return e, err
	}
	defer cleanup()

	sums := make([]string, len(platforms))
	g, gctx := errgroup.WithContext(ctx)
	for i, plat := range platforms {
		g.Go(func() error {
			url, err := fetch.UpstreamURL(pkg, target, plat)
			if err != nil {
				return err
			}
			subject := pkg.Name + " " + target + " " + plat.String()
			task := console.Task("Hashing " + subject)
			meta := fetch.Meta{Name: pkg.Name, Version: target, Platform: plat}
			_, sum, err := bumpDownload(gctx, http.DefaultClient, url, tmpDir, meta, task)
			if err != nil {
				task.Fail("Download failed for " + subject)
				return err
			}
			task.Done("Hashed " + subject)
			sums[i] = sum
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return e, err
	}
	e.Sha256 = make(map[string]string, len(platforms))
	for i, plat := range platforms {
		e.Sha256[plat.String()] = sums[i]
	}
	return e, nil
}

// buildOCIEntry handles oci-artifact packages: source-built ones pin a
// source hash; external ones need only the version (registry digests
// verify the artifact).
func buildOCIEntry(ctx context.Context, pkg *spec.Package, target string) (spec.VersionEntry, error) {
	e := spec.VersionEntry{Version: target}
	if pkg.Build == nil {
		return e, nil
	}
	url, err := pkg.BuildSourceURL(target, spec.Current())
	if err != nil {
		return e, err
	}
	tmpDir, cleanup, err := bumpTmpDir()
	if err != nil {
		return e, err
	}
	defer cleanup()
	subject := pkg.Name + " " + target + " source"
	task := console.Task("Hashing " + subject)
	meta := fetch.Meta{Name: pkg.Name, Version: target, Platform: spec.Current()}
	_, sum, err := bumpDownload(ctx, http.DefaultClient, url, tmpDir, meta, task)
	if err != nil {
		task.Fail("Download failed for " + subject)
		return e, err
	}
	task.Done("Hashed " + subject)
	e.SourceSha256 = sum
	return e, nil
}

// bumpTmpDir creates a scratch dir under $NEM_HOME/tmp for downloads.
func bumpTmpDir() (string, func(), error) {
	if err := os.MkdirAll(nemHome.Tmp(), 0o755); err != nil {
		return "", nil, err
	}
	dir, err := os.MkdirTemp(nemHome.Tmp(), "bump-")
	if err != nil {
		return "", nil, err
	}
	return dir, func() { os.RemoveAll(dir) }, nil
}
