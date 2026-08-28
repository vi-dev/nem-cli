// Package fill downloads a catalog's sha-pinned upstream artifacts and
// publishes them as archives.
package fill

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync/atomic"

	"golang.org/x/sync/errgroup"
	"oras.land/oras-go/v2"

	"github.com/vi-dev/nem-cli/internal/home"
	"github.com/vi-dev/nem-cli/internal/netx"
	"github.com/vi-dev/nem-cli/internal/ocix"
	"github.com/vi-dev/nem-cli/internal/report"
)

type Options struct {
	CatalogRef string
	Pkgs       []string // empty = every package in the catalog
	DryRun     bool
}

// Summary counts Filled, Healed, and Present per (version, platform)
// item but NotFillable per package, so the fields don't reconcile
// against Packages. Failed drives the exit code and is not printed.
type Summary struct {
	Packages    int // after --pkg scoping
	Filled      int
	Healed      int
	Present     int
	NotFillable int
	Failed      int
	DryRun      bool
}

func (s Summary) String() string {
	verb := "Filled"
	if s.DryRun {
		verb = "Would fill"
	}
	return fmt.Sprintf("%s %d packages, %d fill(s), %d heal(s), %d present, %d package(s) not fillable",
		verb, s.Packages, s.Filled, s.Healed, s.Present, s.NotFillable)
}

var (
	openCatalog  = ocix.RemoteCatalog
	openArchives = ocix.RemoteArchivesRW
)

func SetCatalogOpener(f func(ref string) (oras.ReadOnlyTarget, string, error)) (restore func()) {
	prev := openCatalog
	openCatalog = f
	return func() { openCatalog = prev }
}

func SetArchivesOpener(f func(catalogRef, name string) (oras.Target, error)) (restore func()) {
	prev := openArchives
	openArchives = f
	return func() { openArchives = prev }
}

var httpClient = netx.Client()

func SetHTTPClient(c *http.Client) (restore func()) {
	prev := httpClient
	httpClient = c
	return func() { httpClient = prev }
}

// Run stages the catalog and fills every scoped package's archives in
// parallel. A non-nil error means the run aborted; per-item failures are
// counted only in Summary.Failed, which the caller must check for a
// nonzero exit.
func Run(ctx context.Context, h home.Home, opts Options, rep report.Reporter) (Summary, error) {
	if err := ocix.WithTagOrDigest(opts.CatalogRef); err != nil {
		return Summary{}, err
	}
	if err := ctx.Err(); err != nil {
		return Summary{}, err
	}

	store, err := stageCatalog(ctx, opts.CatalogRef, rep)
	if err != nil {
		return Summary{}, err
	}

	pkgs, err := scopePackages(store.Packages(), opts.Pkgs)
	if err != nil {
		return Summary{}, err
	}

	if !opts.DryRun {
		if err := os.MkdirAll(h.Tmp(), 0o755); err != nil {
			return Summary{}, fmt.Errorf("create tmp dir: %w", err)
		}
	}

	summary := Summary{Packages: len(pkgs), DryRun: opts.DryRun}
	var agg aggregator

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(min(runtime.NumCPU(), 8))
	for _, nm := range pkgs {
		g.Go(func() error {
			if gctx.Err() != nil {
				return nil
			}
			fillPackage(gctx, h, opts, nm, store, rep, &agg)
			return nil
		})
	}
	_ = g.Wait()

	summary.Filled = int(agg.filled.Load())
	summary.Healed = int(agg.healed.Load())
	summary.Present = int(agg.present.Load())
	summary.NotFillable = int(agg.notFillable.Load())
	summary.Failed = int(agg.failed.Load())

	if ctx.Err() != nil {
		return summary, ctx.Err()
	}
	return summary, nil
}

// stageCatalog stages ref's index closure into memory — the run's only
// read of the catalog.
func stageCatalog(ctx context.Context, ref string, rep report.Reporter) (*ocix.Store, error) {
	labels := report.TaskLabels{Run: "Pulling catalog", Status: "copying", Done: "Pulled catalog", Fail: "Pull failed"}
	var store *ocix.Store
	err := report.RunTask(rep, labels, func(count func(done, total int64)) error {
		src, srcTag, err := openCatalog(ref)
		if err != nil {
			return err
		}
		store, err = ocix.OpenStoreInMemory(ctx, src, srcTag, count)
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

func scopePackages(all []ocix.TitledManifest, want []string) ([]ocix.TitledManifest, error) {
	if len(want) == 0 {
		return all, nil
	}
	byName := make(map[string]ocix.TitledManifest, len(all))
	for _, nm := range all {
		byName[nm.Title] = nm
	}
	seen := make(map[string]bool, len(want))
	var out []ocix.TitledManifest
	var unknown []string
	for _, w := range want {
		if seen[w] {
			continue
		}
		seen[w] = true
		if nm, ok := byName[w]; ok {
			out = append(out, nm)
		} else {
			unknown = append(unknown, w)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, fmt.Errorf("unknown package(s): %s", strings.Join(unknown, ", "))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Title < out[j].Title })
	return out, nil
}

type aggregator struct {
	filled, healed, present, notFillable, failed atomic.Int64
}
