package main

import (
	"os"
	"testing"
	"time"
)

func TestWatchInterruptsCancelsOnFirstSignalExitsOnSecond(t *testing.T) {
	sig := make(chan os.Signal, 2)
	cancelled := make(chan struct{})
	cancel := func() { close(cancelled) }
	exited := make(chan int, 1)
	exit := func(code int) { exited <- code }

	done := make(chan struct{})
	go func() {
		watchInterrupts(sig, cancel, exit)
		close(done)
	}()

	sig <- os.Interrupt

	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("cancel was not called after the first signal")
	}

	select {
	case code := <-exited:
		t.Fatalf("exit called after only one signal, code %d", code)
	case <-time.After(20 * time.Millisecond):
	}

	sig <- os.Interrupt

	select {
	case code := <-exited:
		if code != 130 {
			t.Fatalf("exit code = %d, want 130", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("exit was not called after the second signal")
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("watchInterrupts did not return after exit")
	}
}

func TestWatchInterruptsReturnsWhenChannelClosedBeforeFirstSignal(t *testing.T) {
	sig := make(chan os.Signal)
	cancelCalled := false
	exitCalled := false

	done := make(chan struct{})
	go func() {
		watchInterrupts(sig, func() { cancelCalled = true }, func(int) { exitCalled = true })
		close(done)
	}()

	close(sig)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("watchInterrupts did not return when sig closed before any signal")
	}
	if cancelCalled || exitCalled {
		t.Fatalf("cancel/exit called with no signal ever sent: cancel=%v exit=%v", cancelCalled, exitCalled)
	}
}

func TestWatchInterruptsReturnsWhenChannelClosedBetweenSignals(t *testing.T) {
	sig := make(chan os.Signal, 1)
	cancelled := make(chan struct{})
	cancel := func() { close(cancelled) }
	exitCalled := false

	done := make(chan struct{})
	go func() {
		watchInterrupts(sig, cancel, func(int) { exitCalled = true })
		close(done)
	}()

	sig <- os.Interrupt
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("cancel was not called after the first signal")
	}

	close(sig)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("watchInterrupts did not return when sig closed after cancel")
	}
	if exitCalled {
		t.Fatal("exit called despite sig closing instead of a second signal")
	}
}
