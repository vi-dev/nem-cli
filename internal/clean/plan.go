package clean

import (
	"fmt"
	"time"

	"github.com/vi-dev/nem-cli/internal/usage"
)

// Item is one reclaimable path. Newest is the most recent modification
// anywhere in it: for a directory its own mtime is effectively its creation
// time, because a build writes into subdirectories beneath it, so a long
// compile would otherwise look abandoned.
type Item struct {
	Path   string
	Newest time.Time
	Size   int64
}

// Version is one installed package version.
type Version struct {
	Name, Version, Path string
	Size                int64
}

// Store is everything the scanner found on disk.
type Store struct {
	Staging   []Item
	Downloads []Item
	Partials  []Item
	Versions  []Version
}

// Options are the flag-derived knobs. Unused is zero when --unused was not
// given, which leaves every installed version alone.
type Options struct {
	Grace  time.Duration
	Unused time.Duration
	All    bool
}

// Entry is one planned deletion. Key is the usage index row the path owns,
// dropped once the path is gone, and every version entry carries one.
// Recheck asks the executor to re-read that row's stamp immediately before
// deleting: --unused acts on the stamp, so a version used since planning
// must survive, while --all is explicit and deletes regardless.
type Entry struct {
	Path    string
	Reason  string
	Size    int64
	Key     string
	Stamp   time.Time
	Recheck bool
}

// Plan is what a run would delete. Confirm reports whether it is about to
// delete an installed package version, worth asking the user about first.
type Plan struct {
	Entries []Entry
	Confirm bool
}

// Total is the number of bytes the plan would reclaim.
func (p Plan) Total() int64 {
	var n int64
	for _, e := range p.Entries {
		n += e.Size
	}
	return n
}

// Build decides what to reclaim. It reads no files and no clock, so every
// rule below is exercised by passing a Store and a now.
func Build(s Store, idx usage.Index, opts Options, now time.Time) Plan {
	var p Plan

	// Every tier 0 source is grace-filtered: a path touched inside the
	// window may belong to a build or install still in flight, and its
	// writer is not otherwise visible from here.
	for _, group := range []struct {
		items  []Item
		reason string
	}{
		{items: s.Staging, reason: "leaked staging"},
		{items: s.Downloads, reason: "leaked download"},
		{items: s.Partials, reason: "partial install"},
	} {
		for _, it := range group.items {
			if now.Sub(it.Newest) < opts.Grace {
				continue
			}
			p.Entries = append(p.Entries, Entry{
				Path:   it.Path,
				Reason: group.reason,
				Size:   it.Size,
			})
		}
	}

	switch {
	case opts.All:
		for _, v := range s.Versions {
			p.Entries = append(p.Entries, Entry{
				Path:   v.Path,
				Reason: "--all",
				Size:   v.Size,
				Key:    usage.Key(v.Name, v.Version),
			})
			p.Confirm = true
		}
	case opts.Unused > 0:
		for _, v := range s.Versions {
			last, ok := idx.LastUsed(v.Name, v.Version)
			if !ok {
				continue // no stamp is no evidence, and no evidence is no eviction
			}
			age := now.Sub(last)
			if age < opts.Unused {
				continue
			}
			p.Entries = append(p.Entries, Entry{
				Path:    v.Path,
				Reason:  unusedReason(age),
				Size:    v.Size,
				Key:     usage.Key(v.Name, v.Version),
				Stamp:   last,
				Recheck: true,
			})
			p.Confirm = true
		}
	}
	return p
}

// unusedReason renders an age the way a person reads it.
func unusedReason(age time.Duration) string {
	if age >= 24*time.Hour {
		return fmt.Sprintf("unused %dd", int(age.Hours())/24)
	}
	return fmt.Sprintf("unused %dh", int(age.Hours()))
}
