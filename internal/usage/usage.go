// Package usage records when nem last observed each installed package
// version in use, so clean can tell a live version from an abandoned one.
package usage

import (
	"encoding/json"
	"os"
	"time"

	"github.com/vi-dev/nem-cli/internal/fsx"
	"github.com/vi-dev/nem-cli/internal/home"
)

// Debounce is how stale a stamp must be before a write is worth doing. The
// recording path runs on every directory change and, under bash, every
// prompt, so the common case must not touch the disk at all. It is
// exported so a reader of a stamp — clean's --unused planner — can pad
// its eviction window by exactly this error bound: a stamp can understate
// the real last-use time by up to Debounce, since a use arriving while the
// stamp is still fresh writes nothing.
const Debounce = time.Hour

// Index maps "<name>@<version>" to the last time nem resolved that version.
type Index map[string]time.Time

// Key is the index key for one installed version.
func Key(name, version string) string { return name + "@" + version }

// Load reads the index. A missing, unreadable, or corrupt file yields an
// empty Index and never an error: usage data is advisory, and a version with
// no stamp is simply never evicted.
func Load(h home.Home) Index {
	idx, err := Read(h)
	if err != nil {
		return Index{}
	}
	return idx
}

// Read reads the index like Load, but reports a read or parse failure
// instead of swallowing it. A caller that must not confuse "a stamp never
// existed" with "the index is unreadable right now" — clean's pre-delete
// recheck, for one — needs this distinction; every other caller wants
// Load's fail-open behavior.
func Read(h home.Home) (Index, error) {
	data, err := os.ReadFile(h.Usage())
	if err != nil {
		if os.IsNotExist(err) {
			return Index{}, nil
		}
		return Index{}, err
	}
	idx := Index{}
	if err := json.Unmarshal(data, &idx); err != nil {
		return Index{}, err
	}
	return idx, nil
}

// LastUsed reports the recorded time for one version.
func (i Index) LastUsed(name, version string) (time.Time, bool) {
	t, ok := i[Key(name, version)]
	return t, ok
}

// ByKey reports the recorded time for an already-built key.
func (i Index) ByKey(key string) (time.Time, bool) {
	t, ok := i[key]
	return t, ok
}

// Prune returns a copy holding only the keys still present on disk.
func (i Index) Prune(existing []string) Index {
	keep := make(map[string]bool, len(existing))
	for _, k := range existing {
		keep[k] = true
	}
	out := make(Index, len(existing))
	for k, v := range i {
		if keep[k] {
			out[k] = v
		}
	}
	return out
}

// Save writes the index atomically.
func Save(h home.Home, idx Index) error {
	data, err := json.Marshal(idx)
	if err != nil {
		return err
	}
	return fsx.WriteAtomic(h.Usage(), data, 0o644)
}

// Stamp records now for every key whose stamp is missing or older than the
// debounce window, writing at most one file. Every failure is dropped: this
// runs on the directory-change path, which must never break because the
// index is unwritable.
func Stamp(h home.Home, now time.Time, keys []string) {
	if len(keys) == 0 {
		return
	}
	idx := Load(h)
	changed := false
	for _, k := range keys {
		if last, ok := idx[k]; ok && now.Sub(last) < Debounce {
			continue
		}
		idx[k] = now
		changed = true
	}
	if !changed {
		return
	}
	_ = Save(h, idx)
}
