package publish

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/errgroup"
	"oras.land/oras-go/v2"

	"github.com/vi-dev/nem-cli/internal/ocix"
	"github.com/vi-dev/nem-cli/internal/report"
	"github.com/vi-dev/nem-cli/internal/spec"
)

// Options configures a Publish call.
type Options struct {
	// Tags are the moving tags to apply to the pushed catalog index, in
	// addition to the immutable release tag Publish always adds. Defaults
	// to ["v2"] when empty.
	Tags []string
	// DryRun reports the publish plan without opening the target or
	// writing anything.
	DryRun bool
	// Force pushes every package manifest even when its content already
	// exists in the target, bypassing the skip-unchanged check.
	Force bool
}

// nowFunc is the clock Publish uses to compute the release tag's
// timestamp; a package var so tests can inject a fixed time.
var nowFunc = time.Now

// openTarget opens ref as a writable oras target; a package var so tests
// can inject an in-memory store instead of a real registry.
var openTarget = func(ctx context.Context, ref string) (oras.Target, error) {
	t, _, err := ocix.RemoteCatalogRW(ref)
	return t, err
}

// SetTargetOpenerForTest swaps the target opener Publish uses and returns
// a closure that restores the previous one. Test-only.
func SetTargetOpenerForTest(f func(context.Context, string) (oras.Target, error)) (restore func()) {
	prev := openTarget
	openTarget = f
	return func() { openTarget = prev }
}

// pkgEntry is one enumerated package ready to publish.
type pkgEntry struct {
	Bytes       []byte
	Name        string
	Description string
	Version     string
}

// Publish lints dir, then pushes every package it contains to ref as a
// nem v2 catalog: an empty shared config, one image manifest per package,
// and a catalog index referencing them all. Tags move only after the
// index push succeeds, so a failure anywhere before that point leaves
// every existing tag untouched.
//
// Any lint finding blocks every registry write — dir is not even parsed
// beyond what linting itself requires. A dry run reports the publish plan
// and returns before the target is opened. A package whose manifest
// content already exists in the target is skipped unless Force is set.
func Publish(ctx context.Context, dir, ref string, opts Options, r report.Reporter) error {
	if err := ocix.ValidateRef(ref); err != nil {
		return err
	}

	findings, err := Lint(dir)
	if err != nil {
		return err
	}
	if len(findings) > 0 {
		return lintError(findings)
	}

	entries, err := enumerate(dir)
	if err != nil {
		return err
	}

	tags := effectiveTags(opts.Tags, nowFunc())

	if opts.DryRun {
		reportPlan(r, ref, entries, tags)
		return nil
	}

	target, err := openTarget(ctx, ref)
	if err != nil {
		return fmt.Errorf("open %s: %w", ref, err)
	}
	if err := ocix.PushEmptyConfig(ctx, target); err != nil {
		return fmt.Errorf("push empty config: %w", err)
	}

	idxEntries, pushed, skipped, err := pushPackages(ctx, target, entries, opts.Force, r)
	if err != nil {
		return err
	}

	idxBytes, idxDesc := ocix.AssembleIndex(idxEntries)
	if err := ocix.PushIndex(ctx, target, idxBytes, idxDesc, tags); err != nil {
		return fmt.Errorf("push catalog index: %w", err)
	}

	r.Info("published %s: %d pushed, %d unchanged, tags %s", ref, pushed, skipped, strings.Join(tags, ", "))
	return nil
}

// effectiveTags computes the tags a publish moves: opts' tags (defaulting
// to ["v2"] when empty) plus an immutable release tag derived from now.
func effectiveTags(optTags []string, now time.Time) []string {
	base := optTags
	if len(base) == 0 {
		base = []string{"v2"}
	}
	release := "v2." + now.UTC().Format("20060102T150405Z")
	return append(append([]string(nil), base...), release)
}

// lintError renders findings as a single error listing every one of them.
func lintError(findings []Finding) error {
	msgs := make([]string, len(findings))
	for i, f := range findings {
		msgs[i] = f.String()
	}
	return fmt.Errorf("catalog lint failed:\n%s", strings.Join(msgs, "\n"))
}

// reportPlan narrates what a real publish would do: the ref, every
// package that would be pushed, and the tags that would move. It never
// touches the target, so it cannot report skip-vs-push per package —
// that decision depends on what the target already holds.
func reportPlan(r report.Reporter, ref string, entries []pkgEntry, tags []string) {
	r.Info("dry run: publish %s (%d packages)", ref, len(entries))
	for _, e := range entries {
		r.Info("  %s %s", e.Name, e.Version)
	}
	r.Info("tags: %s", strings.Join(tags, ", "))
}

