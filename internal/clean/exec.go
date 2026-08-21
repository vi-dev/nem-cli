package clean

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/vi-dev/nem-cli/internal/fsx"
	"github.com/vi-dev/nem-cli/internal/home"
	"github.com/vi-dev/nem-cli/internal/usage"
)

// Observer receives Execute's per-entry outcomes. The zero value is valid:
// nil callbacks are simply not invoked.
type Observer struct {
	Removing func(entry Entry)                // about to delete this entry
	Skipped  func(entry Entry, reason string) // left in place, and why
}

func (o Observer) removing(e Entry) {
	if o.Removing != nil {
		o.Removing(e)
	}
}

func (o Observer) skipped(e Entry, reason string) {
	if o.Skipped != nil {
		o.Skipped(e, reason)
	}
}

// Execute deletes what the plan chose and returns the bytes reclaimed. The
// removal loop itself takes no lock: each entry is verified immediately
// before it is removed — paths outside $NEM_HOME and symlinks are refused
// outright, and for a keyed entry the usage stamp is re-read fresh right
// before that entry's own removal — not once for the whole run — so a
// version used since planning is left alone even if the run has already
// been going for a while. Index bookkeeping afterward is locked; see
// pruneIndex. obs, if non-zero, is notified of each entry's outcome as it
// happens; an already-gone (ENOENT) entry notifies neither callback, since
// it was not skipped and there is nothing to remove.
func Execute(h home.Home, p Plan, obs Observer) (int64, error) {
	freed, deleted, unverifiable, err := removeEntries(h, p.Entries, obs)
	if len(deleted) > 0 && !unverifiable {
		pruneIndex(h, deleted)
	}
	return freed, err
}

// removeEntries deletes every planned entry and reports which usage keys
// are now free to prune. It stops at the first failure, leaving the keys
// deleted before it for the caller to prune. unverifiable reports whether
// a recheck-carrying entry's stamp could not be read at all: an index
// that came back empty because the read failed must not be mistaken for
// one with nothing left to prune, so the caller skips pruning entirely
// rather than saving that empty index over the real one.
func removeEntries(h home.Home, entries []Entry, obs Observer) (int64, map[string]bool, bool, error) {
	var freed int64
	deleted := map[string]bool{}
	unverifiable := false

	for _, e := range entries {
		if !withinRoot(h.Root(), e.Path) {
			obs.skipped(e, "outside NEM_HOME")
			continue
		}
		info, err := os.Lstat(e.Path)
		if os.IsNotExist(err) {
			if e.Key != "" {
				deleted[e.Key] = true
			}
			continue // already gone; that is the outcome we wanted
		}
		if err != nil {
			obs.skipped(e, err.Error())
			return freed, deleted, unverifiable, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			obs.skipped(e, "symlink")
			continue // never follow a link out of the store
		}
		if e.Recheck {
			idx, err := usage.Read(h)
			if err != nil {
				unverifiable = true
				obs.skipped(e, "usage index unreadable")
				continue // the stamp can't be verified; leave this entry alone
			}
			if last, ok := idx.ByKey(e.Key); ok && !last.Equal(e.Stamp) {
				obs.skipped(e, "used since planning")
				continue // used between planning and now
			}
		}
		obs.removing(e)
		if err := os.RemoveAll(e.Path); err != nil {
			return freed, deleted, unverifiable, err
		}
		freed += e.Size
		if e.Key != "" {
			deleted[e.Key] = true
		}
	}
	return freed, deleted, unverifiable, nil
}

// pruneIndex removes the index rows for keys this run deleted, leaving
// every other row in the index untouched. It holds the store lock for the
// load-delete-save sequence, which serializes concurrent nem clean runs
// and other store-mutating commands against each other. It does not
// exclude the lockless hot-path Stamp: a Stamp write racing this prune
// is the same last-write-wins trade-off Stamp already accepts, not
// something this lock closes.
func pruneIndex(h home.Home, deleted map[string]bool) {
	release, err := fsx.Lock(h.LockFile())
	if err != nil {
		return
	}
	defer release()

	idx := usage.Load(h)
	var surviving []string
	for k := range idx {
		if !deleted[k] {
			surviving = append(surviving, k)
		}
	}
	_ = usage.Save(h, idx.Prune(surviving))
}

// withinRoot reports whether path is inside root, so a malformed plan can
// never reach outside $NEM_HOME.
func withinRoot(root, path string) bool {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	return path != root && strings.HasPrefix(path, root+string(filepath.Separator))
}
