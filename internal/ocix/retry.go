package ocix

import (
	"context"
	"time"
)

const (
	retryAttempts = 3
	retryBackoff  = 200 * time.Millisecond
)

// withRetry retries fn up to retryAttempts times; a ctx cancellation
// aborts immediately and is never itself retried.
func withRetry(ctx context.Context, fn func(ctx context.Context) error) error {
	var err error
	for attempt := 1; attempt <= retryAttempts; attempt++ {
		if err = fn(ctx); err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if attempt == retryAttempts {
			break
		}
		select {
		case <-time.After(retryBackoff):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return err
}
