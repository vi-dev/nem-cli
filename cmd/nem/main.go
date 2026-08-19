package main

import (
	"context"
	"errors"
	"fmt"
	"os"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	installInterruptWatcher(cancel)

	root := newRoot()
	err := root.ExecuteContext(ctx)
	cancel()
	if err == nil {
		return
	}

	var exitErr *ExitError
	if errors.As(err, &exitErr) {
		os.Exit(exitErr.Code)
	}
	if errors.Is(err, context.Canceled) {
		os.Exit(130)
	}
	if ranHook && console != nil {
		console.Error(err, hintFor(err))
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "Error:", err) //nolint:forbidigo // usage errors fail before the console exists
	os.Exit(2)                             // cobra usage error: flags/args never validated
}
