package main

import (
	"os"
	"os/signal"
)

// watchInterrupts implements the two-stage SIGINT contract: the first
// value received from sig calls cancel, letting the running command wind
// down through its context; the second calls exit(130), the conventional
// SIGINT exit code, for a user who wants out immediately. It returns once
// exit has been called, or as soon as sig is closed without a second
// signal arriving.
func watchInterrupts(sig <-chan os.Signal, cancel func(), exit func(int)) {
	if _, ok := <-sig; !ok {
		return
	}
	cancel()
	if _, ok := <-sig; !ok {
		return
	}
	exit(130)
}

// installInterruptWatcher registers os.Interrupt with the OS and runs
// watchInterrupts against it in its own goroutine, calling cancel on the
// first SIGINT and terminating the process on the second.
func installInterruptWatcher(cancel func()) {
	sig := make(chan os.Signal, 2)
	signal.Notify(sig, os.Interrupt)
	go watchInterrupts(sig, cancel, os.Exit)
}
