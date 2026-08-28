package mirror

import (
	"context"
	"errors"
	"strings"
	"testing"

	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/memory"

	"github.com/vi-dev/nem-cli/internal/ocix"
	"github.com/vi-dev/nem-cli/internal/publish"
)

func urlPkg(name, version, digest string) string {
	return string(publish.URLPkgYAML(name, version, "https://example.com/"+name+"/{{.Version}}", publish.UniformSha256(digest)))
}

func ociRelativePkg(name, version string) string {
	return string(publish.OCIPkgYAML(name, version))
}

// TestRunStagesCatalogAndReadsSrcIndexOnce proves package enumeration and
// pkg.yaml reads never touch src again once staging has returned. The
// invariant isn't a small constant — it's "exactly staging's own cost,
// no more": the test learns that cost from syncCatalog alone against a
// fresh counter, then asserts a full Run against an identical counter
// on the same fixture produces the exact same count.
func TestRunStagesCatalogAndReadsSrcIndexOnce(t *testing.T) {
	pkgs := map[string]string{}
	for i := 0; i < 8; i++ {
		name := "pkg" + string(rune('a'+i))
		pkgs[name] = urlPkg(name, "1.0.0", "deadbeef")
	}
	srcStore, srcTag := newCatalog(t, pkgs)
	opts := Options{SrcRef: "example.com/cat:v2", DstRef: "internal.example.com/cat:v2"}

	stagingOnly := &countingTarget{ReadOnlyTarget: srcStore}
	wireCatalog(t, stagingOnly, srcTag, memory.New(), "v2")
	if _, err := syncCatalog(context.Background(), opts, &fakeReporter{}); err != nil {
		t.Fatalf("syncCatalog: %v", err)
	}
	wantResolves, wantFetches := stagingOnly.resolves.Load(), stagingOnly.fetches.Load()
	if wantFetches == 0 {
		t.Fatal("staging must touch src at all")
	}
	// The index ref is resolved exactly once: a single resolve+copy of the
	// closure, with no separate pre-validation resolve beforehand. A
	// reintroduced double-fetch of the index (e.g. a pre-copy FetchIndex)
	// would push this to 2 — unlike wantFetches below, this bound isn't
	// self-referential, so it actually catches that regression.
	if wantResolves != 1 {
		t.Fatalf("src resolves during staging = %d, want exactly 1 (the index ref, resolved once)", wantResolves)
	}

	counted := &countingTarget{ReadOnlyTarget: srcStore}
	wireCatalog(t, counted, srcTag, memory.New(), "v2")
	wireArchives(t, newArchiveFixtures(), newArchiveFixtures())

	rep := &fakeReporter{}
	summary, err := Run(context.Background(), opts, rep)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if summary.Packages != 8 {
		t.Fatalf("Packages = %d, want 8", summary.Packages)
	}
	if got := counted.resolves.Load(); got != wantResolves {
		t.Fatalf("src catalog resolves = %d, want exactly staging's own cost %d (per-package processing must never touch src again)", got, wantResolves)
	}
	if got := counted.fetches.Load(); got != wantFetches {
		t.Fatalf("src catalog fetches = %d, want exactly staging's own cost %d (per-package processing must never touch src again)", got, wantFetches)
	}
}

