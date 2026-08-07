package report

import (
	"fmt"
	"time"
)

const (
	// liveTickInterval is the live block's default redraw cadence.
	liveTickInterval = 100 * time.Millisecond

	// autoElapsedAfter is how long a segment must go without a progress
	// report before its line grows an elapsed-time suffix.
	autoElapsedAfter = 10 * time.Second

	// defaultWidth is the line-truncation width used when the terminal
	// width can't be determined.
	defaultWidth = 80
)

// liveActive reports whether the TTY live block is in effect: a real
// terminal and narration isn't suppressed.
func (c *Console) liveActive() bool {
	return c.opts.IsTTY && !c.opts.Quiet
}

// registerLiveTask adds t to the live block in start order, painting it
// immediately and starting the redraw ticker if it's the first active
// task.
func (c *Console) registerLiveTask(t *task) {
	c.liveMu.Lock()
	defer c.liveMu.Unlock()
	c.liveTasks = append(c.liveTasks, t)
	if len(c.liveTasks) == 1 {
		c.startLiveTickerLocked()
	}
	c.repaintLocked()
}

// completeTask finalizes t: if it's part of the live block, the block is
// cleared, printCompletion renders the completion line in its place, and
// the remaining tasks are repainted below it. Otherwise printCompletion
// just runs directly, matching non-TTY behavior.
func (c *Console) completeTask(t *task, printCompletion func()) {
	c.liveMu.Lock()
	defer c.liveMu.Unlock()

	idx := -1
	for i, lt := range c.liveTasks {
		if lt == t {
			idx = i
			break
		}
	}
	if idx == -1 {
		printCompletion()
		return
	}

	c.clearBlockLocked()
	printCompletion()
	c.liveTasks = append(c.liveTasks[:idx], c.liveTasks[idx+1:]...)
	if len(c.liveTasks) == 0 {
		c.stopLiveTickerLocked()
	}
	c.repaintLocked()
}

// repaint redraws the live block in place: clear the previously painted
// lines, then write one line per active task in start order.
func (c *Console) repaint() {
	c.liveMu.Lock()
	defer c.liveMu.Unlock()
	c.repaintLocked()
}

func (c *Console) repaintLocked() {
	if c.liveLines > 0 {
		fmt.Fprintf(c.err, "\x1b[%dA", c.liveLines)
	}
	now := c.now()
	width := c.width()
	for _, t := range c.liveTasks {
		fmt.Fprintf(c.err, "\x1b[2K%s\n", t.renderLine(now, width, c.colored))
	}
	c.liveLines = len(c.liveTasks)
}

// clearBlockLocked erases the currently painted block and leaves the
// cursor exactly where it was before the block existed, ready for fresh
// content (a completion line, a repaint, or both).
func (c *Console) clearBlockLocked() {
	if c.liveLines == 0 {
		return
	}
	fmt.Fprintf(c.err, "\x1b[%dA\x1b[J", c.liveLines)
	c.liveLines = 0
}

func (c *Console) startLiveTickerLocked() {
	stop := make(chan struct{})
	c.liveStop = stop
	go func() {
		ticker := time.NewTicker(c.tick)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				c.repaint()
			case <-stop:
				return
			}
		}
	}()
}

func (c *Console) stopLiveTickerLocked() {
	if c.liveStop != nil {
		close(c.liveStop)
		c.liveStop = nil
	}
}

// renderLine builds t's live-block line: the label, then (if a segment is
// active) two spaces and the dimmed segment with its progress or
// auto-elapsed suffix, truncated to width.
func (t *task) renderLine(now time.Time, width int, colored bool) string {
	return formatTaskLine(t.label, t.trailingText(now), width, colored)
}

// trailingText renders t's segment plus whatever progress form applies:
// a percentage, a byte count, an item count, or — once the segment has
// gone 10s without a progress report — an elapsed-time suffix.
func (t *task) trailingText(now time.Time) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.segment == "" {
		return ""
	}
	if p := progressText(t.done, t.total, t.cdone, t.ctotal); p != "" {
		return t.segment + " " + p
	}
	if elapsed := now.Sub(t.segmentStart); elapsed >= autoElapsedAfter {
		return t.segment + DurSuffix(elapsed)
	}
	return t.segment
}

// progressText renders a task's current progress: item counts take
// priority over byte/percent progress since a task reports only one kind.
func progressText(done, total int64, cdone, ctotal int) string {
	switch {
	case ctotal > 0:
		return fmt.Sprintf("%d/%d", cdone, ctotal)
	case total > 0:
		return fmt.Sprintf("%d%%", int(float64(done)/float64(total)*100))
	case total < 0:
		return formatBytes(done)
	default:
		return ""
	}
}

// formatBytes renders n bytes as a short human count, e.g. "12 MB".
func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.0f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

// formatTaskLine joins label and trailing with the two-space gutter,
// truncates to width, and — when colored — wraps whatever of trailing
// survived truncation in the dim role color.
func formatTaskLine(label, trailing string, width int, colored bool) string {
	plain := label
	if trailing != "" {
		plain += "  " + trailing
	}
	truncated := truncateToWidth(plain, width)
	if !colored || trailing == "" {
		return truncated
	}
	labelLen := len([]rune(label))
	truncRunes := []rune(truncated)
	prefixLen := labelLen + 2
	if prefixLen >= len(truncRunes) {
		return truncated
	}
	return string(truncRunes[:prefixLen]) + ansiDim + string(truncRunes[prefixLen:]) + ansiReset
}

// truncateToWidth cuts s to at most width runes; width<=0 falls back to
// defaultWidth.
func truncateToWidth(s string, width int) string {
	if width <= 0 {
		width = defaultWidth
	}
	r := []rune(s)
	if len(r) <= width {
		return s
	}
	return string(r[:width])
}