// enumerate walks dir the same way Lint does — pkgs/<name>/pkg.yaml under
// a catalog directory, or a single pkg.yaml file — and parses each
// manifest into a pkgEntry. Lint has already gated on findings by the
// time this runs, so parse/validate failures here are unexpected and
// surfaced as plain I/O errors rather than Findings.
func enumerate(dir string) ([]pkgEntry, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", dir, err)
	}
	if !info.IsDir() {
		e, err := readEntry(dir)
		if err != nil {
			return nil, err
		}
		return []pkgEntry{e}, nil
	}

	pkgsDir := filepath.Join(dir, "pkgs")
	dirEntries, err := os.ReadDir(pkgsDir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", pkgsDir, err)
	}

	var out []pkgEntry
	for _, de := range dirEntries {
		if !de.IsDir() {
			continue
		}
		e, err := readEntry(filepath.Join(pkgsDir, de.Name(), "pkg.yaml"))
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

// readEntry reads and parses one pkg.yaml into a pkgEntry, using its
// first (latest) declared version.
func readEntry(path string) (pkgEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return pkgEntry{}, fmt.Errorf("read %s: %w", path, err)
	}
	pkg, err := spec.Parse(data)
	if err != nil {
		return pkgEntry{}, fmt.Errorf("parse %s: %w", path, err)
	}
	var version string
	if len(pkg.Versions) > 0 {
		version = pkg.Versions[0].Version
	}
	return pkgEntry{Bytes: data, Name: pkg.Name, Description: pkg.Description, Version: version}, nil
}

// pushPackages pushes every entry into target concurrently, bounded to
// min(runtime.NumCPU(), 8) at once, and returns one ocix.IndexEntry per
// package plus the pushed/skipped counts. A package whose manifest
// content already exists in target is skipped unless force is set. The
// first push failure cancels every other in-flight push; its error is
// returned once every goroutine has unwound.
func pushPackages(ctx context.Context, target oras.Target, entries []pkgEntry, force bool, r report.Reporter) ([]ocix.IndexEntry, int, int, error) {
	idxEntries := make([]ocix.IndexEntry, len(entries))
	var pushed, skipped atomic.Int64

	g, gctx := errgroup.WithContext(ctx)
	sem := make(chan struct{}, min(runtime.NumCPU(), 8))

	for i, e := range entries {
		g.Go(func() error {
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-gctx.Done():
				return gctx.Err()
			}
			entry, wasPushed, err := pushOne(gctx, target, e, force, r)
			if err != nil {
				return err
			}
			idxEntries[i] = entry
			if wasPushed {
				pushed.Add(1)
			} else {
				skipped.Add(1)
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, 0, 0, err
	}
	return idxEntries, int(pushed.Load()), int(skipped.Load()), nil
}

// pushOne pushes a single package's manifest into target, or skips the
// push when its content already exists and force is false. It always
// returns the index entry the package contributes to the catalog index,
// whichever path it took.
func pushOne(ctx context.Context, target oras.Target, e pkgEntry, force bool, r report.Reporter) (ocix.IndexEntry, bool, error) {
	desc, err := ocix.PackageManifestDescriptor(e.Bytes)
	if err != nil {
		return ocix.IndexEntry{}, false, fmt.Errorf("compute manifest descriptor for %s: %w", e.Name, err)
	}

	if !force {
		exists, err := ocix.ManifestExists(ctx, target, desc)
		if err != nil {
			return ocix.IndexEntry{}, false, fmt.Errorf("check %s: %w", e.Name, err)
		}
		if exists {
			r.Info("skip %s %s (unchanged)", e.Name, e.Version)
			return indexEntry(e, desc), false, nil
		}
	}

	pushed, err := ocix.PushPackageManifest(ctx, target, e.Bytes)
	if err != nil {
		return ocix.IndexEntry{}, false, fmt.Errorf("push %s: %w", e.Name, err)
	}
	r.Info("push %s %s", e.Name, e.Version)
	return indexEntry(e, pushed), true, nil
}

// indexEntry builds the ocix.IndexEntry a package contributes to the
// catalog index from its metadata and its (pushed or pre-existing)
// manifest descriptor.
func indexEntry(e pkgEntry, desc ocispec.Descriptor) ocix.IndexEntry {
	return ocix.IndexEntry{Manifest: desc, Title: e.Name, Description: e.Description, Version: e.Version}
}
