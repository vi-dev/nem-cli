package fill

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/memory"

	"github.com/vi-dev/nem-cli/internal/ocix"
	"github.com/vi-dev/nem-cli/internal/publish"
	"github.com/vi-dev/nem-cli/internal/spec"
)

// TestRunStagesCatalogAndReadsSrcIndexOnce proves package enumeration and
// pkg.yaml reads never touch the catalog again once staging has returned,
// mirroring mirror's own proof: staging itself legitimately reads it once
// per package (copying the closure means fetching every manifest and
// pkg.yaml layer), so the invariant is "exactly staging's own cost."
func TestRunStagesCatalogAndReadsSrcIndexOnce(t *testing.T) {
	up := newUpstream(t)
	wireUpstream(t, up)

	pkgs := map[string]string{}
	for i := 0; i < 8; i++ {
		name := "pkg" + string(rune('a'+i))
		pkgs[name] = urlPkg(name, "1.0.0", up.URL+"/"+name+"/{{.Version}}", "deadbeef")
	}
	store, tag := newCatalog(t, pkgs)

	stagingOnly := &countingTarget{ReadOnlyTarget: store}
	wireCatalog(t, stagingOnly, tag)
	if _, err := stageCatalog(context.Background(), "example.com/cat:v2", &fakeReporter{}); err != nil {
		t.Fatalf("stageCatalog: %v", err)
	}
	wantResolves, wantFetches := stagingOnly.resolves.Load(), stagingOnly.fetches.Load()
	if wantFetches == 0 {
		t.Fatal("staging must touch the catalog at all")
	}
	// The index ref is resolved exactly once: a single resolve+copy of the
	// closure, with no separate pre-validation resolve beforehand. A
	// reintroduced double-fetch of the index (e.g. a pre-copy FetchIndex)
	// would push this to 2 — unlike wantFetches below, this bound isn't
	// self-referential, so it actually catches that regression.
	if wantResolves != 1 {
		t.Fatalf("catalog resolves during staging = %d, want exactly 1 (the index ref, resolved once)", wantResolves)
	}

	counted := &countingTarget{ReadOnlyTarget: store}
	wireCatalog(t, counted, tag)
	wireArchives(t, newArchiveFixtures())

	rep := &fakeReporter{}
	summary, err := Run(context.Background(), newHome(t), Options{CatalogRef: "example.com/cat:v2"}, rep)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if summary.Packages != 8 {
		t.Fatalf("Packages = %d, want 8", summary.Packages)
	}
	if got := counted.resolves.Load(); got != wantResolves {
		t.Fatalf("catalog resolves = %d, want exactly staging's own cost %d", got, wantResolves)
	}
	if got := counted.fetches.Load(); got != wantFetches {
		t.Fatalf("catalog fetches = %d, want exactly staging's own cost %d", got, wantFetches)
	}
}

// TestRunFillsMissingArchive proves a package with no archive at all gets
// every supported platform filled: downloaded from upstream, verified, and
// published.
func TestRunFillsMissingArchive(t *testing.T) {
	up := newUpstream(t)
	wireUpstream(t, up)
	payload := []byte("go-payload")
	up.set("/go/1.0.0", payload)

	store, tag := newCatalog(t, map[string]string{
		"go": urlPkg("go", "1.0.0", up.URL+"/go/{{.Version}}", sha256Hex(payload)),
	})
	wireCatalog(t, store, tag)
	archives := newArchiveFixtures()
	wireArchives(t, archives)

	rep := &fakeReporter{}
	summary, err := Run(context.Background(), newHome(t), Options{CatalogRef: "example.com/cat:v2"}, rep)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if summary.Filled != len(spec.SupportedPlatforms) || summary.Healed != 0 || summary.Present != 0 || summary.Failed != 0 || summary.NotFillable != 0 {
		t.Fatalf("summary = %+v, want Filled:%d only", summary, len(spec.SupportedPlatforms))
	}

	task := rep.taskFor("Filling go")
	if task == nil {
		t.Fatal("no task for go")
	}
	wantOutcome := fmt.Sprintf("Filled go (%d fill(s), 0 heal(s))", len(spec.SupportedPlatforms))
	if done, failed, outcome := task.snapshot(); !done || failed || outcome != wantOutcome {
		t.Fatalf("go task done=%v failed=%v outcome=%q, want done %q", done, failed, outcome, wantOutcome)
	}

	for _, plat := range spec.SupportedPlatforms {
		got, err := ocix.ArchiveLayerDigest(context.Background(), archives.open("go"), "1.0.0", plat)
		if err != nil {
			t.Fatalf("resolve published archive for %s: %v", plat, err)
		}
		if got.Encoded() != sha256Hex(payload) {
			t.Fatalf("%s digest = %s, want %s", plat, got.Encoded(), sha256Hex(payload))
		}
	}
}

