package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"

	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"github.com/vi-dev/nem-cli/internal/discover"
	"github.com/vi-dev/nem-cli/internal/fetch"
	"github.com/vi-dev/nem-cli/internal/fsx"
	"github.com/vi-dev/nem-cli/internal/spec"
)

// bumpList and bumpDigest are package vars so tests can stub upstream
// listing and artifact hashing.
var (
	bumpList   = discover.List
	bumpDigest = fetch.DigestURL
)

// bumpRow is one manifest's sweep result; the --json shape is the
// contract catalog automation consumes. Head stays equal to Current for
// an up-to-date package and empty when the package failed.
type bumpRow struct {
	Name    string   `json:"name"`
	Path    string   `json:"path"`
	Current string   `json:"current"`
	Head    string   `json:"head,omitempty"`
	Added   []string `json:"added,omitempty"`
	Error   string   `json:"error,omitempty"`
}

func newCatalogBumpCmd() *cobra.Command {
	var version string
	var backfill int
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "bump [dir|pkg.yaml]",
		Short: "Add newer upstream versions to package manifests",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if version != "" && backfill > 0 {
				return fmt.Errorf("--version and --backfill cannot be combined")
			}
			target := "."
			if len(args) == 1 {
				target = args[0]
			}
			info, err := os.Stat(target)
			if err != nil {
				return err
			}
			if info.IsDir() {
				if version != "" {
					return fmt.Errorf("--version needs a single pkg.yaml target, not a directory")
				}
				return bumpSweep(cmd.Context(), target, backfill, jsonOut)
			}
			path := target
			data, pkg, current, err := bumpLoad(path)
			if err != nil {
				return err
			}
			if err := spec.ValidateEditable(data); err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
			row := bumpRow{Name: pkg.Name, Path: path, Current: current}
			emit := func() error {
				if !jsonOut {
					return nil
				}
				return console.JSON([]bumpRow{row})
			}

			var targets []string
			if version != "" {
				if hasEqualVersion(pkg, version) {
					console.Success("%s %s already present", pkg.Name, version)
					row.Head = current
					return emit()
				}
				targets = []string{version}
			} else {
				if targets, err = candidateVersions(cmd.Context(), pkg, backfill); err != nil {
					return err
				}
				if len(targets) == 0 {
					console.Success("%s up to date (%s)", pkg.Name, displayVersion(current))
					row.Head = current
					return emit()
				}
			}

			added, head, err := bumpApply(cmd.Context(), path, data, pkg, targets)
			if err != nil {
				return err
			}
			printBumpResult(pkg.Name, current, head, added)
			row.Head, row.Added = head, added
			return emit()
		},
	}
	cmd.Flags().StringVar(&version, "version", "", "version to add (default: every version newer than the current latest)")
	cmd.Flags().IntVar(&backfill, "backfill", 0, "also ensure the newest <n> discovered versions have entries")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit every attempted package as JSON")
	return cmd
}

// bumpSweep runs a discovery-driven bump over every manifest in a
// catalog directory, several packages at a time. Per-package failures
// are reported and counted, not fatal: a flaky upstream must not abort
// the rest of the sweep.
func bumpSweep(ctx context.Context, dir string, backfill int, jsonOut bool) error {
	paths, err := manifestPaths(dir)
	if err != nil {
		return err
	}
	results := make([]*bumpRow, len(paths))
	var g errgroup.Group
	// A bumping package already downloads its platform artifacts in
	// parallel, so a small package limit keeps the download fan-out
	// modest.
	g.SetLimit(4)
	for i, path := range paths {
		g.Go(func() error {
			results[i] = bumpSweepOne(ctx, path, backfill)
			return nil
		})
	}
	_ = g.Wait() // workers record failures in their row and never fail the group

	var rows []bumpRow
	var bumped, upToDate, failed, skipped int
	for _, r := range results {
		if r == nil {
			skipped++
			continue
		}
		rows = append(rows, *r)
		switch {
		case r.Error != "":
			failed++
		case len(r.Added) > 0:
			bumped++
		default:
			upToDate++
		}
	}
	if jsonOut {
		if err := console.JSON(rows); err != nil {
			return err
		}
	}
	console.Success("Checked %d packages: %d bumped, %d up to date, %d failed, %d without discovery",
		len(paths), bumped, upToDate, failed, skipped)
	return nil
}

