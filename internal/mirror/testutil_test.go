package mirror

import (
	"context"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/memory"

	"github.com/vi-dev/nem-cli/internal/publish"
	"github.com/vi-dev/nem-cli/internal/report"
)

// fakeReporter records every task, Info, and Warn call; all its methods are
// safe for concurrent use across the package goroutines Run spawns.
type fakeReporter struct {
	mu    sync.Mutex
	tasks []*fakeTask
	infos []string
	warns []string
}

func (r *fakeReporter) Info(format string, a ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.infos = append(r.infos, fmt.Sprintf(format, a...))
}

func (r *fakeReporter) Warn(format string, a ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.warns = append(r.warns, fmt.Sprintf(format, a...))
}

func (r *fakeReporter) Debug(string, ...any) {}

func (r *fakeReporter) Task(label string) report.Task {
	t := &fakeTask{label: label}
	r.mu.Lock()
	r.tasks = append(r.tasks, t)
	r.mu.Unlock()
	return t
}

func (r *fakeReporter) taskFor(label string) *fakeTask {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, t := range r.tasks {
		if t.label == label {
			return t
		}
	}
	return nil
}

// snapshotTasks returns a stable copy of every task created so far, safe
// to range over while other goroutines may still be creating or
// resolving tasks concurrently.
func (r *fakeReporter) snapshotTasks() []*fakeTask {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*fakeTask(nil), r.tasks...)
}

func (r *fakeReporter) snapshotWarns() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.warns...)
}

func (r *fakeReporter) snapshotInfos() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.infos...)
}

// countCall records one Count(done, total) call.
type countCall struct{ done, total int }

type fakeTask struct {
	label string

	mu        sync.Mutex
	statuses  []string
	counts    []countCall
	done      bool
	failed    bool
	discarded bool
	outcome   string
}

func (t *fakeTask) Status(segment string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.statuses = append(t.statuses, segment)
}
func (t *fakeTask) Progress(int64, int64) {}

func (t *fakeTask) Count(done, total int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.counts = append(t.counts, countCall{done, total})
}

func (t *fakeTask) Done(outcome string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.done = true
	t.outcome = outcome
}

func (t *fakeTask) Fail(outcome string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.failed = true
	t.outcome = outcome
}

func (t *fakeTask) Discard() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.discarded = true
}

func (t *fakeTask) snapshot() (done, failed bool, outcome string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.done, t.failed, t.outcome
}

func (t *fakeTask) wasDiscarded() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.discarded
}

func (t *fakeTask) snapshotCounts() []countCall {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]countCall(nil), t.counts...)
}

func (t *fakeTask) snapshotStatuses() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.statuses...)
}

// countingTarget wraps an oras.ReadOnlyTarget, counting Resolve and Fetch
// calls so a test can prove a read path touched the wrapped target a
// bounded number of times regardless of how much work happens downstream
// of it.
type countingTarget struct {
	oras.ReadOnlyTarget
	resolves atomic.Int64
	fetches  atomic.Int64
}

func (c *countingTarget) Resolve(ctx context.Context, ref string) (ocispec.Descriptor, error) {
	c.resolves.Add(1)
	return c.ReadOnlyTarget.Resolve(ctx, ref)
}

func (c *countingTarget) Fetch(ctx context.Context, d ocispec.Descriptor) (io.ReadCloser, error) {
	c.fetches.Add(1)
	return c.ReadOnlyTarget.Fetch(ctx, d)
}

// rejectPushTarget wraps an oras.Target whose Push always fails, so tests
// can force ocix.CopyTag's retry-then-fail path without a real network.
type rejectPushTarget struct {
	oras.Target
}

func (r *rejectPushTarget) Push(context.Context, ocispec.Descriptor, io.Reader) error {
	return fmt.Errorf("push rejected")
}