// TestRunPresentSkipsDownload proves an archive already matching the
// pinned checksum, for every platform, needs no download at all.
func TestRunPresentSkipsDownload(t *testing.T) {
	up := newUpstream(t) // never asked to serve anything
	wireUpstream(t, up)
	payload := []byte("present-payload")

	store, tag := newCatalog(t, map[string]string{
		"go": urlPkg("go", "1.0.0", up.URL+"/go/{{.Version}}", sha256Hex(payload)),
	})
	wireCatalog(t, store, tag)

	archives := newArchiveFixtures()
	s := memory.New()
	platMap := map[string][]byte{}
	for _, plat := range spec.SupportedPlatforms {
		platMap[plat.String()] = payload
	}
	ocix.PushFakeArchive(t, s, "1.0.0", platMap)
	archives.set("go", s)
	wireArchives(t, archives)

	rep := &fakeReporter{}
	summary, err := Run(context.Background(), newHome(t), Options{CatalogRef: "example.com/cat:v2"}, rep)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := Summary{Packages: 1, Present: len(spec.SupportedPlatforms)}
	if summary != want {
		t.Fatalf("summary = %+v, want %+v", summary, want)
	}
	if got := up.hits.Load(); got != 0 {
		t.Fatalf("upstream hits = %d, want 0 (nothing missing)", got)
	}
	// "Pulling catalog" completes normally; go's own task exists (eager
	// creation on pickup) but is discarded, since every item is present.
	task := rep.taskFor("Filling go")
	if task == nil {
		t.Fatal("no task for go: eager creation on pickup must still register one")
	}
	if !task.wasDiscarded() {
		t.Fatal("go task was not discarded")
	}
	if done, failed, _ := task.snapshot(); done || failed {
		t.Fatalf("go task resolved via Done/Fail (done=%v failed=%v), want Discard only", done, failed)
	}
}

// TestRunHealsStaleArchive proves an archive present for one platform but
// no longer matching the pinned checksum is healed (re-downloaded,
// re-published), while a platform with no archive at all is filled, not
// healed.
func TestRunHealsStaleArchive(t *testing.T) {
	up := newUpstream(t)
	wireUpstream(t, up)
	newPayload := []byte("new-payload")
	up.set("/go/1.0.0", newPayload)

	store, tag := newCatalog(t, map[string]string{
		"go": urlPkg("go", "1.0.0", up.URL+"/go/{{.Version}}", sha256Hex(newPayload)),
	})
	wireCatalog(t, store, tag)

	archives := newArchiveFixtures()
	s := memory.New()
	stalePlat := spec.SupportedPlatforms[0]
	ocix.PushFakeArchive(t, s, "1.0.0", map[string][]byte{stalePlat.String(): []byte("old-payload")})
	archives.set("go", s)
	wireArchives(t, archives)

	rep := &fakeReporter{}
	summary, err := Run(context.Background(), newHome(t), Options{CatalogRef: "example.com/cat:v2"}, rep)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if summary.Healed != 1 || summary.Filled != len(spec.SupportedPlatforms)-1 || summary.Failed != 0 {
		t.Fatalf("summary = %+v, want Healed:1 Filled:%d", summary, len(spec.SupportedPlatforms)-1)
	}

	for _, plat := range spec.SupportedPlatforms {
		got, err := ocix.ArchiveLayerDigest(context.Background(), s, "1.0.0", plat)
		if err != nil {
			t.Fatalf("resolve %s: %v", plat, err)
		}
		if got.Encoded() != sha256Hex(newPayload) {
			t.Fatalf("%s digest = %s, want healed %s", plat, got.Encoded(), sha256Hex(newPayload))
		}
	}
}

// TestRunFillsMultiPlatformWithOneIndexCommit is the direct regression
// proof for the bug this fix corrects: fill used to read-merge-push-retag
// the archive index once per (version, platform), so a 4-platform version
// left 3 orphaned, untagged intermediate indexes behind in the registry.
// Filling every supported platform for one version now pushes and tags
// the index exactly once, regardless of platform count.
func TestRunFillsMultiPlatformWithOneIndexCommit(t *testing.T) {
	up := newUpstream(t)
	wireUpstream(t, up)
	payload := []byte("go-payload")
	up.set("/go/1.0.0", payload)

	store, tag := newCatalog(t, map[string]string{
		"go": urlPkg("go", "1.0.0", up.URL+"/go/{{.Version}}", sha256Hex(payload)),
	})
	wireCatalog(t, store, tag)

	counted := &indexPushCountingTarget{Target: memory.New()}
	archives := newArchiveFixtures()
	archives.set("go", counted)
	wireArchives(t, archives)

	rep := &fakeReporter{}
	summary, err := Run(context.Background(), newHome(t), Options{CatalogRef: "example.com/cat:v2"}, rep)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if summary.Filled != len(spec.SupportedPlatforms) || summary.Failed != 0 {
		t.Fatalf("summary = %+v, want Filled:%d Failed:0", summary, len(spec.SupportedPlatforms))
	}
	if got := counted.indexPushes.Load(); got != 1 {
		t.Fatalf("index pushes = %d, want exactly 1 for a %d-platform fill", got, len(spec.SupportedPlatforms))
	}
	if got := counted.tags.Load(); got != 1 {
		t.Fatalf("tag calls = %d, want exactly 1 for a %d-platform fill", got, len(spec.SupportedPlatforms))
	}

	for _, plat := range spec.SupportedPlatforms {
		got, err := ocix.ArchiveLayerDigest(context.Background(), counted, "1.0.0", plat)
		if err != nil {
			t.Fatalf("resolve published archive for %s: %v", plat, err)
		}
		if got.Encoded() != sha256Hex(payload) {
			t.Fatalf("%s digest = %s, want %s", plat, got.Encoded(), sha256Hex(payload))
		}
	}
}

