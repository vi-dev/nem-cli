package install

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"

	"golang.org/x/sync/errgroup"

	"github.com/vi-dev/nem-cli/internal/fetch"
	"github.com/vi-dev/nem-cli/internal/home"
	"github.com/vi-dev/nem-cli/internal/report"
	"github.com/vi-dev/nem-cli/internal/spec"
)

// Job is one package version to install: the parsed package, the version to
// install, the lock entry's catalog name, and where its artifact came from.
type Job struct {
	Pkg     *spec.Package
	Version string
	Catalog string // lock entry catalog name
	Source  fetch.Source
}

// acquire resolves and verifies a job's artifact; a package var so tests can
// replace it without a real network fetch.
var acquire = fetch.Acquire

// Run installs jobs in parallel, capped at min(runtime.NumCPU(), 8)
// concurrent installs, and fails fast: the first job error cancels every
// other job, and that error is returned once every job has unwound. If
// every job instead skipped or was reported cancelled without an error of
// its own — e.g. ctx was already cancelled before any job got to run —
// Run still returns ctx.Err() rather than nil, so an aborted run is never
// mistaken for success.
//
// A job whose target is already installed is skipped without a report
// task. Every other job gets one task labelled "Installing <name>
// <version>": Status "downloading" while its artifact is acquired (the
// task is passed through so acquire can feed Progress), then "extracting"
// while it's staged and committed, then Done "Installed <name> <version>".
// A job that fails on its own error ends its task with "Failed <name>
// <version>" and propagates the error for the caller to render once.
// A job that never got to start, or that was interrupted mid-run, because
// a sibling failed first reports "Cancelled <name> <version>" instead of
// propagating the raw context error. Its downloaded artifact, if any, is
// removed once the job ends, whichever way it ends.
func Run(ctx context.Context, h home.Home, rep report.Reporter, jobs []Job) error {
	if err := os.MkdirAll(h.Tmp(), 0o755); err != nil {
		return fmt.Errorf("create tmp dir: %w", err)
	}

	g, gctx := errgroup.WithContext(ctx)
	sem := make(chan struct{}, min(runtime.NumCPU(), 8))

	for _, job := range jobs {
		g.Go(func() error { return runJob(gctx, h, rep, job, sem) })
	}
	if err := g.Wait(); err != nil {
		return err
	}
	return ctx.Err()
}

// runJob installs one job, or reports it cancelled if gctx ends before or
// during the attempt; see Run's doc comment for the reporting contract.
func runJob(gctx context.Context, h home.Home, rep report.Reporter, job Job, sem chan struct{}) error {
	name, version := job.Pkg.Name, job.Version
	if IsInstalled(h, name, version) {
		return nil
	}

	label := fmt.Sprintf("Installing %s %s", name, version)
	cancelled := fmt.Sprintf("Cancelled %s %s", name, version)
	// The task outcome names the job, not the error: the error is returned
	// and rendered once by the caller, so echoing it here printed it twice.
	failedOutcome := fmt.Sprintf("Failed %s %s", name, version)

	select {
	case sem <- struct{}{}:
		defer func() { <-sem }()
	case <-gctx.Done():
		rep.Task(label).Fail(cancelled)
		return nil
	}

	task := rep.Task(label)
	if isCancellation(gctx, nil) {
		task.Fail(cancelled)
		return nil
	}

	task.Status("downloading")
	artifact, err := acquire(gctx, job.Pkg, version, spec.Current(), job.Source, h.Tmp(), task)
	if err != nil {
		if isCancellation(gctx, err) {
			task.Fail(cancelled)
			return nil
		}
		task.Fail(failedOutcome)
		return err
	}
	// Best-effort cleanup: the artifact is scratch space once install has
	// consumed it, so a removal failure here has nothing further to report
	// to.
	defer os.Remove(artifact)

	task.Status("extracting")
	if err := Install(gctx, h, job.Pkg, version, job.Catalog, artifact); err != nil {
		if isCancellation(gctx, err) {
			task.Fail(cancelled)
			return nil
		}
		task.Fail(failedOutcome)
		return err
	}

	task.Done(fmt.Sprintf("Installed %s %s", name, version))
	return nil
}

// isCancellation reports whether err reflects context cancellation rather
// than a job's own failure. With no error to inspect (err == nil — a job
// that never got to start), it falls back to gctx's own state; given a
// real error it trusts only that error, so a sibling's cancellation
// racing this job's unrelated failure never masks the real reason.
func isCancellation(gctx context.Context, err error) bool {
	if err != nil {
		return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
	}
	return gctx.Err() != nil
}
