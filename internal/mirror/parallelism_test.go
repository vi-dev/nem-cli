package mirror

import (
	"context"
	"fmt"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/memory"
)

// inFlightGauge tracks concurrently in-flight units of work and the peak
// concurrency observed, so a test can distinguish genuine parallel work
// from goroutines that were merely created together but then serialize on
// a lock: output order alone can't tell the two apart, this can.
type inFlightGauge struct {
	cur atomic.Int64
	max atomic.Int64
}

func (g *inFlightGauge) start() {
	n := g.cur.Add(1)
	for {
		m := g.max.Load()
		if n <= m || g.max.CompareAndSwap(m, n) {
			return
		}
	}
}

func (g *inFlightGauge) end() { g.cur.Add(-1) }

// wireArchivesWithSrcDelay wires fresh per-package archive fixtures like
// wireArchives, except every src open sleeps delay and marks itself
// in-flight on gauge first. mirrorPackage opens src archives exactly once
// per package regardless of that package's version outcomes, so this
// stands in for "per-package work" without needing a real registry.
func wireArchivesWithSrcDelay(t *testing.T, delay time.Duration, gauge *inFlightGauge) {
	t.Helper()
	src := newArchiveFixtures()
	dst := newArchiveFixtures()
	t.Cleanup(SetSrcArchivesOpener(func(_, name string) (oras.ReadOnlyTarget, error) {
		gauge.start()
		time.Sleep(delay)
		gauge.end()
		return src.open(name), nil
	}))
	t.Cleanup(SetDstArchivesOpener(func(_, name string) (oras.Target, error) {
		return dst.open(name), nil
	}))
}

// TestRunPackagesFanOutInParallel proves mirror.Run's per-package errgroup
// dispatch is genuine parallelism, not goroutines that serialize on some
// lock once running: with a fixed per-package delay standing in for real
// per-package work, wall clock lands in the parallel-width range (not the
// fully-serialized range), and a peak-concurrency gauge independently
// confirms multiple packages were doing their delay simultaneously, capped
// exactly at SetLimit's bound.
func TestRunPackagesFanOutInParallel(t *testing.T) {
	limit := min(runtime.NumCPU(), 8)
	if limit < 2 {
		t.Skip("needs at least two concurrent slots to distinguish parallel from serial")
	}

	const n = 32
	const delay = 100 * time.Millisecond

	pkgs := map[string]string{}
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("pkg%02d", i)
		pkgs[name] = urlPkg(name, "1.0.0", "deadbeef")
	}
	srcStore, srcTag := newCatalog(t, pkgs)
	wireCatalog(t, srcStore, srcTag, memory.New(), "v2")

	var gauge inFlightGauge
	wireArchivesWithSrcDelay(t, delay, &gauge)

	opts := Options{SrcRef: "example.com/cat:v2", DstRef: "internal.example.com/cat:v2"}
	start := time.Now()
	summary, err := Run(context.Background(), opts, &fakeReporter{})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if summary.Packages != n {
		t.Fatalf("Packages = %d, want %d", summary.Packages, n)
	}

	serialFloor := n * delay
	// A generous ceiling above the ideal n/limit*delay: real scheduling
	// jitter shouldn't fail this, only actual serialization should.
	parallelCeiling := time.Duration(n/limit+2) * delay
	if elapsed >= serialFloor {
		t.Fatalf("elapsed %s is not faster than the fully-serialized floor %s: no parallelism", elapsed, serialFloor)
	}
	if elapsed > parallelCeiling {
		t.Fatalf("elapsed %s exceeds the parallel-%d ceiling %s", elapsed, limit, parallelCeiling)
	}

	maxInFlight := int(gauge.max.Load())
	if maxInFlight > limit {
		t.Fatalf("max in-flight = %d, exceeds SetLimit's bound %d", maxInFlight, limit)
	}
	if maxInFlight < limit {
		t.Fatalf("max in-flight = %d, want exactly the SetLimit bound %d (n=%d is well above it)", maxInFlight, limit, n)
	}

	t.Logf("n=%d delay=%s limit=%d elapsed=%s serialFloor=%s parallelCeiling=%s maxInFlight=%d",
		n, delay, limit, elapsed, serialFloor, parallelCeiling, maxInFlight)
}

// TestRunTasksRegisterBeforeProbingCompletes proves the point of eager
// task creation directly: a package's task is created (status "probing")
// the moment a worker picks it up, not once probing has already decided
// there's work to do. With every package's probing delayed, up to the
// parallel bound's worth of package tasks are registered concurrently —
// none of them Done, Failed, or Discarded yet — while those probes are
// still in flight. Before this fix, a task only existed once probing had
// already found a copy or a failure, so this same in-flight window showed
// no task lines at all.
func TestRunTasksRegisterBeforeProbingCompletes(t *testing.T) {
	limit := min(runtime.NumCPU(), 8)
	if limit < 2 {
		t.Skip("needs at least two concurrent slots to observe concurrent registration")
	}

	const n = 32
	const delay = 200 * time.Millisecond

	pkgs := map[string]string{}
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("pkg%02d", i)
		pkgs[name] = urlPkg(name, "1.0.0", "deadbeef")
	}
	srcStore, srcTag := newCatalog(t, pkgs)
	wireCatalog(t, srcStore, srcTag, memory.New(), "v2")

	var gauge inFlightGauge
	wireArchivesWithSrcDelay(t, delay, &gauge)

	rep := &fakeReporter{}
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		_, _ = Run(context.Background(), Options{SrcRef: "example.com/cat:v2", DstRef: "internal.example.com/cat:v2"}, rep)
	}()

	fanOutDeadline := time.After(5 * time.Second)
waitForFullFanOut:
	for {
		select {
		case <-fanOutDeadline:
			t.Fatalf("in-flight probes never reached the parallel bound %d (stuck at %d)", limit, gauge.cur.Load())
		case <-runDone:
			t.Fatal("Run finished before any probe delay elapsed; delay wiring is broken")
		default:
			if int(gauge.cur.Load()) == limit {
				break waitForFullFanOut
			}
			time.Sleep(time.Millisecond)
		}
	}

	// Registration happens at pickup, independent of whether -- or how
	// long -- probing takes: every in-flight probe's package task already
	// exists, and none of them has resolved.
	tasks := rep.snapshotTasks()
	if len(tasks) < limit {
		t.Fatalf("task count = %d while %d probes are in flight, want at least %d", len(tasks), limit, limit)
	}
	for _, task := range tasks {
		if task.label == "Pulling catalog" || task.label == "Pushing catalog" {
			continue
		}
		if done, failed, _ := task.snapshot(); done || failed {
			t.Fatalf("task %q resolved via Done/Fail while probes were still in flight", task.label)
		}
		if task.wasDiscarded() {
			t.Fatalf("task %q discarded while probes were still in flight", task.label)
		}
	}

	<-runDone
}