// TestRunPartialBatchCommitsSuccessfulPlatforms proves the batch's
// failure semantics: when one platform's own publish fails inside an
// otherwise-successful multi-platform fill, the failed platform is
// warned and counted failed exactly as any other item failure always
// was, but the batch still commits every platform that DID succeed —
// partial progress heals on re-run rather than being silently discarded
// — and that still lands as one index push, not one per surviving
// platform.
func TestRunPartialBatchCommitsSuccessfulPlatforms(t *testing.T) {
	up := newUpstream(t)
	wireUpstream(t, up)

	failing := spec.SupportedPlatforms[0]
	sha := map[string]string{}
	for _, p := range spec.SupportedPlatforms {
		if p == failing {
			sha[p.String()] = "deadbeef" // never served upstream: 404
			continue
		}
		payload := []byte("payload-" + p.String())
		up.set("/go/"+p.String()+"/1.0.0", payload)
		sha[p.String()] = sha256Hex(payload)
	}

	store, tag := newCatalog(t, map[string]string{
		"go": string(publish.URLPkgYAML("go", "1.0.0", up.URL+"/go/{{.OS}}/{{.Arch}}/{{.Version}}", sha)),
	})
	wireCatalog(t, store, tag)

	counted := &indexPushCountingTarget{Target: memory.New()}
	archives := newArchiveFixtures()
	archives.set("go", counted)
	wireArchives(t, archives)

	rep := &fakeReporter{}
	summary, err := Run(context.Background(), newHome(t), Options{CatalogRef: "example.com/cat:v2"}, rep)
	if err != nil {
		t.Fatalf("Run must not abort on an item failure: %v", err)
	}
	wantFilled := len(spec.SupportedPlatforms) - 1
	if summary.Filled != wantFilled || summary.Failed != 1 {
		t.Fatalf("summary = %+v, want Filled:%d Failed:1", summary, wantFilled)
	}
	if got := counted.indexPushes.Load(); got != 1 {
		t.Fatalf("index pushes = %d, want exactly 1 for the batch's %d successful platforms", got, wantFilled)
	}

	task := rep.taskFor("Filling go")
	if task == nil {
		t.Fatal("no task for go")
	}
	if _, failed, outcome := task.snapshot(); !failed || outcome != "Failed go" {
		t.Fatalf("go task failed=%v outcome=%q, want failed \"Failed go\"", failed, outcome)
	}

	plats, err := ocix.ArchivePlatforms(context.Background(), counted, "1.0.0")
	if err != nil {
		t.Fatalf("ArchivePlatforms: %v", err)
	}
	if len(plats) != wantFilled {
		t.Fatalf("committed platforms = %v, want %d (every platform but the failing one)", plats, wantFilled)
	}
	for _, p := range plats {
		if p == failing {
			t.Fatalf("failing platform %s was committed despite its own publish failing", failing)
		}
	}
}

// rejectIndexTagTarget wraps an oras.Target whose Tag call always fails —
// simulating the batch commit itself failing after every platform's own
// publish already succeeded.
type rejectIndexTagTarget struct {
	oras.Target
}

func (r *rejectIndexTagTarget) Tag(context.Context, ocispec.Descriptor, string) error {
	return errors.New("tag rejected")
}

