package report

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// newLiveTest builds a Console like newTest, but pins the redraw ticker
// to a long interval so the background goroutine never fires during a
// test — tests drive every repaint explicitly and deterministically.
func newLiveTest(opts Options) (*Console, *bytes.Buffer, *bytes.Buffer) {
	c, out, errb := newTest(opts)
	c.tick = time.Hour
	return c, out, errb
}

// stripANSI removes CSI escape sequences (\x1b[...<letter>) so tests can
// measure the visible width of a rendered line.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && !((s[j] >= 'A' && s[j] <= 'Z') || (s[j] >= 'a' && s[j] <= 'z')) {
				j++
			}
			i = j
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func TestLiveSingleTaskShowsLabelAndSegment(t *testing.T) {
	c, _, errb := newLiveTest(Options{IsTTY: true, Color: ColorNever})
	tk := c.Task("Installing go v1.26.5")
	tk.Status("downloading")
	c.repaint()

	got := errb.String()
	if !strings.Contains(got, "Installing go v1.26.5") || !strings.Contains(got, "downloading") {
		t.Errorf("live line missing label/segment: %q", got)
	}
	if !strings.Contains(got, "\x1b[2K") {
		t.Errorf("live line missing clear-line sequence: %q", got)
	}
	tk.Done("Installed go v1.26.5")
}

func TestLiveTwoTasksInStartOrder(t *testing.T) {
	c, _, errb := newLiveTest(Options{IsTTY: true, Color: ColorNever})
	first := c.Task("Installing go v1.26.5")
	first.Status("downloading")
	second := c.Task("Installing kubectl v1.34.1")
	second.Status("extracting")
	c.repaint()

	got := errb.String()
	goIdx := strings.Index(got, "Installing go v1.26.5")
	kubectlIdx := strings.Index(got, "Installing kubectl v1.34.1")
	if goIdx == -1 || kubectlIdx == -1 {
		t.Fatalf("expected both task lines, got %q", got)
	}
	if goIdx > kubectlIdx {
		t.Errorf("tasks not painted in start order: %q", got)
	}

	first.Done("Installed go v1.26.5")
	second.Done("Installed kubectl v1.34.1")
}

func TestLiveDonePrintsCompletionAboveAndRemovesLine(t *testing.T) {
	c, _, errb := newLiveTest(Options{IsTTY: true, Color: ColorAlways})
	first := c.Task("Installing go v1.26.5")
	first.Status("downloading")
	second := c.Task("Installing kubectl v1.34.1")
	second.Status("extracting")
	c.repaint()
	errb.Reset()

	first.Done("Installed go v1.26.5")

	got := errb.String()

	// The block had 2 lines, so the clear must be a single cursor-up-to-top
	// + erase-to-end-of-screen, not the old per-line clear-and-blank-newline
	// loop (which left the cursor at the block's BOTTOM, stranding N dead
	// blank rows on every completion).
	const wantClear = "\x1b[2A\x1b[J"
	clearIdx := strings.Index(got, wantClear)
	if clearIdx == -1 {
		t.Fatalf("expected cursor-up-to-top + erase sequence %q, got %q", wantClear, got)
	}
	if strings.Contains(got, "\x1b[2K\n\x1b[2K\n") {
		t.Errorf("cursor left mid-block by a per-line clear loop instead of a single erase: %q", got)
	}

	// After the clear: completion line, then the repaint of the remaining
	// task, in that order — "clear block, print completion, repaint
	// remaining".
	after := got[clearIdx+len(wantClear):]
	doneIdx := strings.Index(after, "✓")
	if doneIdx == -1 {
		t.Fatalf("completion line missing right after the clear sequence: %q", after)
	}
	remainIdx := strings.Index(after, "Installing kubectl v1.34.1")
	if remainIdx == -1 {
		t.Fatalf("remaining task line missing after Done: %q", after)
	}
	if doneIdx > remainIdx {
		t.Errorf("completion did not print above the repainted block: %q", after)
	}
	if strings.Contains(after[:remainIdx], "Installing go v1.26.5") {
		t.Errorf("finished task's line was not removed from the block: %q", after)
	}

	second.Done("Installed kubectl v1.34.1")
}

func TestLiveTruncatesAtWidth(t *testing.T) {
	c, _, errb := newLiveTest(Options{IsTTY: true, Color: ColorNever})
	c.width = func() int { return 20 }
	tk := c.Task("Installing go v1.26.5")
	tk.Status("downloading a very long segment name")
	c.repaint()

	got := errb.String()
	for _, line := range strings.Split(got, "\n") {
		plain := stripANSI(line)
		if n := len([]rune(plain)); n > 20 {
			t.Errorf("line exceeds width 20 (%d runes): %q", n, plain)
		}
	}
	tk.Done("Installed go v1.26.5")
}

func TestLiveAutoElapsedAfterTenSeconds(t *testing.T) {
	c, _, errb := newLiveTest(Options{IsTTY: true, Color: ColorNever})
	now, advance := fakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	c.now = now

	tk := c.Task("Building erlang v27.2")
	tk.Status("./make.bash")
	advance(11 * time.Second)
	c.repaint()

	got := errb.String()
	if !strings.Contains(got, "(11s)") {
		t.Errorf("expected auto-elapsed suffix after 11s of silence: %q", got)
	}
	tk.Done("Built erlang v27.2")
}

func TestLiveBlockAbsentWhenNotTTY(t *testing.T) {
	c, _, errb := newLiveTest(Options{IsTTY: false, Color: ColorNever})
	tk := c.Task("Installing go v1.26.5")
	tk.Status("downloading")
	c.repaint()

	if errb.Len() != 0 {
		t.Errorf("non-TTY console painted a live block: %q", errb.String())
	}
	tk.Done("Installed go v1.26.5")
}

func TestLiveBlockAbsentWhenQuiet(t *testing.T) {
	c, _, errb := newLiveTest(Options{IsTTY: true, Quiet: true, Color: ColorNever})
	tk := c.Task("Installing go v1.26.5")
	tk.Status("downloading")
	c.repaint()

	if strings.Contains(errb.String(), "downloading") {
		t.Errorf("quiet console painted a live block: %q", errb.String())
	}
	tk.Done("Installed go v1.26.5")
}
