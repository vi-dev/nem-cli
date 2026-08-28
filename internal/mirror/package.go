package mirror

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"oras.land/oras-go/v2"

	"github.com/vi-dev/nem-cli/internal/ocix"
	"github.com/vi-dev/nem-cli/internal/report"
	"github.com/vi-dev/nem-cli/internal/spec"
)

// mirrorPackage creates its task before probing so in-flight work renders
// live; a package with nothing to do discards it.
func mirrorPackage(ctx context.Context, opts Options, manifest ocix.TitledManifest, store *ocix.Store, rep report.Reporter, agg *aggregator) {
	label := fmt.Sprintf("Mirroring %s", manifest.Title)
	failedOutcome := fmt.Sprintf("Failed %s", manifest.Title)

	task := rep.Task(label)
	task.Status("probing")

	data, _, err := store.PkgBytes(ctx, manifest.Title)
	if err != nil {
		agg.failed.Add(1)
		rep.Warn("%s: %v", manifest.Title, err)
		task.Fail(failedOutcome)
		return
	}
	pkg, err := spec.Parse(data)
	if err != nil {
		agg.failed.Add(1)
		rep.Warn("%s: %v", manifest.Title, err)
		task.Fail(failedOutcome)
		return
	}

	participates, prebuilt := classify(pkg)
	if !participates || len(pkg.Versions) == 0 {
		task.Discard()
		return
	}

	src, err := openSrcArchives(opts.SrcRef, manifest.Title)
	if err != nil {
		agg.failed.Add(1)
		rep.Warn("%s: %v", manifest.Title, err)
		task.Fail(failedOutcome)
		return
	}
	dst, err := openDstArchives(opts.DstRef, manifest.Title)
	if err != nil {
		agg.failed.Add(1)
		rep.Warn("%s: %v", manifest.Title, err)
		task.Fail(failedOutcome)
		return
	}

	copied := 0
	failed := false
	for _, v := range pkg.Versions {
		if ctx.Err() != nil {
			break
		}
		outcome := mirrorVersion(ctx, src, dst, manifest.Title, v.Version, prebuilt, opts.DryRun, rep, task)
		if outcome == outcomeCancelled {
			break
		}
		switch outcome {
		case outcomeCopied:
			agg.copied.Add(1)
			copied++
		case outcomeFailed:
			agg.failed.Add(1)
			failed = true
		}
	}

	switch {
	case ctx.Err() != nil:
		task.Fail(fmt.Sprintf("Cancelled %s", manifest.Title))
	case failed:
		task.Fail(failedOutcome)
	case copied > 0:
		verb := "Mirrored"
		if opts.DryRun {
			verb = "Would mirror"
		}
		task.Done(fmt.Sprintf("%s %s (%d tag(s))", verb, manifest.Title, copied))
	default:
		task.Discard()
	}
}

// classify reports whether pkg participates in archive mirroring and
// whether it's prebuilt (oci-artifact). Absolute oci references don't
// participate: each platform pulls those directly from where they point.
func classify(pkg *spec.Package) (participates, prebuilt bool) {
	if pkg.Artifact.OCI != "" {
		return ociRefIsRelative(pkg.Artifact.OCI), true
	}
	return true, false
}

// ociRefIsRelative classifies the raw template: the leading character
// that decides relative vs absolute is never itself a placeholder, so no
// rendering is needed.
func ociRefIsRelative(tmpl string) bool {
	return tmpl == "" || strings.HasPrefix(tmpl, ":") || strings.HasPrefix(tmpl, "@")
}

type versionOutcome int

const (
	outcomePresent versionOutcome = iota
	outcomeUnfilled
	outcomeCopied
	outcomeFailed
	outcomeCancelled // ctx cancellation: neither warned nor counted as a failure
)

func mirrorVersion(ctx context.Context, src oras.ReadOnlyTarget, dst oras.Target, name, version string, prebuilt, dryRun bool, rep report.Reporter, task report.Task) versionOutcome {
	srcDesc, err := ocix.ResolveArchiveTag(ctx, src, version)
	switch {
	case errors.Is(err, ocix.ErrArchiveNotFound):
		if prebuilt {
			rep.Warn("%s %s: archive missing from source", name, version)
			return outcomeFailed
		}
		return outcomeUnfilled
	case err != nil:
		if report.IsCancellation(err) {
			return outcomeCancelled
		}
		rep.Warn("%s %s: %v", name, version, err)
		return outcomeFailed
	}

	dstDesc, err := ocix.ResolveArchiveTag(ctx, dst, version)
	switch {
	case err == nil:
		if dstDesc.Digest == srcDesc.Digest {
			return outcomePresent
		}
	case !errors.Is(err, ocix.ErrArchiveNotFound):
		if report.IsCancellation(err) {
			return outcomeCancelled
		}
		rep.Warn("%s %s: %v", name, version, err)
		return outcomeFailed
	}

	if dryRun {
		task.Status(fmt.Sprintf("would copy %s", version))
		return outcomeCopied
	}
	task.Status(fmt.Sprintf("copying %s", version))
	if _, err := ocix.CopyTag(ctx, src, dst, version); err != nil {
		if report.IsCancellation(err) {
			return outcomeCancelled
		}
		rep.Warn("%s %s: %v", name, version, err)
		return outcomeFailed
	}
	return outcomeCopied
}