// cancelOnPushTarget wraps an oras.Target whose first Push call cancels
// ctx — simulating an interrupt landing mid-copy — and returns
// context.Canceled instead of performing the write.
type cancelOnPushTarget struct {
	oras.Target
	cancel context.CancelFunc
	fired  atomic.Bool
}

func (c *cancelOnPushTarget) Push(context.Context, ocispec.Descriptor, io.Reader) error {
	if c.fired.CompareAndSwap(false, true) {
		c.cancel()
		return context.Canceled
	}
	return nil
}

// newCatalog publishes pkgs (name -> pkg.yaml content) into a fresh
// in-memory target via the real Publish path and returns it alongside the
// tag its default "v2" moving tag resolves to.
func newCatalog(t *testing.T, pkgs map[string]string) (oras.Target, string) {
	t.Helper()
	target := memory.New()
	publish.PublishCatalogForTest(t, target, "example.com/cat", pkgs)
	return target, "v2"
}

// wireCatalog injects src and dst as the catalog openers Run uses,
// ignoring whatever ref string a caller passes — tests address the fixed
// fixtures directly instead of through ref parsing.
func wireCatalog(t *testing.T, src oras.ReadOnlyTarget, srcTag string, dst oras.Target, dstTag string) *bool {
	t.Helper()
	var dstOpened bool
	t.Cleanup(SetSrcCatalogOpener(func(string) (oras.ReadOnlyTarget, string, error) {
		return src, srcTag, nil
	}))
	t.Cleanup(SetDstCatalogOpener(func(string) (oras.Target, string, error) {
		dstOpened = true
		return dst, dstTag, nil
	}))
	return &dstOpened
}

// archiveFixtures holds one side's per-package archive stores, created
// lazily so a package absent from the map still resolves to an empty
// (archive-not-found) store rather than a nil pointer.
type archiveFixtures struct {
	mu     sync.Mutex
	stores map[string]oras.Target
	opened []string
}

func newArchiveFixtures() *archiveFixtures {
	return &archiveFixtures{stores: map[string]oras.Target{}}
}

func (f *archiveFixtures) set(name string, target oras.Target) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stores[name] = target
}

func (f *archiveFixtures) open(name string) oras.Target {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.opened = append(f.opened, name)
	if s, ok := f.stores[name]; ok {
		return s
	}
	s := memory.New()
	f.stores[name] = s
	return s
}

func (f *archiveFixtures) openedNames() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.opened...)
}

// wireArchives injects src and dst as the per-package archives openers Run
// uses.
func wireArchives(t *testing.T, src, dst *archiveFixtures) {
	t.Helper()
	t.Cleanup(SetSrcArchivesOpener(func(_, name string) (oras.ReadOnlyTarget, error) {
		return src.open(name), nil
	}))
	t.Cleanup(SetDstArchivesOpener(func(_, name string) (oras.Target, error) {
		return dst.open(name), nil
	}))
}

// absoluteOCIPkgYAML builds a pkg.yaml for an oci-artifact package whose
// reference points at a registry other than the catalog's own archives
// repo — mirror must leave it alone entirely.
func absoluteOCIPkgYAML(name, ociRef, version string) string {
	return fmt.Sprintf("schema: 2\nname: %s\ndescription: test package\nartifact:\n  oci: %q\ninstall:\n  - extract: {strip: 0}\nversions:\n  - version: %s\n", name, ociRef, version)
}

// TestNoUpstreamHTTPImports statically proves mirror never imports the
// packages that could reach an upstream server directly (net/http, or
// nem's own shared upstream client) — every registry it talks to arrives
// through an injected opener, and archive/catalog content moves through
// ocix's registry-only primitives.
func TestNoUpstreamHTTPImports(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	forbidden := map[string]bool{
		"net/http": true,
		"github.com/vi-dev/nem-cli/internal/netx": true,
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, imp := range file.Imports {
			spec := strings.Trim(imp.Path.Value, `"`)
			if forbidden[spec] {
				t.Fatalf("%s imports %s: mirror must reach only the two registries it is given", name, spec)
			}
		}
	}
}