// TestRunBatchCommitFailureCountsAsFailedNotFilled proves the batch
// commit's own failure is not silently absorbed: every platform that was
// collected into it — its content pushed, but never landed in the tag's
// index — counts failed, not filled/healed. Counting it a success would
// be a lie the exit code and summary couldn't recover from; failed means
// a re-run heals it the same way an ordinary missing platform would.
func TestRunBatchCommitFailureCountsAsFailedNotFilled(t *testing.T) {
	up := newUpstream(t)
	wireUpstream(t, up)
	payload := []byte("go-payload")
	up.set("/go/1.0.0", payload)

	store, tag := newCatalog(t, map[string]string{
		"go": urlPkg("go", "1.0.0", up.URL+"/go/{{.Version}}", sha256Hex(payload)),
	})
	wireCatalog(t, store, tag)
	t.Cleanup(SetArchivesOpener(func(_, _ string) (oras.Target, error) {
		return &rejectIndexTagTarget{Target: memory.New()}, nil
	}))

	rep := &fakeReporter{}
	summary, err := Run(context.Background(), newHome(t), Options{CatalogRef: "example.com/cat:v2"}, rep)
	if err != nil {
		t.Fatalf("Run must not abort on a commit failure: %v", err)
	}
	if summary.Filled != 0 || summary.Healed != 0 {
		t.Fatalf("summary = %+v, want Filled:0 Healed:0 — a failed commit must never count a success", summary)
	}
	if summary.Failed != len(spec.SupportedPlatforms) {
		t.Fatalf("summary.Failed = %d, want %d (every platform in the failed batch)", summary.Failed, len(spec.SupportedPlatforms))
	}
	if len(rep.snapshotWarns()) == 0 {
		t.Fatal("want at least one warn for the commit failure")
	}

	task := rep.taskFor("Filling go")
	if task == nil {
		t.Fatal("no task for go")
	}
	if _, failed, outcome := task.snapshot(); !failed || outcome != "Failed go" {
		t.Fatalf("go task failed=%v outcome=%q, want failed \"Failed go\"", failed, outcome)
	}
}

// TestRunNotFillablePackageIsSilentAggregate proves a prebuilt/source-built
// package counts once toward NotFillable and produces no Info/Warn line
// and no completion line for itself (its eagerly-created task is
// discarded), and that fill never even opens its archives.
func TestRunNotFillablePackageIsSilentAggregate(t *testing.T) {
	store, tag := newCatalog(t, map[string]string{"kubectl": ociPkg("kubectl", "1.28.0")})
	wireCatalog(t, store, tag)
	archives := newArchiveFixtures()
	wireArchives(t, archives)

	rep := &fakeReporter{}
	summary, err := Run(context.Background(), newHome(t), Options{CatalogRef: "example.com/cat:v2"}, rep)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if summary.NotFillable != 1 || summary.Failed != 0 || summary.Filled != 0 {
		t.Fatalf("summary = %+v, want NotFillable:1", summary)
	}
	task := rep.taskFor("Filling kubectl")
	if task == nil {
		t.Fatal("no task for kubectl: eager creation on pickup must still register one")
	}
	if !task.wasDiscarded() {
		t.Fatal("kubectl task was not discarded")
	}
	if done, failed, _ := task.snapshot(); done || failed {
		t.Fatalf("kubectl task resolved via Done/Fail (done=%v failed=%v), want Discard only", done, failed)
	}
	if len(rep.snapshotWarns()) != 0 || len(rep.snapshotInfos()) != 0 {
		t.Fatalf("narration = warns:%v infos:%v, want none", rep.snapshotWarns(), rep.snapshotInfos())
	}
	if len(archives.openedNames()) != 0 {
		t.Fatalf("archives opened for a not-fillable package: %v", archives.openedNames())
	}
}

// TestRunUpstream404WarnsAndFailsNotAborts proves an upstream 404/410 is a
// per-item warning that fails the item (and hence the run's exit code) but
// never aborts the run.
func TestRunUpstream404WarnsAndFailsNotAborts(t *testing.T) {
	up := newUpstream(t) // "/go/1.0.0" never set: every request 404s
	wireUpstream(t, up)

	store, tag := newCatalog(t, map[string]string{
		"go": urlPkg("go", "1.0.0", up.URL+"/go/{{.Version}}", "deadbeef"),
	})
	wireCatalog(t, store, tag)
	wireArchives(t, newArchiveFixtures())

	rep := &fakeReporter{}
	summary, err := Run(context.Background(), newHome(t), Options{CatalogRef: "example.com/cat:v2"}, rep)
	if err != nil {
		t.Fatalf("Run must not abort on a 404: %v", err)
	}
	if summary.Failed == 0 {
		t.Fatalf("summary = %+v, want Failed > 0", summary)
	}
	task := rep.taskFor("Filling go")
	if task == nil {
		t.Fatal("no task for go")
	}
	if _, failed, outcome := task.snapshot(); !failed || outcome != "Failed go" {
		t.Fatalf("go task failed=%v outcome=%q, want failed \"Failed go\"", failed, outcome)
	}
	warns := rep.snapshotWarns()
	if len(warns) == 0 {
		t.Fatal("want at least one warn for the 404")
	}
	for _, w := range warns {
		if !strings.Contains(w, "not found") {
			t.Fatalf("warn %q does not mention the artifact being gone", w)
		}
	}
}

