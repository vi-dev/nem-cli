package ocix

import (
	"fmt"
	"sync"
)

// progressCall records one ProgressFunc invocation.
type progressCall struct{ done, total int64 }

// progressRecorder collects every ProgressFunc call it receives, safe for
// the concurrent calls a bounded copy can make.
type progressRecorder struct {
	mu    sync.Mutex
	calls []progressCall
}

func (r *progressRecorder) record(done, total int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, progressCall{done, total})
}

func (r *progressRecorder) snapshot() []progressCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]progressCall(nil), r.calls...)
}

// fakeCatalogEntries builds n distinctly named, distinctly bodied fixture
// entries for PushFakeCatalogForTest.
func fakeCatalogEntries(n int) []FakeEntry {
	entries := make([]FakeEntry, n)
	for i := range entries {
		name := fmt.Sprintf("pkg%02d", i)
		entries[i] = FakeEntry{Name: name, YAML: []byte("name: " + name)}
	}
	return entries
}
