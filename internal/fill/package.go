package fill

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"

	"github.com/vi-dev/nem-cli/internal/fetch"
	"github.com/vi-dev/nem-cli/internal/home"
	"github.com/vi-dev/nem-cli/internal/ocix"
	"github.com/vi-dev/nem-cli/internal/report"
	"github.com/vi-dev/nem-cli/internal/spec"
)

// fillPackage creates its task before probing so in-flight work renders
// live; a package with nothing to do discards it. If a version's batched
// index commit fails, its platforms count failed, not filled/healed.
func fillPackage(ctx context.Context, h home.Home, opts Options, nm ocix.TitledManifest, store *ocix.Store, rep report.Reporter, agg *aggregator) {
	label := fmt.Sprintf("Filling %s", nm.Title)
	failedOutcome := fmt.Sprintf("Failed %s", nm.Title)

	task := rep.Task(label)
	task.Status("probing")

	data, _, err := store.PkgBytes(ctx, nm.Title)
	if err != nil {
		agg.failed.Add(1)
		rep.Warn("%s: %v", nm.Title, err)
		task.Fail(failedOutcome)
		return
	}
	pkg, err := spec.Parse(data)
	if err != nil {
		agg.failed.Add(1)
		rep.Warn("%s: %v", nm.Title, err)
		task.Fail(failedOutcome)
		return
	}

	if pkg.Artifact.OCI != "" {
		agg.notFillable.Add(1)
		task.Discard()
		return
	}
	if len(pkg.Versions) == 0 {
		task.Discard()
		return
	}

	archives, err := openArchives(opts.CatalogRef, nm.Title)
	if err != nil {
		agg.failed.Add(1)
		rep.Warn("%s: %v", nm.Title, err)
		task.Fail(failedOutcome)
		return
	}

	filled, healed := 0, 0
	failed := false
	for _, v := range pkg.Versions {
		cancelled := false
		var batch []batchedManifest
		for _, plat := range pkg.SupportedBy() {
			if ctx.Err() != nil {
				cancelled = true
				break
			}
			outcome, entry := fillItem(ctx, h, archives, pkg, v.Version, plat, opts.DryRun, rep, task)
			if outcome == outcomeCancelled {
				cancelled = true
				break
			}
			switch outcome {
			case outcomePresent:
				agg.present.Add(1)
			case outcomeFilled:
				if opts.DryRun {
					agg.filled.Add(1)
					filled++
				} else {
					batch = append(batch, batchedManifest{entry, false})
				}
			case outcomeHealed:
				if opts.DryRun {
					agg.healed.Add(1)
					healed++
				} else {
					batch = append(batch, batchedManifest{entry, true})
				}
			case outcomeFailed:
				agg.failed.Add(1)
				failed = true
			}
		}

		if len(batch) > 0 {
			entries := make([]ocispec.Descriptor, len(batch))
			for i, b := range batch {
				entries[i] = b.desc
			}
			if _, err := ocix.CommitArchiveManifests(ctx, archives, v.Version, entries); err != nil {
				if report.IsCancellation(err) {
					cancelled = true
				} else {
					agg.failed.Add(int64(len(batch)))
					failed = true
					rep.Warn("%s %s: publish archive index: %v", pkg.Name, v.Version, err)
				}
			} else {
				for _, b := range batch {
					if b.heal {
						agg.healed.Add(1)
						healed++
					} else {
						agg.filled.Add(1)
						filled++
					}
				}
			}
		}

		if cancelled {
			break
		}
	}

	switch {
	case ctx.Err() != nil:
		task.Fail(fmt.Sprintf("Cancelled %s", nm.Title))
	case failed:
		task.Fail(failedOutcome)
	case filled > 0 || healed > 0:
		verb := "Filled"
		if opts.DryRun {
			verb = "Would fill"
		}
		task.Done(fmt.Sprintf("%s %s (%d fill(s), %d heal(s))", verb, nm.Title, filled, healed))
	default:
		task.Discard()
	}
}

