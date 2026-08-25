package clean

import (
	"testing"
	"time"

	"github.com/vi-dev/nem-cli/internal/usage"
)

var now = time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)

func ago(d time.Duration) time.Time { return now.Add(-d) }

func reasons(p Plan) map[string]string {
	out := map[string]string{}
	for _, e := range p.Entries {
		out[e.Path] = e.Reason
	}
	return out
}

func TestBuildTierZeroClassifiesEverySource(t *testing.T) {
	s := Store{
		Staging:      []Item{{Path: "/tmp/go-build-1", Newest: ago(48 * time.Hour), Size: 100}},
		Partials:     []Item{{Path: "/pkgs/go/1.26.6-x.tmp", Newest: ago(48 * time.Hour), Size: 10}},
		TestInstalls: []Item{{Path: "/pkgs/tool-NEMTEST-1", Newest: ago(48 * time.Hour), Size: 5}},
	}
	p := Build(s, usage.Index{}, Options{Grace: time.Hour}, now)

	got := reasons(p)
	want := map[string]string{
		"/tmp/go-build-1":       "leaked staging",
		"/pkgs/go/1.26.6-x.tmp": "partial install",
		"/pkgs/tool-NEMTEST-1":  "leftover test install",
	}
	for path, reason := range want {
		if got[path] != reason {
			t.Errorf("%s: reason = %q, want %q", path, got[path], reason)
		}
	}
	if p.Total() != 115 {
		t.Errorf("Total() = %d, want 115", p.Total())
	}
	if p.Confirm {
		t.Error("tier 0 must not set Confirm")
	}
}

func TestBuildSkipsTestInstallsInsideGrace(t *testing.T) {
	s := Store{TestInstalls: []Item{
		{Path: "/pkgs/tool-NEMTEST-live", Newest: ago(30 * time.Minute)},
		{Path: "/pkgs/tool-NEMTEST-dead", Newest: ago(90 * time.Minute)},
	}}
	p := Build(s, usage.Index{}, Options{Grace: time.Hour}, now)

	if len(p.Entries) != 1 || p.Entries[0].Path != "/pkgs/tool-NEMTEST-dead" {
		t.Fatalf("grace window wrong, planned %v", p.Entries)
	}
}

func TestBuildSkipsStagingInsideGrace(t *testing.T) {
	s := Store{Staging: []Item{
		{Path: "/tmp/live", Newest: ago(30 * time.Minute)},
		{Path: "/tmp/dead", Newest: ago(90 * time.Minute)},
	}}
	p := Build(s, usage.Index{}, Options{Grace: time.Hour}, now)

	if len(p.Entries) != 1 || p.Entries[0].Path != "/tmp/dead" {
		t.Fatalf("grace window wrong, planned %v", p.Entries)
	}
}

func TestBuildStagingExactlyAtGraceIsReclaimed(t *testing.T) {
	s := Store{Staging: []Item{{Path: "/tmp/edge", Newest: ago(time.Hour)}}}
	p := Build(s, usage.Index{}, Options{Grace: time.Hour}, now)
	if len(p.Entries) != 1 {
		t.Fatalf("staging exactly at the grace boundary should be reclaimed, got %v", p.Entries)
	}
}

func TestBuildReclaimsLeakedDownloadsOutsideGrace(t *testing.T) {
	s := Store{Downloads: []Item{
		{Path: "/tmp/go-1.26.6-live.tmp", Newest: ago(time.Minute), Size: 900},
		{Path: "/tmp/go-1.26.6-dead.tmp", Newest: ago(90 * time.Minute), Size: 100},
	}}
	p := Build(s, usage.Index{}, Options{Grace: time.Hour}, now)

	if len(p.Entries) != 1 || p.Entries[0].Path != "/tmp/go-1.26.6-dead.tmp" {
		t.Fatalf("a download still streaming must be left alone, planned %v", p.Entries)
	}
	if p.Entries[0].Reason != "leaked download" {
		t.Errorf("reason = %q, want %q", p.Entries[0].Reason, "leaked download")
	}
}

func TestBuildSkipsPartialInsideGrace(t *testing.T) {
	s := Store{Partials: []Item{
		{Path: "/pkgs/go/1.26.6-live.tmp", Newest: ago(5 * time.Minute)},
		{Path: "/pkgs/go/1.26.6-dead.tmp", Newest: ago(90 * time.Minute)},
	}}
	p := Build(s, usage.Index{}, Options{Grace: time.Hour}, now)

	if len(p.Entries) != 1 || p.Entries[0].Path != "/pkgs/go/1.26.6-dead.tmp" {
		t.Fatalf("an install still extracting must be left alone, planned %v", p.Entries)
	}
}