// bumpSweepOne bumps a single manifest within a sweep, reporting the
// outcome as a row instead of an error. A nil row marks a package
// without versionDiscovery, which a sweep can only skip.
func bumpSweepOne(ctx context.Context, path string, backfill int) *bumpRow {
	row := &bumpRow{Name: filepath.Base(filepath.Dir(path)), Path: path}
	fail := func(err error) *bumpRow {
		console.Warn("%s: %v", row.Name, err)
		row.Error = err.Error()
		return row
	}
	data, pkg, current, err := bumpLoad(path)
	if err != nil {
		return fail(err)
	}
	row.Name, row.Current = pkg.Name, current
	if pkg.VersionDiscovery == nil {
		console.Debug("Skipping %s: no versionDiscovery", pkg.Name)
		return nil
	}
	if err := spec.ValidateEditable(data); err != nil {
		return fail(err)
	}
	targets, err := candidateVersions(ctx, pkg, backfill)
	if err != nil {
		return fail(err)
	}
	if len(targets) == 0 {
		console.Debug("%s up to date (%s)", pkg.Name, displayVersion(current))
		row.Head = current
		return row
	}
	added, head, err := bumpApply(ctx, path, data, pkg, targets)
	if err != nil {
		return fail(err)
	}
	row.Head, row.Added = head, added
	printBumpResult(pkg.Name, current, head, added)
	return row
}

// bumpLoad reads and parses a manifest, returning its raw bytes, the
// package, and the current head version ("" when it has none).
func bumpLoad(path string) ([]byte, *spec.Package, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, "", err
	}
	pkg, err := spec.Parse(data)
	if err != nil {
		return nil, nil, "", fmt.Errorf("%s: %w", path, err)
	}
	current := ""
	if len(pkg.Versions) > 0 {
		current = pkg.Versions[0].Version
	}
	return data, pkg, current, nil
}

// displayVersion renders an absent version as "none" for console output.
func displayVersion(v string) string {
	if v == "" {
		return "none"
	}
	return v
}

// bumpApply hashes every target, inserts the entries newest-first,
// validates the result, and writes it back atomically. It returns the
// versions actually added and the resulting head version. A target whose
// artifacts can't all be fetched is skipped with a warning so the rest
// still land; only a run where nothing lands is an error.
func bumpApply(ctx context.Context, path string, data []byte, pkg *spec.Package, targets []string) ([]string, string, error) {
	edited := data
	var added []string
	var lastErr error
	notFound, sourceBackfill := false, false
	for _, target := range targets {
		entry, err := buildEntry(ctx, pkg, target)
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
			return nil, "", err
		}
		if edited, err = spec.InsertVersionAt(edited, entry, pos); err != nil {
			return nil, "", err
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
		return nil, "", fmt.Errorf("no versions could be added: %w", lastErr)
	}

	check, err := spec.Parse(edited)
	if err != nil {
		return nil, "", fmt.Errorf("edited manifest is invalid: %w", err)
	}
	if err := check.Validate(); err != nil {
		return nil, "", fmt.Errorf("edited manifest is invalid: %w", err)
	}
	// The whole edited list must be strictly newest-first — a
	// manifest that already violates the ordering is a lint
	// finding to fix, not state bump papers over.
	for _, v := range added {
		if !hasVersionEntry(check, v) {
			return nil, "", fmt.Errorf("edited manifest is missing %s", v)
		}
	}
	for i := 1; i < len(check.Versions); i++ {
		if spec.CompareVersions(check.Versions[i-1].Version, check.Versions[i].Version) <= 0 {
			return nil, "", fmt.Errorf("edited manifest is not newest-first at %s", check.Versions[i].Version)
		}
	}
	if err := fsx.WriteAtomic(path, edited, 0o644); err != nil {
		return nil, "", err
	}
	return added, check.Versions[0].Version, nil
}

// printBumpResult reports one manifest's outcome: an unchanged head is a
// pure backfill, an advanced head is a bump. current is the raw prior
// head ("" when the manifest had none).
func printBumpResult(name, current, head string, added []string) {
	switch {
	case head == current:
		word := "versions"
		if len(added) == 1 {
			word = "version"
		}
		console.Success("Backfilled %s (%d %s)", name, len(added), word)
	case len(added) > 1:
		console.Success("Bumped %s %s → %s (%d versions)", name, displayVersion(current), head, len(added))
	default:
		console.Success("Bumped %s %s → %s", name, displayVersion(current), head)
	}
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
	if pkg.Artifact.OCI != "" {
		return buildOCIEntry(ctx, pkg, target)
	}

	e := spec.VersionEntry{Version: target}
	platforms := pkg.SupportedBy()

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
			sum, err := bumpDigest(gctx, http.DefaultClient, url, meta, task)
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
	subject := pkg.Name + " " + target + " source"
	task := console.Task("Hashing " + subject)
	meta := fetch.Meta{Name: pkg.Name, Version: target, Platform: spec.Current()}
	sum, err := bumpDigest(ctx, http.DefaultClient, url, meta, task)
	if err != nil {
		task.Fail("Download failed for " + subject)
		// A missing source tarball is a dead or wrong URL, not a
		// release still uploading — surface the URL, not a retry hint.
		var nf *fetch.ArtifactNotFoundError
		if errors.As(err, &nf) {
			return e, fmt.Errorf("source for %s@%s not found: %s", pkg.Name, target, url)
		}
		return e, err
	}
	task.Done("Hashed " + subject)
	e.SourceSha256 = sum
	return e, nil
}