type itemOutcome int

const (
	outcomePresent itemOutcome = iota
	outcomeFilled
	outcomeHealed
	outcomeFailed
	outcomeCancelled // ctx cancellation: neither warned nor counted as a failure
)

type batchedManifest struct {
	desc ocispec.Descriptor
	heal bool
}

// fillItem's returned descriptor is set only for a non-dry-run fill or
// heal: the platform's manifest, published but not yet committed — the
// caller batches the version's index commit.
func fillItem(ctx context.Context, h home.Home, archives oras.Target, pkg *spec.Package, version string, plat spec.Platform, dryRun bool, rep report.Reporter, task report.Task) (itemOutcome, ocispec.Descriptor) {
	sha, err := pkg.Sha256(version, plat)
	if err != nil {
		rep.Warn("%s %s %s: %v", pkg.Name, version, plat, err)
		return outcomeFailed, ocispec.Descriptor{}
	}
	want := digest.NewDigestFromEncoded(digest.SHA256, sha)

	got, err := ocix.ArchiveLayerDigest(ctx, archives, version, plat)
	switch {
	case errors.Is(err, ocix.ErrArchiveNotFound):
		return doFill(ctx, h, archives, pkg, version, plat, sha, dryRun, false, rep, task)
	case err != nil:
		if report.IsCancellation(err) {
			return outcomeCancelled, ocispec.Descriptor{}
		}
		rep.Warn("%s %s %s: %v", pkg.Name, version, plat, err)
		return outcomeFailed, ocispec.Descriptor{}
	case got == want:
		return outcomePresent, ocispec.Descriptor{}
	default:
		return doFill(ctx, h, archives, pkg, version, plat, sha, dryRun, true, rep, task)
	}
}

func doFill(ctx context.Context, h home.Home, archives oras.Target, pkg *spec.Package, version string, plat spec.Platform, sha string, dryRun, heal bool, rep report.Reporter, task report.Task) (itemOutcome, ocispec.Descriptor) {
	outcome, verb, wouldVerb := outcomeFilled, "filling", "would fill"
	if heal {
		outcome, verb, wouldVerb = outcomeHealed, "healing", "would heal"
	}

	if dryRun {
		task.Status(fmt.Sprintf("%s %s %s", wouldVerb, version, plat))
		return outcome, ocispec.Descriptor{}
	}
	task.Status(fmt.Sprintf("%s %s %s", verb, version, plat))

	url, err := fetch.UpstreamURL(pkg, version, plat)
	if err != nil {
		rep.Warn("%s %s %s: %v", pkg.Name, version, plat, err)
		return outcomeFailed, ocispec.Descriptor{}
	}
	meta := fetch.Meta{Name: pkg.Name, Version: version, Platform: plat}
	path, err := fetch.Download(ctx, httpClient, url, sha, h.Tmp(), meta, nil)
	if err != nil {
		if report.IsCancellation(err) {
			return outcomeCancelled, ocispec.Descriptor{}
		}
		rep.Warn("%s %s %s: %v", pkg.Name, version, plat, err)
		return outcomeFailed, ocispec.Descriptor{}
	}
	defer os.Remove(path)

	info, err := os.Stat(path)
	if err != nil {
		rep.Warn("%s %s %s: %v", pkg.Name, version, plat, err)
		return outcomeFailed, ocispec.Descriptor{}
	}

	entry, err := ocix.PublishArchiveLayerFile(ctx, archives, plat, path, sha, info.Size())
	if err != nil {
		if report.IsCancellation(err) {
			return outcomeCancelled, ocispec.Descriptor{}
		}
		rep.Warn("%s %s %s: %v", pkg.Name, version, plat, err)
		return outcomeFailed, ocispec.Descriptor{}
	}

	return outcome, entry
}
