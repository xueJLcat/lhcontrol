package main

import (
	"context"
	"testing"
	"time"
)

func TestShutdownBoundsStuckAPIServerLoop(t *testing.T) {
	app := NewApp()
	app.api = nil
	app.apiShutdownWait = 25 * time.Millisecond
	app.apiWG.Add(1)

	done := make(chan struct{})
	go func() {
		app.shutdown(context.Background())
		close(done)
	}()

	select {
	case <-done:
		app.apiWG.Done()
	case <-time.After(500 * time.Millisecond):
		// Release the simulated listener before failing so the shutdown goroutine
		// and the waiter's goroutine cannot leak into later tests.
		app.apiWG.Done()
		<-done
		t.Fatal("shutdown remained blocked on the API listener after its timeout")
	}
}

// TestShutdownRunsOnce guards the idempotence fix: Wails may invoke OnShutdown
// and still return a run error, in which case the fatal path calls shutdown a
// second time. Only the first call may run the bounded waits; the second must
// return immediately instead of re-draining already-joined goroutines.
func TestShutdownRunsOnce(t *testing.T) {
	app := NewApp()
	app.api = nil
	app.apiShutdownWait = 25 * time.Millisecond
	app.autoSleepStopWait = time.Millisecond

	first := make(chan struct{})
	go func() {
		app.shutdown(context.Background())
		close(first)
	}()
	<-first

	// A second invocation must be a no-op that returns promptly. If the
	// sequence ran again it would re-arm and re-wait on the auto-sleep and
	// API drains.
	start := time.Now()
	app.shutdown(context.Background())
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("second shutdown re-ran the bounded sequence, took %v", elapsed)
	}
	if !app.shuttingDown.Load() {
		t.Fatal("shuttingDown flag was not set by shutdown")
	}
}
