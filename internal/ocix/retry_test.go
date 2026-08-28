package ocix

import (
	"context"
	"errors"
	"testing"
)

func TestWithRetryRetriesUpToLimitThenReturnsLastError(t *testing.T) {
	var calls int
	err := withRetry(context.Background(), func(context.Context) error {
		calls++
		return errors.New("boom")
	})
	if calls != retryAttempts {
		t.Fatalf("calls = %d, want %d", calls, retryAttempts)
	}
	if err == nil || err.Error() != "boom" {
		t.Fatalf("err = %v, want boom", err)
	}
}

func TestWithRetrySucceedsAfterTransientFailures(t *testing.T) {
	var calls int
	err := withRetry(context.Background(), func(context.Context) error {
		calls++
		if calls < retryAttempts {
			return errors.New("transient")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if calls != retryAttempts {
		t.Fatalf("calls = %d, want %d", calls, retryAttempts)
	}
}

func TestWithRetryAbortsImmediatelyOnCancellationDuringAttempt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var calls int
	err := withRetry(ctx, func(ctx context.Context) error {
		calls++
		cancel()
		return ctx.Err()
	})
	if calls != 1 {
		t.Fatalf("calls = %d, want 1: cancellation must never be retried", calls)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestWithRetryDoesNotCallFnAgainOncePreCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var calls int
	err := withRetry(ctx, func(context.Context) error {
		calls++
		return errors.New("would be retried if this counted")
	})
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
