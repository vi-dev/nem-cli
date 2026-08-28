// Package mirror replicates a catalog and its archives byte-for-byte
// between two registries, rebuilding nothing.
package mirror

import (
	"context"
	"fmt"
	"runtime"
	"sync/atomic"

	"golang.org/x/sync/errgroup"
	"oras.land/oras-go/v2"

	"github.com/vi-dev/nem-cli/internal/ocix"
	"github.com/vi-dev/nem-cli/internal/report"
)

type Options struct {
	SrcRef, DstRef string
	DryRun         bool
}

// Summary is the result of a mirror run.
type Summary struct {
	Packages int // every package in the source catalog, participating or not
	Copied   int
	Failed   int
	DryRun   bool
}

func (s Summary) String() string {
	verb := "Mirrored"
	if s.DryRun {
		verb = "Would mirror"
	}
	if s.Failed > 0 {
		return fmt.Sprintf("%s %d packages, %d tag(s), %d tag(s) failed",
			verb, s.Packages, s.Copied, s.Failed)
	}
	return fmt.Sprintf("%s %d packages, %d tag(s)", verb, s.Packages, s.Copied)
}

var (
	openSrcCatalog  = ocix.RemoteCatalog
	openDstCatalog  = ocix.RemoteCatalogRW
	openSrcArchives = ocix.RemoteArchives
	openDstArchives = ocix.RemoteArchivesRW
)

func SetSrcCatalogOpener(f func(ref string) (oras.ReadOnlyTarget, string, error)) (restore func()) {
	prev := openSrcCatalog
	openSrcCatalog = f
	return func() { openSrcCatalog = prev }
}

func SetDstCatalogOpener(f func(ref string) (oras.Target, string, error)) (restore func()) {
	prev := openDstCatalog
	openDstCatalog = f
	return func() { openDstCatalog = prev }
}

func SetSrcArchivesOpener(f func(catalogRef, name string) (oras.ReadOnlyTarget, error)) (restore func()) {
	prev := openSrcArchives
	openSrcArchives = f
	return func() { openSrcArchives = prev }
}

func SetDstArchivesOpener(f func(catalogRef, name string) (oras.Target, error)) (restore func()) {
	prev := openDstArchives
	openDstArchives = f
	return func() { openDstArchives = prev }
}

// Run syncs the catalog and mirrors every package's declared archive
// tags in parallel. A non-nil error means the run aborted; per-item
// failures are counted only in Summary.Failed, which the caller must
// check for a nonzero exit.
func Run(ctx context.Context, opts Options, rep report.Reporter) (Summary, error) {
	if err := ocix.WithTagOrDigest(opts.SrcRef); err != nil {
		return Summary{}, err
	}
	if err := ocix.WithTagOrDigest(opts.DstRef); err != nil {
		return Summary{}, err
	}
	if err := ctx.Err(); err != nil {
		return Summary{}, err
	}

	store, err := syncCatalog(ctx, opts, rep)
	if err != nil {
		return Summary{}, err
	}

	pkgs := store.Packages()
	summary := Summary{Packages: len(pkgs), DryRun: opts.DryRun}
	var agg aggregator

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(min(runtime.NumCPU(), 8))
	for _, pkg := range pkgs {
		g.Go(func() error {
			if gctx.Err() != nil {
				return nil
			}
			mirrorPackage(gctx, opts, pkg, store, rep, &agg)
			return nil
		})
	}
	_ = g.Wait()

	summary.Copied = int(agg.copied.Load())
	summary.Failed = int(agg.failed.Load())

	if ctx.Err() != nil {
		return summary, ctx.Err()
	}
	return summary, nil
}

type aggregator struct {
	copied, failed atomic.Int64
}

func syncCatalog(ctx context.Context, opts Options, rep report.Reporter) (*ocix.Store, error) {
	store, err := pullCatalog(ctx, opts.SrcRef, rep)
	if err != nil {
		return nil, err
	}
	if !opts.DryRun {
		if err := pushCatalog(ctx, store, opts.DstRef, rep); err != nil {
			return nil, err
		}
	}
	return store, nil
}

// pullCatalog stages srcRef's index closure into memory — the run's only
// read of the source catalog.
func pullCatalog(ctx context.Context, srcRef string, rep report.Reporter) (*ocix.Store, error) {
	labels := report.TaskLabels{Run: "Pulling catalog", Status: "copying", Done: "Pulled catalog", Fail: "Pull failed"}
	var store *ocix.Store
	err := report.RunTask(rep, labels, func(progress func(done, total int64)) error {
		src, srcTag, err := openSrcCatalog(srcRef)
		if err != nil {
			return err
		}
		store, err = ocix.OpenStoreInMemory(ctx, src, srcTag, progress)
		if err != nil {
			return fmt.Errorf("stage catalog: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return store, nil
}

func pushCatalog(ctx context.Context, store *ocix.Store, dstRef string, rep report.Reporter) error {
	labels := report.TaskLabels{Run: "Pushing catalog", Status: "copying", Done: "Pushed catalog", Fail: "Push failed"}
	return report.RunTask(rep, labels, func(progress func(done, total int64)) error {
		dst, dstTag, err := openDstCatalog(dstRef)
		if err != nil {
			return err
		}
		pushed, err := store.CopyTo(ctx, dst, dstTag, progress)
		if err != nil {
			return fmt.Errorf("publish catalog: %w", err)
		}
		got, err := dst.Resolve(ctx, dstTag)
		if err != nil {
			return fmt.Errorf("verify published catalog: %w", err)
		}
		if got.Digest != pushed.Digest {
			return fmt.Errorf("catalog %s digest mismatch after publish: copied %s, resolved %s", dstTag, pushed.Digest, got.Digest)
		}
		return nil
	})
}
