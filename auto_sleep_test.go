package main

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"lhcontrol/internal/autosleep"
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

func TestAutoSleepAndHTTPSnapshotsShareUpdateSequencer(t *testing.T) {
	app := NewApp()
	httpSnapshotStarted := make(chan struct{})
	releaseHTTPSnapshot := make(chan struct{})
	httpEvents := make(chan stationUpdateEvent, 1)
	autoSleepEvents := make(chan autoSleepEvent, 1)
	app.autoSleepEventSink = func(event autoSleepEvent) { autoSleepEvents <- event }

	go emitStationUpdate(scanEventCallbacks{
		snapshotUpdate: app.snapshotExternalStationUpdate,
		updated:        func(event stationUpdateEvent) { httpEvents <- event },
	}, "http-power", func() []station.StationInfo {
		close(httpSnapshotStarted)
		<-releaseHTTPSnapshot
		return []station.StationInfo{{Address: "AA"}}
	})
	select {
	case <-httpSnapshotStarted:
	case <-time.After(time.Second):
		t.Fatal("HTTP station snapshot did not start")
	}

	go app.emitTerminalAutoSleep(autoSleepEvent{Phase: "completed"})
	autoSleepFinishedEarly := false
	select {
	case <-autoSleepEvents:
		autoSleepFinishedEarly = true
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseHTTPSnapshot)

	var httpEvent stationUpdateEvent
	select {
	case httpEvent = <-httpEvents:
	case <-time.After(time.Second):
		t.Fatal("HTTP station update did not finish")
	}
	var autoEvent autoSleepEvent
	if !autoSleepFinishedEarly {
		select {
		case autoEvent = <-autoSleepEvents:
		case <-time.After(time.Second):
			t.Fatal("automatic-sleep station update did not finish")
		}
	}
	if autoSleepFinishedEarly {
		t.Fatal("automatic-sleep snapshot bypassed the in-flight HTTP snapshot transaction")
	}
	if httpEvent.ID != 1 || autoEvent.UpdateID != 2 {
		t.Fatalf("shared update IDs = HTTP %d, auto-sleep %d; want 1 then 2", httpEvent.ID, autoEvent.UpdateID)
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

// selfStoppingWatcher returns a Watcher that stops itself after repeated
// process-check failures, carrying an unsettled sleep debt seeded beforehand.
func selfStoppingWatcher() *autosleep.Watcher {
	watcher := &autosleep.Watcher{
		Settings: autosleep.Settings{Enabled: true, Target: string(autosleep.TargetSteamVR), DelaySeconds: autosleep.MinDelaySeconds},
		Interval: time.Millisecond,
		IsRunning: func(string) (bool, error) {
			return false, errors.New("process snapshot unavailable")
		},
		Trigger: func(context.Context, time.Time) bool { return true },
	}
	watcher.SeedOwedSession()
	return watcher
}

// TestSelfStoppedWatcherDebtSurvivesRebuild guards the owed-sleep invariant
// across a watcher self-stop: the unsettled debt must be stashed by the reap
// and re-armed by the replacement watcher instead of being dropped until the
// watched process runs a brand-new session.
func TestSelfStoppedWatcherDebtSurvivesRebuild(t *testing.T) {
	app := NewApp()
	// The rebuilt watcher fires the inherited debt on its first poll; block
	// the action there so the debt stays owed while the test asserts it.
	releaseTrigger := make(chan struct{})
	app.scanForAutoSleep = func(ctx context.Context) ([]station.StationInfo, error) {
		select {
		case <-releaseTrigger:
		case <-ctx.Done():
		}
		return nil, nil
	}
	app.setPowerForAutoSleep = func(context.Context, string) (station.BulkPowerResult, error) {
		return station.BulkPowerResult{}, nil
	}
	var failedEvents atomic.Int32
	app.autoSleepEventSink = func(event autoSleepEvent) {
		if event.Phase == "failed" {
			failedEvents.Add(1)
		}
	}

	watcher := selfStoppingWatcher()
	ctx, cancel := context.WithCancel(context.Background())
	go watcher.Run(ctx)
	select {
	case <-watcher.Done():
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("watcher did not self-stop after persistent check failures")
	}
	cancel()

	app.autoSleepMutex.Lock()
	app.autoSleepWatcher = watcher
	app.autoSleepCancel = func() {}
	app.autoSleepMutex.Unlock()

	app.reapStoppedAutoSleepWatcher(watcher)

	app.autoSleepMutex.Lock()
	stillSet := app.autoSleepWatcher == watcher
	owed := app.autoSleepSelfStopOwed
	app.autoSleepMutex.Unlock()
	if stillSet {
		t.Fatal("self-stopped watcher was not cleared")
	}
	if !owed {
		t.Fatal("self-stopped watcher debt was not stashed for the rebuild")
	}
	if failedEvents.Load() != 1 {
		t.Fatalf("failed watcher events = %d, want one surfaced failure", failedEvents.Load())
	}

	app.applyAutoSleep(autosleep.Settings{Enabled: true, Target: string(autosleep.TargetSteamVR), DelaySeconds: autosleep.MinDelaySeconds})
	defer app.stopAutoSleep()
	defer close(releaseTrigger)

	app.autoSleepMutex.Lock()
	rebuilt := app.autoSleepWatcher
	owedAfter := app.autoSleepSelfStopOwed
	app.autoSleepMutex.Unlock()
	if rebuilt == nil {
		t.Fatal("rebuild did not create a replacement watcher")
	}
	if owedAfter {
		t.Fatal("replacement watcher did not consume the stashed debt")
	}
	if !rebuilt.OwesTrigger() {
		t.Fatal("replacement watcher lost the owed sleep debt")
	}
}

// TestSelfStoppedWatcherCountdownSurvivesRebuild guards the countdown
// invariant across a watcher self-stop: a closed-session countdown that had
// not fired yet (delay not elapsed, so no debt exists) must be stashed by the
// reap and continued by the replacement watcher. Restarting the replacement
// monitor from idle would never fire for the already-closed session and the
// stations would stay awake until the watched process runs a brand-new
// session.
func TestSelfStoppedWatcherCountdownSurvivesRebuild(t *testing.T) {
	app := NewApp()
	// Keep the replacement watcher's polls quiet so the continued countdown
	// cannot be disturbed by real process observations.
	app.autoSleepIsRunning = func(string) (bool, error) { return false, nil }

	var checkCalls atomic.Int32
	watcher := &autosleep.Watcher{
		Settings: autosleep.Settings{Enabled: true, Target: string(autosleep.TargetSteamVR), DelaySeconds: autosleep.MaxDelaySeconds},
		Interval: time.Millisecond,
		IsRunning: func(string) (bool, error) {
			call := checkCalls.Add(1)
			switch {
			case call == 1:
				return true, nil
			case call == 2:
				return false, nil
			default:
				return false, errors.New("process snapshot unavailable")
			}
		},
		Trigger: func(context.Context, time.Time) bool { return true },
	}
	ctx, cancel := context.WithCancel(context.Background())
	go watcher.Run(ctx)
	select {
	case <-watcher.Done():
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("watcher did not self-stop after persistent check failures")
	}
	cancel()

	active, closedAt := watcher.Monitor.Countdown()
	if !active || closedAt.IsZero() {
		t.Fatalf("watcher self-stopped without an in-flight countdown: active=%v closedAt=%v", active, closedAt)
	}

	app.autoSleepMutex.Lock()
	app.autoSleepWatcher = watcher
	app.autoSleepCancel = func() {}
	app.autoSleepMutex.Unlock()

	app.reapStoppedAutoSleepWatcher(watcher)

	app.autoSleepMutex.Lock()
	stillSet := app.autoSleepWatcher == watcher
	stashed := app.autoSleepSelfStopCountdownAt
	owed := app.autoSleepSelfStopOwed
	app.autoSleepMutex.Unlock()
	if stillSet {
		t.Fatal("self-stopped watcher was not cleared")
	}
	if owed {
		t.Fatal("countdown without a consumed trigger was stashed as a debt")
	}
	if !stashed.Equal(closedAt) {
		t.Fatalf("stashed countdown = %v, want %v", stashed, closedAt)
	}

	settings := autosleep.Settings{Enabled: true, Target: string(autosleep.TargetSteamVR), DelaySeconds: autosleep.MaxDelaySeconds}
	app.applyAutoSleep(settings)
	defer app.stopAutoSleep()

	app.autoSleepMutex.Lock()
	rebuilt := app.autoSleepWatcher
	stashedAfter := app.autoSleepSelfStopCountdownAt
	app.autoSleepMutex.Unlock()
	if rebuilt == nil {
		t.Fatal("rebuild did not create a replacement watcher")
	}
	if !stashedAfter.IsZero() {
		t.Fatal("replacement watcher did not consume the stashed countdown")
	}
	// Give the replacement one poll window; its countdown must survive it
	// (the long delay keeps the trigger from firing during the test).
	time.Sleep(20 * time.Millisecond)
	rebuiltActive, rebuiltClosedAt := rebuilt.Monitor.Countdown()
	if !rebuiltActive || !rebuiltClosedAt.Equal(closedAt) {
		t.Fatalf("replacement monitor countdown = (%v, %v), want the continued session (%v, %v)",
			rebuiltActive, rebuiltClosedAt, true, closedAt)
	}
	if rebuilt.OwesTrigger() {
		t.Fatal("continued countdown unexpectedly carried a sleep debt")
	}
}

// TestStopAutoSleepCancelsPendingRebuild guards shutdown against a pending
// self-stop rebuild timer: without cancelling it, the shutdown wait would
// block until the timer fires.
func TestStopAutoSleepCancelsPendingRebuild(t *testing.T) {
	app := NewApp()
	app.scheduleAutoSleepRebuild()
	app.autoSleepMutex.Lock()
	pending := app.autoSleepRebuildTimer != nil
	app.autoSleepMutex.Unlock()
	if !pending {
		t.Fatal("scheduleAutoSleepRebuild() did not register a pending rebuild")
	}

	start := time.Now()
	app.stopAutoSleep()
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("stopAutoSleep() took %v, want the pending rebuild cancelled immediately", elapsed)
	}
	app.autoSleepMutex.Lock()
	leftover := app.autoSleepRebuildTimer != nil
	app.autoSleepMutex.Unlock()
	if leftover {
		t.Fatal("stopAutoSleep() left a pending rebuild timer registered")
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