func TestBuildUnusedEvictsOnlyStampedAndStale(t *testing.T) {
	s := Store{Versions: []Version{
		{Name: "go", Version: "1.26.5", Path: "/pkgs/go/1.26.5", Size: 259},
		{Name: "go", Version: "1.26.6", Path: "/pkgs/go/1.26.6", Size: 259},
		{Name: "zig", Version: "0.16.0", Path: "/pkgs/zig/0.16.0", Size: 403},
	}}
	idx := usage.Index{
		usage.Key("go", "1.26.5"): ago(79 * 24 * time.Hour),
		usage.Key("go", "1.26.6"): ago(2 * time.Hour),
		// zig has no stamp at all
	}
	p := Build(s, idx, Options{Unused: 30 * 24 * time.Hour}, now)

	if len(p.Entries) != 1 {
		t.Fatalf("planned %v, want only go@1.26.5", p.Entries)
	}
	e := p.Entries[0]
	if e.Path != "/pkgs/go/1.26.5" {
		t.Errorf("evicted %q, want /pkgs/go/1.26.5", e.Path)
	}
	if e.Reason != "unused 79d" {
		t.Errorf("reason = %q, want %q", e.Reason, "unused 79d")
	}
	if e.Key != usage.Key("go", "1.26.5") {
		t.Errorf("Key = %q, want the usage key", e.Key)
	}
	if !e.Recheck {
		t.Error("--unused acts on the stamp, so the entry must ask for a re-check")
	}
	if !p.Confirm {
		t.Error("evicting a version must set Confirm")
	}
}

func TestBuildUnusedBoundaryIsInclusive(t *testing.T) {
	s := Store{Versions: []Version{{Name: "go", Version: "1.26.5", Path: "/p"}}}
	idx := usage.Index{usage.Key("go", "1.26.5"): ago(30 * 24 * time.Hour)}
	p := Build(s, idx, Options{Unused: 30 * 24 * time.Hour}, now)
	if len(p.Entries) != 1 {
		t.Fatalf("a version exactly at the window should be evicted, got %v", p.Entries)
	}
}

func TestBuildUnusedReasonUsesHoursUnderADay(t *testing.T) {
	s := Store{Versions: []Version{{Name: "go", Version: "1.26.5", Path: "/p"}}}
	idx := usage.Index{usage.Key("go", "1.26.5"): ago(5 * time.Hour)}
	p := Build(s, idx, Options{Unused: 2 * time.Hour}, now)
	if p.Entries[0].Reason != "unused 5h" {
		t.Fatalf("reason = %q, want %q", p.Entries[0].Reason, "unused 5h")
	}
}

func TestBuildAllTakesEveryVersionIncludingUnstamped(t *testing.T) {
	s := Store{Versions: []Version{
		{Name: "go", Version: "1.26.6", Path: "/pkgs/go/1.26.6", Size: 1},
		{Name: "zig", Version: "0.16.0", Path: "/pkgs/zig/0.16.0", Size: 2},
	}}
	p := Build(s, usage.Index{}, Options{All: true}, now)

	if len(p.Entries) != 2 {
		t.Fatalf("--all planned %d entries, want 2", len(p.Entries))
	}
	for _, e := range p.Entries {
		if e.Reason != "--all" {
			t.Errorf("%s: reason = %q, want %q", e.Path, e.Reason, "--all")
		}
		if e.Recheck {
			t.Errorf("%s: --all is explicit and must skip the stamp re-check", e.Path)
		}
		if e.Key == "" {
			t.Errorf("%s: --all must carry the usage key so its index row is pruned", e.Path)
		}
	}
	if !p.Confirm {
		t.Error("--all must set Confirm")
	}
}

func TestBuildWithoutPackageFlagsLeavesVersionsAlone(t *testing.T) {
	s := Store{Versions: []Version{{Name: "go", Version: "1.26.6", Path: "/p"}}}
	idx := usage.Index{usage.Key("go", "1.26.6"): ago(365 * 24 * time.Hour)}
	p := Build(s, idx, Options{Grace: time.Hour}, now)
	if len(p.Entries) != 0 {
		t.Fatalf("bare clean must not touch packages, planned %v", p.Entries)
	}
}
