package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vi-dev/nem-cli/internal/home"
	"github.com/vi-dev/nem-cli/internal/usage"
)

// execNemClean executes the root command with args against an isolated
// NEM_HOME, answering any confirmation prompt with EOF (a refusal).
func execNemClean(t *testing.T, root string, args ...string) (string, error) {
	t.Helper()
	return execNemCleanIn(t, root, "", args...)
}

// execNemCleanIn is execNemClean with a caller-supplied answer for the
// confirmation prompt.
func execNemCleanIn(t *testing.T, root, in string, args ...string) (string, error) {
	t.Helper()
	t.Setenv("NEM_HOME", root)

	cmd := newRoot()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetIn(strings.NewReader(in))
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func seedStaging(t *testing.T, root string) string {
	t.Helper()
	dir := filepath.Join(root, "tmp", "go-build-1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "big"), bytes.Repeat([]byte("x"), 1024), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return dir
}

// seedPackageVersion creates an installed package version under
// packages/<name>/<version> and returns its directory.
func seedPackageVersion(t *testing.T, root, name, version string) string {
	t.Helper()
	dir := filepath.Join(root, "packages", name, version, "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	return filepath.Join(root, "packages", name, version)
}

func TestCleanRemovesLeakedStaging(t *testing.T) {
	root := t.TempDir()
	dir := seedStaging(t, root)

	out, err := execNemClean(t, root, "clean", "--grace", "0s")
	if err != nil {
		t.Fatalf("clean: %v\n%s", err, out)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("staging survived bare clean: %v", err)
	}
}

func TestCleanDryRunDeletesNothing(t *testing.T) {
	root := t.TempDir()
	dir := seedStaging(t, root)

	out, err := execNemClean(t, root, "clean", "--grace", "0s", "--dry-run")
	if err != nil {
		t.Fatalf("clean: %v\n%s", err, out)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("--dry-run deleted something: %v", err)
	}
	if !strings.Contains(out, "leaked staging") {
		t.Errorf("--dry-run should still print the plan, got:\n%s", out)
	}
}

func TestCleanRejectsUnusedWithAll(t *testing.T) {
	root := t.TempDir()
	_, err := execNemClean(t, root, "clean", "--unused", "30d", "--all")
	if err == nil {
		t.Fatal("--unused with --all must be a usage error")
	}
}

func TestCleanRejectsMalformedAge(t *testing.T) {
	root := t.TempDir()
	_, err := execNemClean(t, root, "clean", "--unused", "30m")
	if err == nil {
		t.Fatal("--unused 30m must be rejected")
	}
	if !strings.Contains(err.Error(), "30d") {
		t.Errorf("error must name the accepted shape, got %q", err)
	}
}

func TestCleanOnEmptyHomeSucceeds(t *testing.T) {
	root := t.TempDir()
	out, err := execNemClean(t, root, "clean")
	if err != nil {
		t.Fatalf("clean on empty home: %v\n%s", err, out)
	}
}

// TestCleanRefusalLeavesPackagesIntact covers the confirmation gate in
// runClean: a plan that touches packages/, answered "n", must delete
// nothing and must not turn into a command error. It seeds the --unused
// route via usageIndex, the package var that exists so a test can supply a
// fixed index without writing a real usage.json.
func TestCleanRefusalLeavesPackagesIntact(t *testing.T) {
	root := t.TempDir()
	dir := seedPackageVersion(t, root, "go", "1.26.5")

	orig := usageIndex
	t.Cleanup(func() { usageIndex = orig })
	old := time.Now().Add(-90 * 24 * time.Hour)
	usageIndex = func() usage.Index {
		return usage.Index{usage.Key("go", "1.26.5"): old}
	}

	out, err := execNemCleanIn(t, root, "n\n", "clean", "--unused", "30d")
	if err != nil {
		t.Fatalf("clean: %v\n%s", err, out)
	}
	if _, statErr := os.Stat(dir); statErr != nil {
		t.Fatalf("a refused confirmation deleted the package store: %v", statErr)
	}
}

// TestCleanYesSkipsThePromptAndDeletes covers the other side of the same
// gate: -y must proceed without reading the confirmation answer at all, so
// EOF on stdin (no answer supplied) must not be mistaken for a refusal. The
// completed run reports what it freed through the task summary rather than
// the old standalone success line, but the transcript must still say so.
func TestCleanYesSkipsThePromptAndDeletes(t *testing.T) {
	root := t.TempDir()
	dir := seedPackageVersion(t, root, "go", "1.26.5")

	out, err := execNemCleanIn(t, root, "", "clean", "--all", "-y")
	if err != nil {
		t.Fatalf("clean: %v\n%s", err, out)
	}
	if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
		t.Fatalf("-y left the package version behind: %v", statErr)
	}
	if !strings.Contains(out, "Reclaimed") {
		t.Errorf("a completed run must report what it reclaimed, got:\n%s", out)
	}
}

// TestCleanReportsASkippedRevivedVersion covers the progress design's
// durable skip line: a version used since planning must be named as
// skipped, with its reason, even though the run as a whole still succeeds.
// usageIndex, the package var, only feeds the planner; Execute's own
// pre-delete recheck reads the real usage.json on disk, so seeding that
// file with a fresher stamp than usageIndex reports is what makes the
// planned version look revived by the time Execute reaches it.
func TestCleanReportsASkippedRevivedVersion(t *testing.T) {
	root := t.TempDir()
	dir := seedPackageVersion(t, root, "go", "1.26.5")

	h := home.Resolve(func(k string) string {
		if k == "NEM_HOME" {
			return root
		}
		return ""
	})
	if err := usage.Save(h, usage.Index{usage.Key("go", "1.26.5"): time.Now()}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	orig := usageIndex
	t.Cleanup(func() { usageIndex = orig })
	old := time.Now().Add(-90 * 24 * time.Hour)
	usageIndex = func() usage.Index {
		return usage.Index{usage.Key("go", "1.26.5"): old}
	}

	out, err := execNemCleanIn(t, root, "", "clean", "--unused", "30d", "-y")
	if err != nil {
		t.Fatalf("clean: %v\n%s", err, out)
	}
	if !strings.Contains(out, "skipped go/1.26.5: used since planning") {
		t.Errorf("missing skip line for a revived version, got:\n%s", out)
	}
	if _, statErr := os.Stat(dir); statErr != nil {
		t.Fatalf("a version used since planning was deleted: %v", statErr)
	}
}

// TestCleanReportsFreedBytesOnAMidRunFailure covers runClean's handling of
// clean.Execute's return: a run that deletes one version and then hits a
// permission error on a later one must still report what it freed, not
// only the error.
func TestCleanReportsFreedBytesOnAMidRunFailure(t *testing.T) {
	root := t.TempDir()
	seedPackageVersion(t, root, "go", "1.26.5")
	blocked := seedPackageVersion(t, root, "zig", "0.16.0")

	// Readable and listable (so Scan can still size it), but not
	// writable, so removing its contents fails without hiding it from
	// the plan the way an unlistable directory would.
	if err := os.Chmod(blocked, 0o555); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(blocked, 0o755) })

	out, err := execNemCleanIn(t, root, "", "clean", "--all", "-y")
	if err == nil {
		t.Skip("could not force a deletion failure in this environment (e.g. running as root)")
	}
	if !strings.Contains(out, "Reclaimed") {
		t.Errorf("a failed run must still report what it freed, got:\n%s", out)
	}
}

// TestCleanUnusedPadsTheWindowByTheStampDebounce covers the gap between a
// stamp's recorded time and the real last use it can understate by up to
// usage.Debounce (usage.Stamp suppresses a write within that hour): with
// --unused 1h, a version stamped 61 minutes ago must survive, since real
// use could be as recent as one minute ago, while one stamped past the
// padded 2h window must be evicted.
func TestCleanUnusedPadsTheWindowByTheStampDebounce(t *testing.T) {
	root := t.TempDir()
	recent := seedPackageVersion(t, root, "recent", "1.0.0")
	stale := seedPackageVersion(t, root, "stale", "1.0.0")

	orig := usageIndex
	t.Cleanup(func() { usageIndex = orig })
	usageIndex = func() usage.Index {
		return usage.Index{
			usage.Key("recent", "1.0.0"): time.Now().Add(-61 * time.Minute),
			usage.Key("stale", "1.0.0"):  time.Now().Add(-2*time.Hour - time.Minute),
		}
	}

	out, err := execNemCleanIn(t, root, "", "clean", "--unused", "1h", "-y")
	if err != nil {
		t.Fatalf("clean: %v\n%s", err, out)
	}
	if _, statErr := os.Stat(recent); statErr != nil {
		t.Fatalf("a version stamped inside the debounce-padded window was evicted: %v", statErr)
	}
	if _, statErr := os.Stat(stale); !os.IsNotExist(statErr) {
		t.Fatalf("a version stamped past the debounce-padded window survived: %v", statErr)
	}
}

// TestCleanAllLeavesSyncedCatalogStoreOnDisk covers the core promise of
// this command: catalogs are never touched, in any mode, including the
// mode that previously emptied them. A synced catalog store must survive
// --all untouched and never appear in the printed plan, even while an
// installed package version in the same run is removed as normal.
func TestCleanAllLeavesSyncedCatalogStoreOnDisk(t *testing.T) {
	root := t.TempDir()
	store := filepath.Join(root, "catalogs", "official", "store")
	if err := os.MkdirAll(filepath.Join(store, "blobs", "sha256"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(store, "blobs", "sha256", "aaa"), bytes.Repeat([]byte("x"), 2048), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(store, "index.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	pkgDir := seedPackageVersion(t, root, "go", "1.26.5")

	out, err := execNemCleanIn(t, root, "", "clean", "--all", "-y")
	if err != nil {
		t.Fatalf("clean: %v\n%s", err, out)
	}
	if strings.Contains(out, store) {
		t.Errorf("--all listed the catalog store in its plan:\n%s", out)
	}
	if _, statErr := os.Stat(store); statErr != nil {
		t.Fatalf("--all removed the catalog store: %v", statErr)
	}
	if _, statErr := os.Stat(pkgDir); !os.IsNotExist(statErr) {
		t.Fatalf("--all left the package version behind: %v", statErr)
	}
}