// TestRunWarnAndContinueSiblingCompletes proves package isolation end to
// end: one package's archives open fails outright, another's fills
// cleanly, and the run still reports the healthy package Done, the broken
// one Failed, a nonzero Failed count, and a nil (non-abort) error.
func TestRunWarnAndContinueSiblingCompletes(t *testing.T) {
	up := newUpstream(t)
	wireUpstream(t, up)
	payload := []byte("healthy-payload")
	up.set("/healthy/1.0.0", payload)

	store, tag := newCatalog(t, map[string]string{
		"broken":  urlPkg("broken", "1.0.0", up.URL+"/broken/{{.Version}}", "deadbeef"),
		"healthy": urlPkg("healthy", "1.0.0", up.URL+"/healthy/{{.Version}}", sha256Hex(payload)),
	})
	wireCatalog(t, store, tag)

	healthyArchives := memory.New()
	brokenErr := errors.New("archives unreachable")
	t.Cleanup(SetArchivesOpener(func(_, name string) (oras.Target, error) {
		if name == "broken" {
			return nil, brokenErr
		}
		return healthyArchives, nil
	}))

	rep := &fakeReporter{}
	summary, err := Run(context.Background(), newHome(t), Options{CatalogRef: "example.com/cat:v2"}, rep)
	if err != nil {
		t.Fatalf("Run must not abort on an item failure: %v", err)
	}
	if summary.Failed != 1 || summary.Filled != len(spec.SupportedPlatforms) {
		t.Fatalf("summary = %+v, want Failed:1 Filled:%d", summary, len(spec.SupportedPlatforms))
	}

	brokenTask := rep.taskFor("Filling broken")
	if brokenTask == nil {
		t.Fatal("no task for broken")
	}
	if _, failed, outcome := brokenTask.snapshot(); !failed || outcome != "Failed broken" {
		t.Fatalf("broken task failed=%v outcome=%q", failed, outcome)
	}

	healthyTask := rep.taskFor("Filling healthy")
	if healthyTask == nil {
		t.Fatal("no task for healthy")
	}
	wantHealthyOutcome := fmt.Sprintf("Filled healthy (%d fill(s), 0 heal(s))", len(spec.SupportedPlatforms))
	if done, failed, outcome := healthyTask.snapshot(); !done || failed || outcome != wantHealthyOutcome {
		t.Fatalf("healthy task done=%v failed=%v outcome=%q, want done %q", done, failed, outcome, wantHealthyOutcome)
	}
}

// rejectPushTarget wraps an oras.Target whose Push always fails, so a test
// can force ocix.PublishArchiveLayerFile's own retry-then-fail path
// without a real network.
type rejectPushTarget struct {
	oras.Target
}

func (r *rejectPushTarget) Push(context.Context, ocispec.Descriptor, io.Reader) error {
	return fmt.Errorf("push rejected")
}

// TestRunPublishFailureIsWarnedAndCounted forces
// ocix.PublishArchiveLayerFile's own retry-then-fail path (an archives
// repo that rejects every push) and checks it surfaces the same way as
// any other item failure.
func TestRunPublishFailureIsWarnedAndCounted(t *testing.T) {
	up := newUpstream(t)
	wireUpstream(t, up)
	payload := []byte("go-payload")
	up.set("/go/1.0.0", payload)

	store, tag := newCatalog(t, map[string]string{
		"go": urlPkg("go", "1.0.0", up.URL+"/go/{{.Version}}", sha256Hex(payload)),
	})
	wireCatalog(t, store, tag)
	t.Cleanup(SetArchivesOpener(func(_, _ string) (oras.Target, error) {
		return &rejectPushTarget{Target: memory.New()}, nil
	}))

	rep := &fakeReporter{}
	summary, err := Run(context.Background(), newHome(t), Options{CatalogRef: "example.com/cat:v2"}, rep)
	if err != nil {
		t.Fatalf("Run must not abort on a publish failure: %v", err)
	}
	if summary.Failed == 0 {
		t.Fatalf("summary = %+v, want Failed > 0", summary)
	}
	if len(rep.snapshotWarns()) == 0 {
		t.Fatal("want at least one warn")
	}
}

// cancelOnPushTarget wraps an oras.Target whose first Push call cancels
// ctx — simulating an interrupt landing mid-publish — and returns
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

