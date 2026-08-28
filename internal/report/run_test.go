package report

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// TestRunTaskSetsStatusBeforeFnSoCountRenders pins RunTask's core
// property: because it sets Status itself before calling fn, a count fed
// from inside fn renders immediately alongside it — the mistake a bare
// Count-without-Status call would otherwise invite (see
// TestLiveCountAloneRendersNothingUntilStatusIsSet) is structurally ruled
// out.
func TestRunTaskSetsStatusBeforeFnSoCountRenders(t *testing.T) {
	c, _, errb := newLiveTest(Options{IsTTY: true, Color: ColorNever})
	labels := TaskLabels{Run: "Pulling catalog", Status: "copying", Done: "Pulled catalog", Fail: "Pull failed"}

	err := RunTask(c, labels, func(count func(done, total int64)) error {
		count(3, 10)
		c.repaint()
		got := errb.String()
		if !strings.Contains(got, "Pulling catalog") || !strings.Contains(got, "copying 3/10") {
			t.Fatalf("live line missing label/status/count: %q", got)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	if got := errb.String(); !strings.Contains(got, "Pulled catalog") {
		t.Errorf("missing Done completion line: %q", got)
	}
}

func TestRunTaskFailReturnsErrUnchanged(t *testing.T) {
	c, _, errb := newTest(Options{Color: ColorNever})
	labels := TaskLabels{Run: "Pushing catalog", Status: "copying", Done: "Pushed catalog", Fail: "Push failed"}
	sentinel := errors.New("boom")

	err := RunTask(c, labels, func(func(done, total int64)) error { return sentinel })
	if !errors.Is(err, sentinel) {
		t.Fatalf("RunTask error = %v, want sentinel unchanged", err)
	}

	got := errb.String()
	if !strings.Contains(got, "Push failed") {
		t.Errorf("missing Fail line: %q", got)
	}
	if strings.Contains(got, "Pushed catalog") {
		t.Errorf("unexpected Done line on failure: %q", got)
	}
}

func TestIsCancellation(t *testing.T) {
	if !IsCancellation(context.Canceled) {
		t.Error("context.Canceled must be classified as cancellation")
	}
	if !IsCancellation(fmt.Errorf("wrap: %w", context.DeadlineExceeded)) {
		t.Error("wrapped context.DeadlineExceeded must be classified as cancellation")
	}
	if IsCancellation(errors.New("real failure")) {
		t.Error("an unrelated error must not be classified as cancellation")
	}
	if IsCancellation(nil) {
		t.Error("a nil error must not be classified as cancellation")
	}
}
