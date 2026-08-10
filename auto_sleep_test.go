package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"lhcontrol/internal/station"
)

func TestStopAutoSleepBoundedWhenTheRunningActionIsStuck(t *testing.T) {
	app := NewApp()
	app.autoSleepStopWait = 30 * time.Millisecond
	app.autoSleepCancel = func() {}
	// Simulate a sleep action stuck in an adapter call that ignores
	// cancellation; the shutdown join must give up after the limit instead of
	// keeping the window open.
	app.autoSleepWG.Add(1)

	start := time.Now()
	app.stopAutoSleep()
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("stopAutoSleep() took %v, want the bounded wait", elapsed)
	}
	if app.autoSleepCancel != nil {
		t.Fatal("stopAutoSleep() did not clear the watcher cancel")
	}
	app.autoSleepWG.Done() // release the stranded wait goroutine
}

func TestRunAutoSleepEmitsTerminalCancellationBetweenScanAndPower(t *testing.T) {
	app := NewApp()
	ctx, cancel := context.WithCancel(context.Background())
	var events []autoSleepEvent
	app.autoSleepEventSink = func(event autoSleepEvent) {
		events = append(events, event)
	}
	app.scanForAutoSleep = func(context.Context) ([]station.StationInfo, error) {
		cancel()
		return nil, nil
	}
	app.setPowerForAutoSleep = func(context.Context, string) (station.BulkPowerResult, error) {
		t.Fatal("bulk power started after automatic sleep was cancelled")
		return station.BulkPowerResult{}, nil
	}

	app.runAutoSleep(ctx)

	if len(events) != 2 || events[0].Phase != "started" || events[1].Phase != "cancelled" {
		t.Fatalf("auto-sleep events = %+v, want started then cancelled", events)
	}
	if events[0].ID == 0 || events[1].ID != events[0].ID {
		t.Fatalf("auto-sleep lifecycle IDs = %d/%d, want one non-zero action ID", events[0].ID, events[1].ID)
	}
	if events[1].Error != "cancelled after scanning and before power commands were sent" {
		t.Fatalf("cancellation reason = %q", events[1].Error)
	}
}

func TestRunAutoSleepUsesDistinctLifecycleIDs(t *testing.T) {
	app := NewApp()
	var events []autoSleepEvent
	app.autoSleepEventSink = func(event autoSleepEvent) {
		events = append(events, event)
	}
	app.scanForAutoSleep = func(context.Context) ([]station.StationInfo, error) {
		return nil, nil
	}
	app.setPowerForAutoSleep = func(context.Context, string) (station.BulkPowerResult, error) {
		return station.BulkPowerResult{}, nil
	}

	app.runAutoSleep(context.Background())
	app.runAutoSleep(context.Background())

	if len(events) != 4 {
		t.Fatalf("auto-sleep event count = %d, want two lifecycle pairs", len(events))
	}
	if events[0].ID == 0 || events[0].ID != events[1].ID {
		t.Fatalf("first lifecycle IDs = %d/%d", events[0].ID, events[1].ID)
	}
	if events[2].ID <= events[0].ID || events[2].ID != events[3].ID {
		t.Fatalf("second lifecycle IDs = %d/%d after %d", events[2].ID, events[3].ID, events[0].ID)
	}
}

func TestRunAutoSleepReplacementWaitsForCancelledSessionToDrain(t *testing.T) {
	app := NewApp()
	closedAt := time.Now()
	firstStarted := make(chan struct{})
	secondStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := make(chan struct{})
	secondDone := make(chan struct{})
	var scanCalls atomic.Int32
	var powerCalls atomic.Int32
	app.scanForAutoSleep = func(ctx context.Context) ([]station.StationInfo, error) {
		switch scanCalls.Add(1) {
		case 1:
			close(firstStarted)
			<-releaseFirst
			return nil, ctx.Err()
		case 2:
			close(secondStarted)
			return nil, nil
		default:
			return nil, nil
		}
	}
	app.setPowerForAutoSleep = func(context.Context, string) (station.BulkPowerResult, error) {
		powerCalls.Add(1)
		return station.BulkPowerResult{}, nil
	}

	firstCtx, cancelFirst := context.WithCancel(context.Background())
	go func() {
		defer close(firstDone)
		app.runAutoSleepSession(firstCtx, closedAt)
	}()
	select {
	case <-firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first automatic-sleep action did not start")
	}

	cancelFirst()
	go func() {
		defer close(secondDone)
		app.runAutoSleepSession(context.Background(), closedAt)
	}()
	select {
	case <-secondStarted:
		t.Error("replacement automatic-sleep scan overlapped the draining action")
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseFirst)
	for name, done := range map[string]<-chan struct{}{"cancelled action": firstDone, "replacement action": secondDone} {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatalf("%s did not finish", name)
		}
	}

	if got := scanCalls.Load(); got != 2 {
		t.Fatalf("scan calls = %d, want cancelled action plus replacement", got)
	}
	if got := powerCalls.Load(); got != 1 {
		t.Fatalf("power calls = %d, want only the replacement action", got)
	}
}