// TestRunPublishesCatalogDigestChecked proves the catalog closure staged
// from src lands on dst byte-identical: dst resolves the mirrored tag to
// the exact same digest src's index carried.
func TestRunPublishesCatalogDigestChecked(t *testing.T) {
	srcStore, srcTag := newCatalog(t, map[string]string{"go": urlPkg("go", "1.0.0", "aaaa")})
	wantDesc, err := srcStore.Resolve(context.Background(), srcTag)
	if err != nil {
		t.Fatalf("resolve src: %v", err)
	}

	dst := memory.New()
	wireCatalog(t, srcStore, srcTag, dst, "mirrored")
	wireArchives(t, newArchiveFixtures(), newArchiveFixtures())

	if _, err := Run(context.Background(), Options{SrcRef: "example.com/cat:v2", DstRef: "internal.example.com/cat:mirrored"}, &fakeReporter{}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	gotDesc, err := dst.Resolve(context.Background(), "mirrored")
	if err != nil {
		t.Fatalf("resolve dst: %v", err)
	}
	if gotDesc.Digest != wantDesc.Digest {
		t.Fatalf("dst catalog digest = %s, want %s (byte-identical to src)", gotDesc.Digest, wantDesc.Digest)
	}
}

// TestRunDryRunSkipsCatalogPublishEntirely proves --dry-run never even
// opens the dst catalog target, let alone writes to it.
func TestRunDryRunSkipsCatalogPublishEntirely(t *testing.T) {
	srcStore, srcTag := newCatalog(t, map[string]string{"go": urlPkg("go", "1.0.0", "aaaa")})
	dst := memory.New()
	dstOpened := wireCatalog(t, srcStore, srcTag, dst, "v2")
	wireArchives(t, newArchiveFixtures(), newArchiveFixtures())

	summary, err := Run(context.Background(), Options{SrcRef: "example.com/cat:v2", DstRef: "internal.example.com/cat:v2", DryRun: true}, &fakeReporter{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if *dstOpened {
		t.Fatal("dry run must never open the dst catalog target")
	}
	if summary.Packages != 1 {
		t.Fatalf("Packages = %d, want 1", summary.Packages)
	}
}

// TestRunCopiesAbsentAndHealsDifferingArchives covers presence, fresh
// copy, and heal-by-digest together: three url packages each pinned to
// version "1.0.0" — one already present on dst byte-identical to src, one
// absent from dst, one present but stale (different payload) — and checks
// both the Summary counts and that every dst tag ends up digest-identical
// to its src counterpart.
func TestRunCopiesAbsentAndHealsDifferingArchives(t *testing.T) {
	pkgs := map[string]string{
		"present": urlPkg("present", "1.0.0", "aaaa"),
		"absent":  urlPkg("absent", "1.0.0", "bbbb"),
		"stale":   urlPkg("stale", "1.0.0", "cccc"),
	}
	srcStore, srcTag := newCatalog(t, pkgs)

	src := newArchiveFixtures()
	dst := newArchiveFixtures()
	for name, payload := range map[string][]byte{
		"present": []byte("present-payload"),
		"absent":  []byte("absent-payload"),
		"stale":   []byte("stale-payload-new"),
	} {
		s := memory.New()
		ocix.PushFakeArchive(t, s, "1.0.0", map[string][]byte{"linux/amd64": payload})
		src.set(name, s)
	}
	presentDst := memory.New()
	ocix.PushFakeArchive(t, presentDst, "1.0.0", map[string][]byte{"linux/amd64": []byte("present-payload")})
	dst.set("present", presentDst)
	staleDst := memory.New()
	ocix.PushFakeArchive(t, staleDst, "1.0.0", map[string][]byte{"linux/amd64": []byte("stale-payload-old")})
	dst.set("stale", staleDst)

	dstCatalog := memory.New()
	wireCatalog(t, srcStore, srcTag, dstCatalog, "v2")
	wireArchives(t, src, dst)

	rep := &fakeReporter{}
	summary, err := Run(context.Background(), Options{SrcRef: "example.com/cat:v2", DstRef: "internal.example.com/cat:v2"}, rep)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if summary.Copied != 2 || summary.Failed != 0 {
		t.Fatalf("summary = %+v, want {Copied:2}", summary)
	}

	for _, name := range []string{"present", "absent", "stale"} {
		srcDesc, err := src.open(name).Resolve(context.Background(), "1.0.0")
		if err != nil {
			t.Fatalf("%s: resolve src: %v", name, err)
		}
		dstDesc, err := dst.open(name).Resolve(context.Background(), "1.0.0")
		if err != nil {
			t.Fatalf("%s: resolve dst: %v", name, err)
		}
		if srcDesc.Digest != dstDesc.Digest {
			t.Fatalf("%s: dst digest %s != src digest %s", name, dstDesc.Digest, srcDesc.Digest)
		}
	}
}

// TestRunUnfilledIsSilent proves a url/github package with no src
// archive produces no Info/Warn line, no completion line, and no summary
// count — the expected, quiet not-yet-filled state. Its task still
// exists (created eagerly on pickup, for the in-flight "probing" line),
// but it resolves via Discard, not Done/Fail.
func TestRunUnfilledIsSilent(t *testing.T) {
	srcStore, srcTag := newCatalog(t, map[string]string{"curl": urlPkg("curl", "1.0.0", "aaaa")})
	dstCatalog := memory.New()
	wireCatalog(t, srcStore, srcTag, dstCatalog, "v2")
	wireArchives(t, newArchiveFixtures(), newArchiveFixtures())

	rep := &fakeReporter{}
	summary, err := Run(context.Background(), Options{SrcRef: "example.com/cat:v2", DstRef: "internal.example.com/cat:v2"}, rep)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if summary.Failed != 0 || summary.Copied != 0 {
		t.Fatalf("summary = %+v, want no copied/failed counts", summary)
	}
	// "Pulling catalog" and "Pushing catalog" complete normally; curl's own
	// task exists (eager pickup) but is discarded, not completed.
	task := rep.taskFor("Mirroring curl")
	if task == nil {
		t.Fatal("no task for curl: eager creation on pickup must still register one")
	}
	if done, failed, _ := task.snapshot(); done || failed {
		t.Fatalf("curl task resolved via Done/Fail (done=%v failed=%v), want Discard only", done, failed)
	}
	if !task.wasDiscarded() {
		t.Fatal("curl task was not discarded")
	}
	if len(rep.snapshotWarns()) != 0 {
		t.Fatalf("warns = %v, want none for the ordinary unfilled state", rep.snapshotWarns())
	}
}

// TestRunPrebuiltMissingTagWarnsAndFails proves an oci-artifact (prebuilt)
// package whose version tag is missing from src is the one per-item
// anomaly: it warns, gets a task of its own, and counts as Failed.
func TestRunPrebuiltMissingTagWarnsAndFails(t *testing.T) {
	srcStore, srcTag := newCatalog(t, map[string]string{"kubectl": ociRelativePkg("kubectl", "1.28.0")})
	dstCatalog := memory.New()
	wireCatalog(t, srcStore, srcTag, dstCatalog, "v2")
	wireArchives(t, newArchiveFixtures(), newArchiveFixtures())

	rep := &fakeReporter{}
	summary, err := Run(context.Background(), Options{SrcRef: "example.com/cat:v2", DstRef: "internal.example.com/cat:v2"}, rep)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if summary.Failed != 1 {
		t.Fatalf("summary = %+v, want {Failed:1}", summary)
	}
	task := rep.taskFor("Mirroring kubectl")
	if task == nil {
		t.Fatal("no task for kubectl")
	}
	_, failed, outcome := task.snapshot()
	if !failed || outcome != "Failed kubectl" {
		t.Fatalf("kubectl task failed=%v outcome=%q, want failed \"Failed kubectl\"", failed, outcome)
	}
	warns := rep.snapshotWarns()
	if len(warns) != 1 || warns[0] != "kubectl 1.28.0: archive missing from source" {
		t.Fatalf("warns = %v, want exactly one prebuilt-missing warning", warns)
	}
}

// TestRunAbsoluteOCIRefIsFullyIgnored proves an oci-artifact package whose
// reference is absolute never has its archives touched at all: no open
// call for it on either side, and it contributes to none of the item
// counts (only to the Packages total).
func TestRunAbsoluteOCIRefIsFullyIgnored(t *testing.T) {
	pkgs := map[string]string{
		"vendor-tool": absoluteOCIPkgYAML("vendor-tool", "other-registry.example.com/vendor/tool:{{.Version}}", "2.0.0"),
	}
	srcStore, srcTag := newCatalog(t, pkgs)
	dstCatalog := memory.New()
	wireCatalog(t, srcStore, srcTag, dstCatalog, "v2")
	src := newArchiveFixtures()
	dst := newArchiveFixtures()
	wireArchives(t, src, dst)

	rep := &fakeReporter{}
	summary, err := Run(context.Background(), Options{SrcRef: "example.com/cat:v2", DstRef: "internal.example.com/cat:v2"}, rep)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if summary.Packages != 1 || summary.Copied+summary.Failed != 0 {
		t.Fatalf("summary = %+v, want only Packages:1, everything else 0", summary)
	}
	if len(src.openedNames()) != 0 || len(dst.openedNames()) != 0 {
		t.Fatalf("archives opened for an absolute oci ref: src=%v dst=%v", src.openedNames(), dst.openedNames())
	}
	// vendor-tool's own task exists (eager creation on pickup) but is
	// discarded as soon as classify finds it doesn't participate.
	task := rep.taskFor("Mirroring vendor-tool")
	if task == nil {
		t.Fatal("no task for vendor-tool: eager creation on pickup must still register one")
	}
	if !task.wasDiscarded() {
		t.Fatal("vendor-tool task was not discarded")
	}
	if done, failed, _ := task.snapshot(); done || failed {
		t.Fatalf("vendor-tool task resolved via Done/Fail (done=%v failed=%v), want Discard only", done, failed)
	}
}

// TestRunWarnAndContinueSiblingCompletes proves package isolation end to
// end: one package's dst archives open fails outright, another's is
// healthy, and the run still reports the healthy package Done, the failed
// one Failed, a nonzero Failed count, and a nil (non-abort) error.
func TestRunWarnAndContinueSiblingCompletes(t *testing.T) {
	pkgs := map[string]string{
		"broken":  urlPkg("broken", "1.0.0", "aaaa"),
		"healthy": urlPkg("healthy", "1.0.0", "bbbb"),
	}
	srcStore, srcTag := newCatalog(t, pkgs)
	dstCatalog := memory.New()
	wireCatalog(t, srcStore, srcTag, dstCatalog, "v2")

	src := newArchiveFixtures()
	for name, payload := range map[string][]byte{
		"broken":  []byte("broken-payload"),
		"healthy": []byte("healthy-payload"),
	} {
		s := memory.New()
		ocix.PushFakeArchive(t, s, "1.0.0", map[string][]byte{"linux/amd64": payload})
		src.set(name, s)
	}
	dst := newArchiveFixtures()
	brokenErr := errors.New("dst archives unreachable")
	t.Cleanup(SetDstArchivesOpener(func(_, name string) (oras.Target, error) {
		if name == "broken" {
			return nil, brokenErr
		}
		return dst.open(name), nil
	}))
	t.Cleanup(SetSrcArchivesOpener(func(_, name string) (oras.ReadOnlyTarget, error) {
		return src.open(name), nil
	}))

	rep := &fakeReporter{}
	summary, err := Run(context.Background(), Options{SrcRef: "example.com/cat:v2", DstRef: "internal.example.com/cat:v2"}, rep)
	if err != nil {
		t.Fatalf("Run must not abort on an item failure: %v", err)
	}
	if summary.Failed != 1 || summary.Copied != 1 {
		t.Fatalf("summary = %+v, want {Failed:1 Copied:1}", summary)
	}

	brokenTask := rep.taskFor("Mirroring broken")
	if brokenTask == nil {
		t.Fatal("no task for broken")
	}
	if _, failed, outcome := brokenTask.snapshot(); !failed || outcome != "Failed broken" {
		t.Fatalf("broken task failed=%v outcome=%q", failed, outcome)
	}

	healthyTask := rep.taskFor("Mirroring healthy")
	if healthyTask == nil {
		t.Fatal("no task for healthy")
	}
	if done, failed, outcome := healthyTask.snapshot(); !done || failed || outcome != "Mirrored healthy (1 tag(s))" {
		t.Fatalf("healthy task done=%v failed=%v outcome=%q, want done \"Mirrored healthy (1 tag(s))\"", done, failed, outcome)
	}
}

// TestRunCopyFailureIsWarnedAndCounted forces ocix.CopyTag's own
// retry-then-fail path (a dst archives repo that rejects every push) and
// checks it surfaces the same way as any other item failure.
func TestRunCopyFailureIsWarnedAndCounted(t *testing.T) {
	srcStore, srcTag := newCatalog(t, map[string]string{"go": urlPkg("go", "1.0.0", "aaaa")})
	dstCatalog := memory.New()
	wireCatalog(t, srcStore, srcTag, dstCatalog, "v2")

	src := newArchiveFixtures()
	s := memory.New()
	ocix.PushFakeArchive(t, s, "1.0.0", map[string][]byte{"linux/amd64": []byte("go-payload")})
	src.set("go", s)
	dst := newArchiveFixtures()
	dst.set("go", &rejectPushTarget{Target: memory.New()})
	wireArchives(t, src, dst)

	rep := &fakeReporter{}
	summary, err := Run(context.Background(), Options{SrcRef: "example.com/cat:v2", DstRef: "internal.example.com/cat:v2"}, rep)
	if err != nil {
		t.Fatalf("Run must not abort on a copy failure: %v", err)
	}
	if summary.Failed != 1 {
		t.Fatalf("summary = %+v, want Failed:1", summary)
	}
	warns := rep.snapshotWarns()
	if len(warns) != 1 {
		t.Fatalf("warns = %v, want exactly one", warns)
	}
}

// TestRunMidCopyCancellationIsNotWarnedOrCounted cancels ctx from inside a
// copy already in flight (the dst archives target's first Push call) and
// proves the cancellation is not treated as an ordinary item failure: no
// "context canceled" warn line, no Failed increment. The run still aborts
// via ctx.Err() — package isolation covers real failures, not shutdown.
func TestRunMidCopyCancellationIsNotWarnedOrCounted(t *testing.T) {
	srcStore, srcTag := newCatalog(t, map[string]string{"go": urlPkg("go", "1.0.0", "aaaa")})
	dstCatalog := memory.New()
	wireCatalog(t, srcStore, srcTag, dstCatalog, "v2")

	src := newArchiveFixtures()
	s := memory.New()
	ocix.PushFakeArchive(t, s, "1.0.0", map[string][]byte{"linux/amd64": []byte("go-payload")})
	src.set("go", s)

	ctx, cancel := context.WithCancel(context.Background())
	dst := newArchiveFixtures()
	dst.set("go", &cancelOnPushTarget{Target: memory.New(), cancel: cancel})
	wireArchives(t, src, dst)

	rep := &fakeReporter{}
	summary, err := Run(ctx, Options{SrcRef: "example.com/cat:v2", DstRef: "internal.example.com/cat:v2"}, rep)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}
	if summary.Failed != 0 {
		t.Fatalf("Failed = %d, want 0: a mid-run cancellation is not an item failure", summary.Failed)
	}
	for _, w := range rep.snapshotWarns() {
		if strings.Contains(w, "context canceled") {
			t.Fatalf("warns = %v, cancellation must not be warned as an item failure", rep.snapshotWarns())
		}
	}
}

// TestRunDeterministicSummaryUnderParallel builds a wider catalog mixing
// every outcome and asserts the exact expected Summary — the aggregate
// counts must come out identical regardless of goroutine scheduling.
// Run with -race to exercise the concurrent aggregation itself.
func TestRunDeterministicSummaryUnderParallel(t *testing.T) {
	const n = 12
	pkgs := map[string]string{}
	src := newArchiveFixtures()
	dst := newArchiveFixtures()
	var wantCopied int
	for i := 0; i < n; i++ {
		name := "pkg" + string(rune('a'+i))
		pkgs[name] = urlPkg(name, "1.0.0", "aaaa")
		switch i % 3 {
		case 0: // present
			s := memory.New()
			ocix.PushFakeArchive(t, s, "1.0.0", map[string][]byte{"linux/amd64": []byte(name)})
			src.set(name, s)
			d := memory.New()
			ocix.PushFakeArchive(t, d, "1.0.0", map[string][]byte{"linux/amd64": []byte(name)})
			dst.set(name, d)
		case 1: // absent -> copy
			s := memory.New()
			ocix.PushFakeArchive(t, s, "1.0.0", map[string][]byte{"linux/amd64": []byte(name)})
			src.set(name, s)
			wantCopied++
		case 2: // unfilled
		}
	}
	srcStore, srcTag := newCatalog(t, pkgs)
	dstCatalog := memory.New()
	wireCatalog(t, srcStore, srcTag, dstCatalog, "v2")
	wireArchives(t, src, dst)

	rep := &fakeReporter{}
	summary, err := Run(context.Background(), Options{SrcRef: "example.com/cat:v2", DstRef: "internal.example.com/cat:v2"}, rep)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := Summary{Packages: n, Copied: wantCopied}
	if summary != want {
		t.Fatalf("summary = %+v, want %+v", summary, want)
	}
}

// TestSummaryString pins the exact one-line format the contract example
// shows.
func TestSummaryString(t *testing.T) {
	s := Summary{Packages: 600, Copied: 102, Failed: 1}
	want := "Mirrored 600 packages, 102 tag(s), 1 tag(s) failed"
	if got := s.String(); got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
	s = Summary{Packages: 600, Copied: 102}
	want = "Mirrored 600 packages, 102 tag(s)"
	if got := s.String(); got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

// TestRunRejectsUntaggedRefs proves both refs are validated up front, so a
// bare repository ref fails fast instead of resolving to whatever a
// registry currently calls "latest".
func TestRunRejectsUntaggedRefs(t *testing.T) {
	if _, err := Run(context.Background(), Options{SrcRef: "example.com/cat", DstRef: "internal.example.com/cat:v2"}, &fakeReporter{}); err == nil {
		t.Fatal("untagged src ref must be rejected")
	}
	if _, err := Run(context.Background(), Options{SrcRef: "example.com/cat:v2", DstRef: "internal.example.com/cat"}, &fakeReporter{}); err == nil {
		t.Fatal("untagged dst ref must be rejected")
	}
}

// TestRunCtxCancelledBeforeStartAborts proves an already-cancelled ctx
// aborts the whole run: staging never runs, Run returns ctx.Err(), and no
// package task is created.
func TestRunCtxCancelledBeforeStartAborts(t *testing.T) {
	srcStore, srcTag := newCatalog(t, map[string]string{"go": urlPkg("go", "1.0.0", "aaaa")})
	dstCatalog := memory.New()
	dstOpened := wireCatalog(t, srcStore, srcTag, dstCatalog, "v2")
	wireArchives(t, newArchiveFixtures(), newArchiveFixtures())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	rep := &fakeReporter{}
	_, err := Run(ctx, Options{SrcRef: "example.com/cat:v2", DstRef: "internal.example.com/cat:v2"}, rep)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}
	if *dstOpened {
		t.Fatal("cancelled-before-start run must not open the dst catalog")
	}
}

// wantStableProgress checks counts against wantTotal: a stable total once
// known, and a final call reaching done=total=wantTotal. Count alone
// never renders (report.TestLiveCountAloneRendersNothingUntilStatusIsSet),
// so callers also check a Status call happened before calling this.
func wantStableProgress(t *testing.T, taskName string, counts []countCall, wantTotal int) {
	t.Helper()
	if len(counts) == 0 {
		t.Fatalf("no Count calls recorded on the %s task", taskName)
	}
	last := counts[len(counts)-1]
	if last.done != wantTotal || last.total != wantTotal {
		t.Fatalf("%s: final count = %+v, want done=total=%d", taskName, last, wantTotal)
	}
	for _, c := range counts {
		if c.total != 0 && c.total != wantTotal {
			t.Fatalf("%s: count %+v: total changed mid-run, want a stable %d once known", taskName, c, wantTotal)
		}
	}
}

// TestPullCatalogReportsProgressOnPullingTask proves the "Pulling catalog"
// task receives real x/y progress as staging copies the index closure
// from src — total known as the index's own manifest count plus one, done
// reaching it exactly once staging completes.
func TestPullCatalogReportsProgressOnPullingTask(t *testing.T) {
	const n = 5
	pkgs := map[string]string{}
	for i := 0; i < n; i++ {
		name := "pkg" + string(rune('a'+i))
		pkgs[name] = urlPkg(name, "1.0.0", "deadbeef")
	}
	srcStore, srcTag := newCatalog(t, pkgs)
	wireCatalog(t, srcStore, srcTag, memory.New(), "v2")
	wireArchives(t, newArchiveFixtures(), newArchiveFixtures())

	rep := &fakeReporter{}
	opts := Options{SrcRef: "example.com/cat:v2", DstRef: "internal.example.com/cat:v2", DryRun: true}
	if _, err := Run(context.Background(), opts, rep); err != nil {
		t.Fatalf("Run: %v", err)
	}

	task := rep.taskFor("Pulling catalog")
	if task == nil {
		t.Fatal("no Pulling catalog task")
	}
	if len(task.snapshotStatuses()) == 0 {
		t.Fatal("no Status call recorded on the pull task: Count alone never renders (segment stays \"\")")
	}
	wantStableProgress(t, "Pulling catalog", task.snapshotCounts(), n+1)

	if rep.taskFor("Pushing catalog") != nil {
		t.Fatal("dry run must never create a Pushing catalog task")
	}
}

// TestPushCatalogReportsProgressOnPushingTask proves the "Pushing catalog"
// task — mirror's own, fill has no equivalent — receives the same real
// x/y progress publishing the staged closure to dst: the mechanism
// applies symmetrically to CopyIndexClosureWithProgress regardless of
// whether src is the remote catalog (pull) or the local staged store
// (push).
func TestPushCatalogReportsProgressOnPushingTask(t *testing.T) {
	const n = 5
	pkgs := map[string]string{}
	for i := 0; i < n; i++ {
		name := "pkg" + string(rune('a'+i))
		pkgs[name] = urlPkg(name, "1.0.0", "deadbeef")
	}
	srcStore, srcTag := newCatalog(t, pkgs)
	wireCatalog(t, srcStore, srcTag, memory.New(), "v2")
	wireArchives(t, newArchiveFixtures(), newArchiveFixtures())

	rep := &fakeReporter{}
	opts := Options{SrcRef: "example.com/cat:v2", DstRef: "internal.example.com/cat:v2"}
	if _, err := Run(context.Background(), opts, rep); err != nil {
		t.Fatalf("Run: %v", err)
	}

	task := rep.taskFor("Pushing catalog")
	if task == nil {
		t.Fatal("no Pushing catalog task")
	}
	if len(task.snapshotStatuses()) == 0 {
		t.Fatal("no Status call recorded on the push task: Count alone never renders (segment stays \"\")")
	}
	wantStableProgress(t, "Pushing catalog", task.snapshotCounts(), n+1)

	if done, failed, outcome := task.snapshot(); !done || failed || outcome != "Pushed catalog" {
		t.Fatalf("push task done=%v failed=%v outcome=%q, want done \"Pushed catalog\"", done, failed, outcome)
	}
}