// TestRunMidPublishCancellationIsNotWarnedOrCounted cancels ctx from
// inside a publish already in flight and proves the cancellation is not
// treated as an ordinary item failure: no "context canceled" warn line, no
// Failed increment. The run still aborts via ctx.Err().
func TestRunMidPublishCancellationIsNotWarnedOrCounted(t *testing.T) {
	up := newUpstream(t)
	wireUpstream(t, up)
	payload := []byte("go-payload")
	up.set("/go/1.0.0", payload)

	store, tag := newCatalog(t, map[string]string{
		"go": urlPkg("go", "1.0.0", up.URL+"/go/{{.Version}}", sha256Hex(payload)),
	})
	wireCatalog(t, store, tag)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(SetArchivesOpener(func(_, _ string) (oras.Target, error) {
		return &cancelOnPushTarget{Target: memory.New(), cancel: cancel}, nil
	}))

	rep := &fakeReporter{}
	summary, err := Run(ctx, newHome(t), Options{CatalogRef: "example.com/cat:v2"}, rep)
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

// TestRunDryRunPlansEverythingNoIO proves --dry-run reports the full plan
// (fill/heal/present/not-fillable all represented) while touching neither
// the upstream host nor any archives target's Push.
func TestRunDryRunPlansEverythingNoIO(t *testing.T) {
	up := newUpstream(t)
	wireUpstream(t, up)
	missingPayload := []byte("missing-payload")
	stalePayload := []byte("new-stale-payload")
	up.set("/missing/1.0.0", missingPayload)
	up.set("/stale/1.0.0", stalePayload)

	presentPayload := []byte("present-payload")
	store, tag := newCatalog(t, map[string]string{
		"missing":  urlPkg("missing", "1.0.0", up.URL+"/missing/{{.Version}}", sha256Hex(missingPayload)),
		"stale":    urlPkg("stale", "1.0.0", up.URL+"/stale/{{.Version}}", sha256Hex(stalePayload)),
		"present":  urlPkg("present", "1.0.0", up.URL+"/present/{{.Version}}", sha256Hex(presentPayload)),
		"prebuilt": ociPkg("prebuilt", "1.0.0"),
	})
	wireCatalog(t, store, tag)

	archives := newArchiveFixtures()
	presentStore := memory.New()
	platMap := map[string][]byte{}
	for _, plat := range spec.SupportedPlatforms {
		platMap[plat.String()] = presentPayload
	}
	ocix.PushFakeArchive(t, presentStore, "1.0.0", platMap)
	archives.set("present", presentStore)
	staleStore := memory.New()
	ocix.PushFakeArchive(t, staleStore, "1.0.0", map[string][]byte{spec.SupportedPlatforms[0].String(): []byte("old")})
	archives.set("stale", &noPushTarget{Target: staleStore, t: t})
	archives.set("missing", &noPushTarget{Target: memory.New(), t: t})
	wireArchives(t, archives)

	rep := &fakeReporter{}
	summary, err := Run(context.Background(), newHome(t), Options{CatalogRef: "example.com/cat:v2", DryRun: true}, rep)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := Summary{
		Packages:    4,
		Filled:      len(spec.SupportedPlatforms) + (len(spec.SupportedPlatforms) - 1), // missing: every platform; stale: its non-stale platforms
		Healed:      1,                                                                 // stale package: the one stale platform
		Present:     len(spec.SupportedPlatforms),                                      // present package
		NotFillable: 1,
		DryRun:      true,
	}
	if summary != want {
		t.Fatalf("summary = %+v, want %+v", summary, want)
	}
	if got := up.hits.Load(); got != 0 {
		t.Fatalf("upstream hits = %d, want 0", got)
	}

	if len(rep.snapshotInfos()) != 0 {
		t.Fatalf("infos = %v, want none: per-item progress must not print", rep.snapshotInfos())
	}

	missingTask := rep.taskFor("Filling missing")
	if missingTask == nil {
		t.Fatal("no task for missing")
	}
	staleTask := rep.taskFor("Filling stale")
	if staleTask == nil {
		t.Fatal("no task for stale")
	}

	var sawWouldFill, sawWouldHeal bool
	for _, s := range missingTask.snapshotStatuses() {
		if strings.HasPrefix(s, "would fill 1.0.0 ") {
			sawWouldFill = true
		}
	}
	for _, s := range staleTask.snapshotStatuses() {
		if strings.HasPrefix(s, "would heal 1.0.0 ") {
			sawWouldHeal = true
		}
	}
	if !sawWouldFill || !sawWouldHeal {
		t.Fatalf("missing statuses = %v, stale statuses = %v, want would-fill/would-heal status transitions",
			missingTask.snapshotStatuses(), staleTask.snapshotStatuses())
	}

	if _, err := archives.open("missing").(oras.ReadOnlyTarget).Resolve(context.Background(), "1.0.0"); err == nil {
		t.Fatal("dry run must not write archives")
	}
}

// TestRunDryRunNeverCreatesTmpDir proves --dry-run skips even Run's own
// MkdirAll for $NEM_HOME/tmp: a real run needs it staged before any
// download, but a dry run downloads nothing.
func TestRunDryRunNeverCreatesTmpDir(t *testing.T) {
	store, tag := newCatalog(t, map[string]string{
		"go": urlPkg("go", "1.0.0", "https://example.com/go/{{.Version}}", "deadbeef"),
	})
	wireCatalog(t, store, tag)
	wireArchives(t, newArchiveFixtures())

	h := bareHome(t)
	if _, err := Run(context.Background(), h, Options{CatalogRef: "example.com/cat:v2", DryRun: true}, &fakeReporter{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := os.Stat(h.Tmp()); !os.IsNotExist(err) {
		t.Fatalf("dry run must not create %s", h.Tmp())
	}
}

// noPushTarget wraps an oras.Target and fails the test if Push is ever
// called — dry run must never write.
type noPushTarget struct {
	oras.Target
	t *testing.T
}

func (n *noPushTarget) Push(context.Context, ocispec.Descriptor, io.Reader) error {
	n.t.Helper()
	n.t.Fatal("dry run must never push an archive layer")
	return nil
}

// TestRunPkgScopingFiltersToNamed proves --pkg limits processing to the
// named packages: the unscoped package's archives are never opened.
func TestRunPkgScopingFiltersToNamed(t *testing.T) {
	up := newUpstream(t)
	wireUpstream(t, up)
	payload := []byte("go-payload")
	up.set("/go/1.0.0", payload)

	store, tag := newCatalog(t, map[string]string{
		"go":   urlPkg("go", "1.0.0", up.URL+"/go/{{.Version}}", sha256Hex(payload)),
		"curl": urlPkg("curl", "1.0.0", up.URL+"/curl/{{.Version}}", "deadbeef"),
	})
	wireCatalog(t, store, tag)
	archives := newArchiveFixtures()
	wireArchives(t, archives)

	rep := &fakeReporter{}
	summary, err := Run(context.Background(), newHome(t), Options{CatalogRef: "example.com/cat:v2", Pkgs: []string{"go"}}, rep)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if summary.Packages != 1 || summary.Filled != len(spec.SupportedPlatforms) {
		t.Fatalf("summary = %+v, want Packages:1 Filled:%d", summary, len(spec.SupportedPlatforms))
	}
	for _, name := range archives.openedNames() {
		if name == "curl" {
			t.Fatal("curl's archives were opened despite being out of --pkg scope")
		}
	}
}

// TestRunPkgScopingUnknownNameErrors proves an unknown --pkg name aborts
// before any package is processed, listing every unknown name sorted.
func TestRunPkgScopingUnknownNameErrors(t *testing.T) {
	store, tag := newCatalog(t, map[string]string{
		"go": urlPkg("go", "1.0.0", "https://example.com/go/{{.Version}}", "deadbeef"),
	})
	wireCatalog(t, store, tag)
	archives := newArchiveFixtures()
	wireArchives(t, archives)

	rep := &fakeReporter{}
	_, err := Run(context.Background(), newHome(t), Options{CatalogRef: "example.com/cat:v2", Pkgs: []string{"zzz", "go", "aaa"}}, rep)
	if err == nil {
		t.Fatal("unknown --pkg name must error")
	}
	if !strings.Contains(err.Error(), "aaa") || !strings.Contains(err.Error(), "zzz") {
		t.Fatalf("error = %v, want it to list both unknown names", err)
	}
	if strings.Index(err.Error(), "aaa") > strings.Index(err.Error(), "zzz") {
		t.Fatalf("error = %v, want unknown names sorted", err)
	}
	if len(archives.openedNames()) != 0 {
		t.Fatalf("archives opened despite the scoping error: %v", archives.openedNames())
	}
}

func TestScopePackagesDedupesRequestedNames(t *testing.T) {
	all := []ocix.TitledManifest{{Title: "a"}, {Title: "b"}}
	got, err := scopePackages(all, []string{"a", "a"})
	if err != nil {
		t.Fatalf("scopePackages: %v", err)
	}
	if len(got) != 1 || got[0].Title != "a" {
		t.Fatalf("got %+v, want exactly one entry for a", got)
	}
}

// TestRunDeterministicSummaryUnderParallel builds a wider catalog mixing
// every outcome and asserts the exact expected Summary — the aggregate
// counts must come out identical regardless of goroutine scheduling.
// Run with -race to exercise the concurrent aggregation itself.
func TestRunDeterministicSummaryUnderParallel(t *testing.T) {
	const n = 12
	up := newUpstream(t)
	wireUpstream(t, up)
	pkgs := map[string]string{}
	archives := newArchiveFixtures()
	var wantFilled, wantPresent, wantNotFillable int
	for i := 0; i < n; i++ {
		name := "pkg" + string(rune('a'+i))
		switch i % 3 {
		case 0: // present on every platform
			payload := []byte(name)
			pkgs[name] = urlPkg(name, "1.0.0", up.URL+"/"+name+"/{{.Version}}", sha256Hex(payload))
			s := memory.New()
			platMap := map[string][]byte{}
			for _, plat := range spec.SupportedPlatforms {
				platMap[plat.String()] = payload
			}
			ocix.PushFakeArchive(t, s, "1.0.0", platMap)
			archives.set(name, s)
			wantPresent += len(spec.SupportedPlatforms)
		case 1: // missing -> filled
			payload := []byte(name)
			pkgs[name] = urlPkg(name, "1.0.0", up.URL+"/"+name+"/{{.Version}}", sha256Hex(payload))
			up.set("/"+name+"/1.0.0", payload)
			wantFilled += len(spec.SupportedPlatforms)
		case 2: // not fillable
			pkgs[name] = ociPkg(name, "1.0.0")
			wantNotFillable++
		}
	}
	store, tag := newCatalog(t, pkgs)
	wireCatalog(t, store, tag)
	wireArchives(t, archives)

	rep := &fakeReporter{}
	summary, err := Run(context.Background(), newHome(t), Options{CatalogRef: "example.com/cat:v2"}, rep)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := Summary{Packages: n, Filled: wantFilled, Present: wantPresent, NotFillable: wantNotFillable}
	if summary != want {
		t.Fatalf("summary = %+v, want %+v", summary, want)
	}
}

// TestSummaryString pins the exact one-line format the contract example
// shows; Failed is deliberately absent from it.
func TestSummaryString(t *testing.T) {
	s := Summary{Packages: 600, Filled: 320, Healed: 4, Present: 950, NotFillable: 102}
	want := "Filled 600 packages, 320 fill(s), 4 heal(s), 950 present, 102 package(s) not fillable"
	if got := s.String(); got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

// TestRunRejectsUntaggedRef proves the catalog ref is validated up front.
func TestRunRejectsUntaggedRef(t *testing.T) {
	if _, err := Run(context.Background(), newHome(t), Options{CatalogRef: "example.com/cat"}, &fakeReporter{}); err == nil {
		t.Fatal("untagged catalog ref must be rejected")
	}
}

// TestRunCtxCancelledBeforeStartAborts proves an already-cancelled ctx
// aborts the whole run: staging never runs, Run returns ctx.Err(), and no
// package task is created.
func TestRunCtxCancelledBeforeStartAborts(t *testing.T) {
	store, tag := newCatalog(t, map[string]string{
		"go": urlPkg("go", "1.0.0", "https://example.com/go/{{.Version}}", "deadbeef"),
	})
	var opened bool
	t.Cleanup(SetCatalogOpener(func(string) (oras.ReadOnlyTarget, string, error) {
		opened = true
		return store, tag, nil
	}))
	wireArchives(t, newArchiveFixtures())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	rep := &fakeReporter{}
	_, err := Run(ctx, newHome(t), Options{CatalogRef: "example.com/cat:v2"}, rep)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}
	if opened {
		t.Fatal("cancelled-before-start run must not open the catalog")
	}
}

// TestStageCatalogReportsProgressOnStagingTask proves the "Pulling
// catalog" task receives real x/y progress as staging copies the index
// closure — total known as the index's own manifest count plus one, done
// reaching it exactly once staging completes. It also checks a Status
// call happened: Count alone never renders
// (report.TestLiveCountAloneRendersNothingUntilStatusIsSet).
func TestStageCatalogReportsProgressOnStagingTask(t *testing.T) {
	const n = 5
	pkgs := map[string]string{}
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("pkg%02d", i)
		pkgs[name] = urlPkg(name, "1.0.0", "https://example.com/"+name+"/{{.Version}}", "deadbeef")
	}
	store, tag := newCatalog(t, pkgs)
	wireCatalog(t, store, tag)
	wireArchives(t, newArchiveFixtures())

	rep := &fakeReporter{}
	opts := Options{CatalogRef: "example.com/cat:v2", DryRun: true}
	if _, err := Run(context.Background(), newHome(t), opts, rep); err != nil {
		t.Fatalf("Run: %v", err)
	}

	task := rep.taskFor("Pulling catalog")
	if task == nil {
		t.Fatal("no Pulling catalog task")
	}
	if len(task.snapshotStatuses()) == 0 {
		t.Fatal("no Status call recorded on the staging task: Count alone never renders (segment stays \"\")")
	}
	counts := task.snapshotCounts()
	if len(counts) == 0 {
		t.Fatal("no Count calls recorded on the staging task")
	}
	wantTotal := n + 1
	last := counts[len(counts)-1]
	if last.done != wantTotal || last.total != wantTotal {
		t.Fatalf("final count = %+v, want done=total=%d", last, wantTotal)
	}
	for _, c := range counts {
		if c.total != 0 && c.total != wantTotal {
			t.Fatalf("count %+v: total changed mid-run, want a stable %d once known", c, wantTotal)
		}
	}
}
