package report

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeClock returns a func() time.Time that starts at t0 and can be
// advanced by tests to control Task duration math deterministically.
func fakeClock(t0 time.Time) (now func() time.Time, advance func(time.Duration)) {
	cur := t0
	return func() time.Time { return cur }, func(d time.Duration) { cur = cur.Add(d) }
}

func TestTaskDoneAtOrOverOneSecondShowsSuffix(t *testing.T) {
	c, _, errb := newTest(Options{Color: ColorNever})
	now, advance := fakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	c.now = now

	task := c.Task("Installing go v1.26.5")
	advance(3 * time.Second)
	task.Done("Installed go v1.26.5")

	got := errb.String()
	if !strings.Contains(got, "OK Installed go v1.26.5 (3s)\n") {
		t.Errorf("done line missing duration suffix: %q", got)
	}
}

func TestTaskDoneUnderOneSecondHasNoSuffix(t *testing.T) {
	c, _, errb := newTest(Options{Color: ColorNever})
	now, advance := fakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	c.now = now

	task := c.Task("Installing go v1.26.5")
	advance(500 * time.Millisecond)
	task.Done("Installed go v1.26.5")

	got := errb.String()
	if !strings.Contains(got, "OK Installed go v1.26.5\n") {
		t.Errorf("done line wrong: %q", got)
	}
	if strings.Contains(got, "(0s)") || strings.Contains(got, "(") {
		t.Errorf("sub-second done must not show a duration suffix: %q", got)
	}
}

func TestTaskFailEmitsUnderQuiet(t *testing.T) {
	c, _, errb := newTest(Options{Quiet: true, Color: ColorNever})
	task := c.Task("Installing go v1.26.5")
	task.Fail("Install go v1.26.5 failed")

	if !strings.Contains(errb.String(), "ERROR Install go v1.26.5 failed\n") {
		t.Errorf("quiet suppressed a task failure: %q", errb.String())
	}
}

func TestTaskFailColoredShowsGlyph(t *testing.T) {
	c, _, errb := newTest(Options{Color: ColorAlways})
	task := c.Task("Installing go v1.26.5")
	task.Fail("Install go v1.26.5 failed")

	if !strings.Contains(errb.String(), "✗") {
		t.Errorf("expected unicode glyph in colored fail, got %q", errb.String())
	}
}

func TestTaskDoneSuppressedByQuiet(t *testing.T) {
	c, _, errb := newTest(Options{Quiet: true, Color: ColorNever})
	task := c.Task("Installing go v1.26.5")
	task.Done("Installed go v1.26.5")

	if errb.Len() != 0 {
		t.Errorf("quiet did not suppress task success: %q", errb.String())
	}
}

func TestTaskSecondDoneIsNoOp(t *testing.T) {
	c, _, errb := newTest(Options{Color: ColorNever})
	task := c.Task("Installing go v1.26.5")
	task.Done("Installed go v1.26.5")
	errb.Reset()

	task.Done("Installed go v1.26.5 again")

	if errb.Len() != 0 {
		t.Errorf("second Done call must be a no-op, got %q", errb.String())
	}
}

func TestTaskSecondFailIsNoOp(t *testing.T) {
	c, _, errb := newTest(Options{Color: ColorNever})
	task := c.Task("Installing go v1.26.5")
	task.Fail("Install go v1.26.5 failed")
	errb.Reset()

	task.Fail("Install go v1.26.5 failed again")

	if errb.Len() != 0 {
		t.Errorf("second Fail call must be a no-op, got %q", errb.String())
	}
}

func TestTaskDoneAfterFailIsNoOp(t *testing.T) {
	c, _, errb := newTest(Options{Color: ColorNever})
	task := c.Task("Installing go v1.26.5")
	task.Fail("Install go v1.26.5 failed")
	errb.Reset()

	task.Done("Installed go v1.26.5")

	if errb.Len() != 0 {
		t.Errorf("Done after Fail must be a no-op (cancel-races-success), got %q", errb.String())
	}
}

func TestDurSuffix(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{500 * time.Millisecond, ""},
		{3 * time.Second, " (3s)"},
		{83 * time.Second, " (1m23s)"},
	}
	for _, c := range cases {
		if got := DurSuffix(c.d); got != c.want {
			t.Errorf("DurSuffix(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

// TestTaskConcurrentUpdatesRaceClean hammers a single task's mutable state
// from many goroutines, then completes it; run with -race.
func TestTaskConcurrentUpdatesRaceClean(t *testing.T) {
	c, _, _ := newTest(Options{Color: ColorNever})
	task := c.Task("Installing go v1.26.5")

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			task.Status("downloading")
			task.Progress(int64(i), 100)
			task.Count(i, 10)
		}(i)
	}
	wg.Wait()
	task.Done("Installed go v1.26.5")
}
