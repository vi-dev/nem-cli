package report

import (
	"fmt"
	"sync"
	"time"
)

// Reporter is the narration + task-reporting interface domain packages
// render through; nothing else writes to the terminal.
type Reporter interface {
	Info(format string, a ...any)
	Warn(format string, a ...any)
	Debug(format string, a ...any)
	Task(label string) Task
}

// Task is a handle to one named unit of work. Status/Progress/Count only
// update internal state; Done/Fail are the only methods that render
// anything outside a live TTY block.
type Task interface {
	Status(segment string)      // current subtask: "downloading"
	Progress(done, total int64) // → "34%"; total<0 → "12 MB"
	Count(done, total int)      // → "12/366"
	Done(outcome string)        // "✓ <outcome> (9s)" — duration auto ≥1s
	Fail(outcome string)        // "✗ <outcome>" — always emits (diagnostic)
}

var _ Reporter = (*Console)(nil)

// task is Console's Task implementation. Status/Progress/Count/Done/Fail
// may be called concurrently; mu guards all mutable state, including the
// completed flag that makes a second Done or Fail call a no-op.
type task struct {
	console *Console
	label   string
	start   time.Time

	mu           sync.Mutex
	segment      string
	segmentStart time.Time
	done         int64
	total        int64
	cdone        int
	ctotal       int
	completed    bool
}

// Task starts a new task bound to c, recording the current time as its
// start. When c's live TTY block is active, the task is added to it in
// start order.
func (c *Console) Task(label string) Task {
	t := &task{console: c, label: label, start: c.now()}
	if c.liveActive() {
		c.registerLiveTask(t)
	}
	return t
}

func (t *task) Status(segment string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.segment = segment
	t.segmentStart = t.console.now()
}

func (t *task) Progress(done, total int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.done, t.total = done, total
}

func (t *task) Count(done, total int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cdone, t.ctotal = done, total
}

// Done renders the completion line as narration (suppressed by Quiet),
// with a duration suffix once the task has run at least a second. A
// second call (racing a Fail from a cancellation path) is a no-op.
func (t *task) Done(outcome string) {
	if !t.markCompleted() {
		return
	}
	elapsed := t.console.now().Sub(t.start)
	t.console.completeTask(t, func() {
		t.console.Success("%s%s", outcome, DurSuffix(elapsed))
	})
}

// Fail renders the failure line unconditionally, like the console's other
// diagnostics — errors themselves still travel the return path and are
// rendered once by report.Console.Error. A second call (racing a Done
// from a success path) is a no-op.
func (t *task) Fail(outcome string) {
	if !t.markCompleted() {
		return
	}
	t.console.completeTask(t, func() {
		c := t.console
		if c.colored {
			fmt.Fprintf(c.err, "%s✗ %s%s\n", ansiRed, outcome, ansiReset)
		} else {
			fmt.Fprintf(c.err, "ERROR %s\n", outcome)
		}
	})
}

// markCompleted flips t.completed and reports whether this call was the
// first — guarding Done/Fail against a second call.
func (t *task) markCompleted() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.completed {
		return false
	}
	t.completed = true
	return true
}

// DurSuffix renders the output convention's duration suffix: empty under
// 1s, else e.g. " (3s)" or " (1m23s)".
func DurSuffix(d time.Duration) string {
	if d < time.Second {
		return ""
	}
	d = d.Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf(" (%ds)", int(d.Seconds()))
	}
	return fmt.Sprintf(" (%dm%02ds)", int(d.Minutes()), int(d.Seconds())%60)
}
