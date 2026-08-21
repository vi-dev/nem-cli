package clean

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vi-dev/nem-cli/internal/fsx"
	"github.com/vi-dev/nem-cli/internal/usage"
)

func TestExecuteDeletesAndReportsFreedBytes(t *testing.T) {
	h, root := scanHome(t)
	victim := filepath.Join(root, "tmp", "go-build-1")
	writeFile(t, filepath.Join(victim, "src", "main.c"), "int main(){}")

	freed, err := Execute(h, Plan{Entries: []Entry{
		{Path: victim, Reason: "leaked staging", Size: 12},
	}}, Observer{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if freed != 12 {
		t.Errorf("freed = %d, want 12", freed)
	}
	if _, err := os.Stat(victim); !os.IsNotExist(err) {
		t.Errorf("victim survived: %v", err)
	}
}

func TestExecuteRefusesPathsOutsideHome(t *testing.T) {
	h, _ := scanHome(t)
	outside := filepath.Join(t.TempDir(), "precious")
	writeFile(t, outside, "do not delete")

	freed, err := Execute(h, Plan{Entries: []Entry{{Path: outside, Size: 99}}}, Observer{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if freed != 0 {
		t.Errorf("freed = %d, want 0 for a refused path", freed)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("a path outside NEM_HOME was deleted: %v", err)
	}
}

func TestExecuteRefusesSymlinks(t *testing.T) {
	h, root := scanHome(t)
	target := filepath.Join(t.TempDir(), "target")
	writeFile(t, target, "real data")

	link := filepath.Join(root, "tmp", "link")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	freed, err := Execute(h, Plan{Entries: []Entry{{Path: link, Size: 1}}}, Observer{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if freed != 0 {
		t.Errorf("freed = %d, want 0 for a refused symlink", freed)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("symlink itself was deleted rather than skipped: %v", err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("following a symlink destroyed its target: %v", err)
	}
}

func TestExecuteTreatsMissingPathAsDone(t *testing.T) {
	h, root := scanHome(t)
	freed, err := Execute(h, Plan{Entries: []Entry{
		{Path: filepath.Join(root, "tmp", "already-gone"), Size: 5},
	}}, Observer{})
	if err != nil {
		t.Fatalf("a vanished path must not fail the run: %v", err)
	}
	if freed != 0 {
		t.Errorf("freed = %d, want 0 for a path that was already gone", freed)
	}
}

func TestExecuteSkipsAVersionRestampedSincePlanning(t *testing.T) {
	h, root := scanHome(t)
	victim := filepath.Join(root, "packages", "go", "1.26.5")
	writeFile(t, filepath.Join(victim, "bin", "go"), "x")

	planned := time.Now().Add(-90 * 24 * time.Hour)
	// Someone cd'd into a go project between planning and executing.
	usage.Stamp(h, time.Now(), []string{usage.Key("go", "1.26.5")})

	freed, err := Execute(h, Plan{Confirm: true, Entries: []Entry{{
		Path: victim, Reason: "unused 90d", Size: 259,
		Key: usage.Key("go", "1.26.5"), Stamp: planned, Recheck: true,
	}}}, Observer{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if freed != 0 {
		t.Errorf("freed = %d, want 0 for a revived version", freed)
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("a version used since planning was deleted: %v", err)
	}
}

func TestExecutePrunesTheIndexOfDeletedVersions(t *testing.T) {
	h, root := scanHome(t)
	victim := filepath.Join(root, "packages", "go", "1.26.5")
	writeFile(t, filepath.Join(victim, "bin", "go"), "x")
	writeFile(t, filepath.Join(root, "packages", "go", "1.26.6", "bin", "go"), "x")

	old := time.Now().Add(-90 * 24 * time.Hour)
	if err := usage.Save(h, usage.Index{
		usage.Key("go", "1.26.5"): old,
		usage.Key("go", "1.26.6"): time.Now(),
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if _, err := Execute(h, Plan{Confirm: true, Entries: []Entry{{
		Path: victim, Reason: "unused 90d", Size: 1,
		Key: usage.Key("go", "1.26.5"), Stamp: old, Recheck: true,
	}}}, Observer{}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	idx := usage.Load(h)
	if _, ok := idx.LastUsed("go", "1.26.5"); ok {
		t.Error("index still names a deleted version")
	}
	if _, ok := idx.LastUsed("go", "1.26.6"); !ok {
		t.Error("index dropped a surviving version")
	}
}

func TestExecuteRereadsStampBeforeEachEntryNotOnceAtStart(t *testing.T) {
	h, root := scanHome(t)

	// slow is large enough that its os.RemoveAll takes real wall-clock
	// time, giving the concurrent stamp write below room to land before
	// the second entry is reached.
	slow := filepath.Join(root, "packages", "slow", "1.0.0")
	for i := 0; i < 3000; i++ {
		writeFile(t, filepath.Join(slow, fmt.Sprintf("f%04d", i)), "x")
	}
	victim := filepath.Join(root, "packages", "go", "1.26.5")
	writeFile(t, filepath.Join(victim, "bin", "go"), "x")

	old := time.Now().Add(-90 * 24 * time.Hour)
	keySlow := usage.Key("slow", "1.0.0")
	keyGo := usage.Key("go", "1.26.5")
	if err := usage.Save(h, usage.Index{keySlow: old, keyGo: old}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	plan := Plan{Confirm: true, Entries: []Entry{
		{Path: slow, Reason: "unused 90d", Size: 1, Key: keySlow, Stamp: old, Recheck: true},
		{Path: victim, Reason: "unused 90d", Size: 259, Key: keyGo, Stamp: old, Recheck: true},
	}}

	// Simulates a cd into a go project while the run is still deleting the
	// first entry: the stamp for the second entry moves after Execute has
	// started but before that entry is reached. A single Load at the top
	// of Execute would miss this; a fresh read per entry would not.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		usage.Stamp(h, time.Now(), []string{keyGo})
	}()

	freed, err := Execute(h, plan, Observer{})
	wg.Wait()
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("a version revived mid-run was deleted: %v", err)
	}
	if freed != 1 {
		t.Errorf("freed = %d, want 1 (only the first entry)", freed)
	}
}

func TestExecutePrunesAnAlreadyGoneKeyedEntry(t *testing.T) {
	h, root := scanHome(t)
	writeFile(t, filepath.Join(root, "packages", "go", "1.26.6", "bin", "go"), "x")

	old := time.Now().Add(-90 * 24 * time.Hour)
	if err := usage.Save(h, usage.Index{
		usage.Key("go", "1.26.5"): old,
		usage.Key("go", "1.26.6"): time.Now(),
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// go@1.26.5's directory is already gone by the time Execute runs, as
	// if a concurrent process removed it first.
	if _, err := Execute(h, Plan{Confirm: true, Entries: []Entry{{
		Path: filepath.Join(root, "packages", "go", "1.26.5"),
		Size: 1, Key: usage.Key("go", "1.26.5"), Stamp: old, Recheck: true,
	}}}, Observer{}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	idx := usage.Load(h)
	if _, ok := idx.LastUsed("go", "1.26.5"); ok {
		t.Error("index still names a version whose directory was already gone")
	}
	if _, ok := idx.LastUsed("go", "1.26.6"); !ok {
		t.Error("index dropped a surviving version")
	}
}

func TestExecutePrunesEvenWhenALaterEntryFailsToDelete(t *testing.T) {
	h, root := scanHome(t)
	writeFile(t, filepath.Join(root, "packages", "go", "1.26.5", "bin", "go"), "x")

	blocked := filepath.Join(root, "packages", "zig", "0.16.0")
	writeFile(t, filepath.Join(blocked, "bin", "zig"), "x")
	if err := os.Chmod(blocked, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(blocked, 0o755) })

	old := time.Now().Add(-90 * 24 * time.Hour)
	keyGo := usage.Key("go", "1.26.5")
	keyZig := usage.Key("zig", "0.16.0")
	if err := usage.Save(h, usage.Index{keyGo: old, keyZig: old}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	_, err := Execute(h, Plan{Confirm: true, Entries: []Entry{
		{Path: filepath.Join(root, "packages", "go", "1.26.5"), Size: 1, Key: keyGo, Stamp: old, Recheck: true},
		{Path: blocked, Size: 1, Key: keyZig, Stamp: old, Recheck: true},
	}}, Observer{})
	if err == nil {
		t.Skip("could not force a deletion failure in this environment (e.g. running as root)")
	}

	idx := usage.Load(h)
	if _, ok := idx.LastUsed("go", "1.26.5"); ok {
		t.Error("a version deleted before the later failure was not pruned")
	}
}

func TestExecutePrunesTheIndexAfterAll(t *testing.T) {
	h, root := scanHome(t)
	victim := filepath.Join(root, "packages", "go", "1.26.6")
	writeFile(t, filepath.Join(victim, "bin", "go"), "x")

	// A stamp from a moment ago: --all deletes regardless, which is what
	// makes it the operation that skips the re-check.
	if err := usage.Save(h, usage.Index{usage.Key("go", "1.26.6"): time.Now()}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	freed, err := Execute(h, Plan{Confirm: true, Entries: []Entry{{
		Path: victim, Reason: "--all", Size: 1, Key: usage.Key("go", "1.26.6"),
	}}}, Observer{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if freed != 1 {
		t.Errorf("freed = %d, want 1; --all does not consult the stamp", freed)
	}
	if _, err := os.Stat(victim); !os.IsNotExist(err) {
		t.Fatalf("--all left a version behind: %v", err)
	}
	if _, ok := usage.Load(h).LastUsed("go", "1.26.6"); ok {
		t.Error("the index still names a version --all deleted")
	}
}

func TestWithinRootRejectsTheRootItself(t *testing.T) {
	if withinRoot("/home/nem", "/home/nem") {
		t.Error("the root path itself must never be considered within root")
	}
}

// TestExecuteTreatsAnUnreadableIndexAsUnverifiableNotEmpty covers the
// pre-delete recheck when usage.json turns unreadable after planning
// already read the stamp that produced the recheck entry below: the
// recheck must not read that failure as "no stamp" and delete the
// version anyway, and the run must not save an empty index over the
// unreadable one. A tier-0 entry, which carries no key and needs no
// recheck, must still be removed.
func TestExecuteTreatsAnUnreadableIndexAsUnverifiableNotEmpty(t *testing.T) {
	h, root := scanHome(t)
	staging := filepath.Join(root, "tmp", "go-build-1")
	writeFile(t, filepath.Join(staging, "src", "main.c"), "int main(){}")

	victim := filepath.Join(root, "packages", "go", "1.26.5")
	writeFile(t, filepath.Join(victim, "bin", "go"), "x")

	old := time.Now().Add(-90 * 24 * time.Hour)
	if err := usage.Save(h, usage.Index{usage.Key("go", "1.26.5"): old}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	corrupt := []byte("{not json")
	if err := os.WriteFile(h.Usage(), corrupt, 0o644); err != nil {
		t.Fatalf("corrupt usage.json: %v", err)
	}

	freed, err := Execute(h, Plan{Confirm: true, Entries: []Entry{
		{Path: staging, Reason: "leaked staging", Size: 12},
		{Path: victim, Reason: "unused 90d", Size: 1, Key: usage.Key("go", "1.26.5"), Stamp: old, Recheck: true},
	}}, Observer{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if freed != 12 {
		t.Errorf("freed = %d, want 12 (tier 0 only; the recheck entry could not be verified)", freed)
	}
	if _, statErr := os.Stat(staging); !os.IsNotExist(statErr) {
		t.Error("tier-0 garbage survived an unreadable usage index")
	}
	if _, statErr := os.Stat(victim); statErr != nil {
		t.Errorf("a recheck entry was deleted while its stamp could not be verified: %v", statErr)
	}

	raw, err := os.ReadFile(h.Usage())
	if err != nil {
		t.Fatalf("read usage.json after Execute: %v", err)
	}
	if string(raw) != string(corrupt) {
		t.Errorf("usage.json was overwritten; an unreadable index must not be saved over, got %q", raw)
	}
}

// TestExecuteSurfacesNonNotExistLstatErrors covers an Lstat failure that
// is not ENOENT (an unreadable store, for instance): unlike a vanished
// path, it must not be silently skipped — it has to fail the run the
// same way a RemoveAll failure does, while what was already freed is
// still reported.
func TestExecuteSurfacesNonNotExistLstatErrors(t *testing.T) {
	h, root := scanHome(t)
	staging := filepath.Join(root, "tmp", "go-build-1")
	writeFile(t, filepath.Join(staging, "big"), "x")

	parent := filepath.Join(root, "packages", "zig")
	blocked := filepath.Join(parent, "0.16.0")
	writeFile(t, filepath.Join(blocked, "bin", "zig"), "x")
	if err := os.Chmod(parent, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })

	// Confirm the permission is actually enforced (it is not under root,
	// for instance) independent of Execute's own handling, so a silently
	// swallowed error below can't be mistaken for an environment that
	// never produced one.
	if _, statErr := os.Lstat(blocked); statErr == nil || os.IsNotExist(statErr) {
		t.Skip("this environment does not enforce the permission needed to force an Lstat error (e.g. running as root)")
	}

	freed, err := Execute(h, Plan{Entries: []Entry{
		{Path: staging, Reason: "leaked staging", Size: 1},
		{Path: blocked, Size: 1},
	}}, Observer{})
	if err == nil {
		t.Fatal("Execute returned no error for a non-ENOENT Lstat failure")
	}
	if freed != 1 {
		t.Errorf("freed = %d, want 1 for the entry removed before the Lstat error", freed)
	}
}

// TestPruneIndexTakesTheStoreLock proves Execute's index bookkeeping
// blocks on the store lock rather than racing a concurrent holder: while
// the lock is held externally, Execute must not return, and once it is
// released the pruned index must reflect the run's deletion.
func TestPruneIndexTakesTheStoreLock(t *testing.T) {
	h, root := scanHome(t)
	victim := filepath.Join(root, "packages", "go", "1.26.5")
	writeFile(t, filepath.Join(victim, "bin", "go"), "x")

	old := time.Now().Add(-90 * 24 * time.Hour)
	if err := usage.Save(h, usage.Index{usage.Key("go", "1.26.5"): old}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	release, err := fsx.Lock(h.LockFile())
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}
	t.Cleanup(release)

	errc := make(chan error, 1)
	go func() {
		_, err := Execute(h, Plan{Confirm: true, Entries: []Entry{{
			Path: victim, Reason: "unused 90d", Size: 1,
			Key: usage.Key("go", "1.26.5"), Stamp: old, Recheck: true,
		}}}, Observer{})
		errc <- err
	}()

	select {
	case <-errc:
		t.Fatal("Execute finished pruning while the store lock was held")
	case <-time.After(200 * time.Millisecond):
	}

	release()
	select {
	case err := <-errc:
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Execute did not finish after the store lock was released")
	}

	if _, ok := usage.Load(h).LastUsed("go", "1.26.5"); ok {
		t.Error("index still names a version deleted while the lock was held")
	}
}

// TestExecuteObserverReportsRemovingInOrder proves Removing fires once per
// deleted entry, in plan order: a caller driving a live progress line off
// it needs the sequence to match the order entries actually disappear in.
func TestExecuteObserverReportsRemovingInOrder(t *testing.T) {
	h, root := scanHome(t)
	first := filepath.Join(root, "tmp", "go-build-1")
	writeFile(t, filepath.Join(first, "src", "main.c"), "int main(){}")
	second := filepath.Join(root, "packages", "go", "1.26.5")
	writeFile(t, filepath.Join(second, "bin", "go"), "x")

	var removing []string
	obs := Observer{Removing: func(e Entry) { removing = append(removing, e.Path) }}

	if _, err := Execute(h, Plan{Entries: []Entry{
		{Path: first, Reason: "leaked staging", Size: 12},
		{Path: second, Reason: "--all", Size: 1, Key: usage.Key("go", "1.26.5")},
	}}, obs); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	got := strings.Join(removing, ",")
	want := strings.Join([]string{first, second}, ",")
	if got != want {
		t.Errorf("Removing order = %q, want %q", got, want)
	}
}

// TestExecuteObserverReportsRevivedVersionAsSkipped covers the recheck
// path's own skip reason: a version whose stamp moved since planning must
// be reported as "used since planning", not folded into a generic skip.
func TestExecuteObserverReportsRevivedVersionAsSkipped(t *testing.T) {
	h, root := scanHome(t)
	victim := filepath.Join(root, "packages", "go", "1.26.5")
	writeFile(t, filepath.Join(victim, "bin", "go"), "x")

	planned := time.Now().Add(-90 * 24 * time.Hour)
	usage.Stamp(h, time.Now(), []string{usage.Key("go", "1.26.5")})

	var skippedPath, skippedReason string
	obs := Observer{Skipped: func(e Entry, reason string) {
		skippedPath, skippedReason = e.Path, reason
	}}

	if _, err := Execute(h, Plan{Confirm: true, Entries: []Entry{{
		Path: victim, Reason: "unused 90d", Size: 259,
		Key: usage.Key("go", "1.26.5"), Stamp: planned, Recheck: true,
	}}}, obs); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if skippedPath != victim {
		t.Errorf("Skipped entry = %q, want %q", skippedPath, victim)
	}
	if skippedReason != "used since planning" {
		t.Errorf("Skipped reason = %q, want %q", skippedReason, "used since planning")
	}
}

// TestExecuteObserverReportsSymlinkAsSkipped covers the symlink refusal's
// own skip reason.
func TestExecuteObserverReportsSymlinkAsSkipped(t *testing.T) {
	h, root := scanHome(t)
	target := filepath.Join(t.TempDir(), "target")
	writeFile(t, target, "real data")

	link := filepath.Join(root, "tmp", "link")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	var reason string
	obs := Observer{Skipped: func(_ Entry, r string) { reason = r }}

	if _, err := Execute(h, Plan{Entries: []Entry{{Path: link, Size: 1}}}, obs); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if reason != "symlink" {
		t.Errorf("Skipped reason = %q, want %q", reason, "symlink")
	}
}

// TestExecuteObserverIgnoresAnAlreadyGoneEntry proves an ENOENT entry
// invokes neither callback: it was never skipped and there was nothing to
// remove, so it must not appear in either report.
func TestExecuteObserverIgnoresAnAlreadyGoneEntry(t *testing.T) {
	h, root := scanHome(t)

	called := false
	obs := Observer{
		Removing: func(Entry) { called = true },
		Skipped:  func(Entry, string) { called = true },
	}

	if _, err := Execute(h, Plan{Entries: []Entry{
		{Path: filepath.Join(root, "tmp", "already-gone"), Size: 5},
	}}, obs); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if called {
		t.Error("an already-gone entry invoked an observer callback")
	}
}