func TestRunAutoSleepReplacementDeduplicatesSettledSession(t *testing.T) {
	app := NewApp()
	closedAt := time.Now()
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := make(chan struct{})
	replacementDone := make(chan struct{})
	var scanCalls atomic.Int32
	var powerCalls atomic.Int32
	app.scanForAutoSleep = func(context.Context) ([]station.StationInfo, error) {
		if scanCalls.Add(1) == 1 {
			close(firstStarted)
			<-releaseFirst
		}
		return nil, nil
	}
	app.setPowerForAutoSleep = func(context.Context, string) (station.BulkPowerResult, error) {
		powerCalls.Add(1)
		return station.BulkPowerResult{}, nil
	}

	go func() {
		defer close(firstDone)
		app.runAutoSleepSession(context.Background(), closedAt)
	}()
	select {
	case <-firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first automatic-sleep action did not start")
	}
	go func() {
		defer close(replacementDone)
		app.runAutoSleepSession(context.Background(), closedAt)
	}()
	close(releaseFirst)
	for name, done := range map[string]<-chan struct{}{"first action": firstDone, "replacement action": replacementDone} {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatalf("%s did not finish", name)
		}
	}

	if got := scanCalls.Load(); got != 1 {
		t.Fatalf("scan calls = %d, want one lifecycle for the shared process session", got)
	}
	if got := powerCalls.Load(); got != 1 {
		t.Fatalf("power calls = %d, want one lifecycle for the shared process session", got)
	}
}

func TestRunAutoSleepIsVisibleInOperationHealthUntilItFinishes(t *testing.T) {
	app := NewApp()
	scanStarted := make(chan struct{})
	releaseScan := make(chan struct{})
	actionDone := make(chan struct{})
	app.scanForAutoSleep = func(context.Context) ([]station.StationInfo, error) {
		close(scanStarted)
		<-releaseScan
		return nil, nil
	}
	app.setPowerForAutoSleep = func(context.Context, string) (station.BulkPowerResult, error) {
		return station.BulkPowerResult{}, nil
	}

	go func() {
		defer close(actionDone)
		app.runAutoSleep(context.Background())
	}()
	select {
	case <-scanStarted:
	case <-time.After(time.Second):
		t.Fatal("automatic sleep scan did not start")
	}

	status := app.GetAPIStatus()
	if status.OperationRevision != 1 || len(status.ActiveOperations) != 1 ||
		status.ActiveOperations[0].Kind != "auto-sleep" {
		t.Fatalf("active operation status = %+v", status)
	}

	close(releaseScan)
	select {
	case <-actionDone:
	case <-time.After(time.Second):
		t.Fatal("automatic sleep action did not finish")
	}
	status = app.GetAPIStatus()
	if status.OperationRevision != 2 || len(status.ActiveOperations) != 0 {
		t.Fatalf("finished operation status = %+v", status)
	}
}

func TestRunAutoSleepPreservesPartialResultsWhenBulkPowerTimesOut(t *testing.T) {
	app := NewApp()
	var events []autoSleepEvent
	app.autoSleepEventSink = func(event autoSleepEvent) {
		events = append(events, event)
	}
	app.scanForAutoSleep = func(context.Context) ([]station.StationInfo, error) {
		return nil, nil
	}
	app.setPowerForAutoSleep = func(context.Context, string) (station.BulkPowerResult, error) {
		return station.BulkPowerResult{
			TimedOut: true,
			Results: []station.BulkPowerStationResult{
				{Success: true, Confirmed: true},
				{Success: true, CommandSent: true},
				{Error: "connection failed"},
				{Skipped: true, Reason: "bulk operation timed out"},
			},
		}, context.DeadlineExceeded
	}

	app.runAutoSleep(context.Background())

	if len(events) != 2 || events[0].Phase != "started" || events[1].Phase != "timed-out" {
		t.Fatalf("auto-sleep events = %+v, want started then timed-out", events)
	}
	event := events[1]
	if !event.TimedOut || event.Success != 1 || event.Unconfirmed != 1 || event.Failed != 1 || event.TimedOutSkipped != 1 || event.Skipped != 0 {
		t.Fatalf("timed-out event = %+v", event)
	}
}
