package main

import (
	"context"
	"errors"
	"log"
	"time"

	"lhcontrol/internal/autosleep"
	"lhcontrol/internal/bluetooth"
	"lhcontrol/internal/platform"
	"lhcontrol/internal/station"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// autoSleepStopLimit mirrors station.shutdownDrainLimit so a stuck sleep
// action cannot keep the window from closing on shutdown.
const autoSleepStopLimit = 60 * time.Second

type autoSleepEvent struct {
	ID uint64 `json:"id,omitempty"`

	Phase string `json:"phase"`

	Success int `json:"success"`

	Failed int `json:"failed"`

	Unconfirmed int `json:"unconfirmed,omitempty"`

	Skipped int `json:"skipped,omitempty"`

	TimedOut bool `json:"timedOut,omitempty"`

	TimedOutSkipped int `json:"timedOutSkipped,omitempty"`

	Error string `json:"error,omitempty"`

	UpdateID uint64 `json:"updateId,omitempty"`

	Stations []station.StationInfo `json:"stations,omitempty"`
}

// GetAutoSleepSettings returns the persisted automatic-sleep settings.

func (a *App) GetAutoSleepSettings() autosleep.Settings {

	return a.config.GetAutoSleep()

}

// SetAutoSleepSettings validates and persists the automatic-sleep settings,

// then restarts the watcher so the change applies immediately.

func (a *App) SetAutoSleepSettings(settings autosleep.Settings) error {

	if err := settings.Validate(); err != nil {

		return err

	}
	a.autoSleepSettingsMutex.Lock()
	defer a.autoSleepSettingsMutex.Unlock()
	previousSettings := a.config.GetAutoSleep()

	log.Printf("Setting auto-sleep: enabled=%v target=%s delay=%ds", settings.Enabled, settings.Target, settings.DelaySeconds)

	err := a.config.SetAutoSleep(settings)

	a.setConfigPersistenceStatus()

	if err != nil {

		return err

	}

	// Re-saving the same valid value can recover a previous persistence error,
	// but it must not cancel an automatic-sleep action that is already running.
	// If runtime state is missing or out of sync, repair it even though the
	// persisted value itself did not change.
	if previousSettings != settings || !a.autoSleepMatches(settings) {
		a.applyAutoSleep(settings)
	}

	return nil

}

func (a *App) autoSleepMatches(settings autosleep.Settings) bool {
	a.autoSleepMutex.Lock()
	defer a.autoSleepMutex.Unlock()
	if !settings.Enabled || a.shuttingDown.Load() {
		return a.autoSleepWatcher == nil && a.autoSleepCancel == nil
	}
	return a.autoSleepWatcher != nil && a.autoSleepCancel != nil &&
		a.autoSleepWatcher.Settings == settings
}

// GetLanguage returns the persisted UI language. An empty string tells the

// frontend to follow the operating-system language for this launch.

func (a *App) applyAutoSleep(settings autosleep.Settings) {

	a.autoSleepMutex.Lock()

	defer a.autoSleepMutex.Unlock()

	var monitor *autosleep.Monitor
	if previous := a.autoSleepWatcher; previous != nil {
		// Cancel before taking the replacement snapshot. Watcher polls perform
		// their final cancellation check under the same lifecycle lock used by
		// ReplacementMonitor, so an old observation cannot land just after the
		// snapshot and silently disappear.
		a.autoSleepCancel()
		if previous.Settings.Target == settings.Target {
			// Preserve idle/running/countdown state, and re-arm a consumed
			// trigger whose action is still running or queued.
			monitor = previous.ReplacementMonitor(settings.Delay())
		}
		a.autoSleepCancel = nil
		a.autoSleepWatcher = nil
		// The cancelled watcher exits on its own; if it is still finishing a
		// running sleep action, joining it here would block the settings
		// caller behind a possibly minute-long Bluetooth operation. Shutdown
		// joins every watcher through the shared wait group instead.
	}

	if !settings.Enabled || a.shuttingDown.Load() {

		return

	}

	if monitor == nil {
		monitor = autosleep.NewMonitor(settings.Delay())
	}

	watcher := &autosleep.Watcher{
		Settings: settings,
		Monitor:  monitor,
		IsRunning: func(name string) (bool, error) {
			return platform.IsProcessRunning(name)
		},
		Trigger: a.runAutoSleepSession,
	}

	ctx, cancel := context.WithCancel(context.Background())

	a.autoSleepCancel = cancel
	a.autoSleepWatcher = watcher

	a.autoSleepWG.Add(1)

	go func() {

		defer a.autoSleepWG.Done()

		watcher.Run(ctx)

	}()

}

// stopAutoSleep terminates the auto-sleep watcher and waits for it,

// including a trigger action that may still be running. It also joins

// superseded watchers whose sleep action was still draining when settings

// replaced them. The wait is bounded: a sleep action stuck in an adapter

// call that ignores cancellation must not keep the window from closing;

// the process exits after the limit and the OS reclaims the handles.

func (a *App) stopAutoSleep() {

	a.autoSleepMutex.Lock()

	defer a.autoSleepMutex.Unlock()

	if a.autoSleepCancel != nil {

		a.autoSleepCancel()

		a.autoSleepCancel = nil

		a.autoSleepWatcher = nil

	}

	waited := make(chan struct{})

	go func() {

		defer close(waited)

		a.autoSleepWG.Wait()

	}()

	limit := a.autoSleepStopWait
	if limit <= 0 {
		limit = autoSleepStopLimit
	}

	select {

	case <-waited:

	case <-time.After(limit):

		log.Printf("Auto-sleep shutdown wait exceeded %s; exiting without the running action", limit)

	}

}

func (a *App) emitAutoSleep(event autoSleepEvent) {
	if a.autoSleepEventSink != nil {
		a.autoSleepEventSink(event)
		return
	}

	if a.ctx == nil || a.shuttingDown.Load() {

		return

	}

	runtime.EventsEmit(a.ctx, "auto-sleep", event)

}

func (a *App) emitTerminalAutoSleep(event autoSleepEvent) {

	// HTTP mutations and automatic sleep share one sequencer so update IDs
	// describe snapshot order across both event streams.
	event.UpdateID, event.Stations = a.snapshotExternalStationUpdate(a.stationManager.GetStationInfo)

	a.emitAutoSleep(event)

}

func summarizeAutoSleepResults(results []station.BulkPowerStationResult) (success, unconfirmed, failed, skipped int) {

	for _, entry := range results {

		if entry.Skipped && !entry.Success {

			skipped++

		} else if entry.Success && entry.Confirmed {

			success++

		} else if entry.Success && entry.CommandSent {

			unconfirmed++

		} else {

			failed++

		}

	}

	return success, unconfirmed, failed, skipped

}

func cancelledAutoSleepEvent(results []station.BulkPowerStationResult, reason string) autoSleepEvent {

	success, unconfirmed, failed, skipped := summarizeAutoSleepResults(results)

	return autoSleepEvent{

		Phase: "cancelled", Success: success, Unconfirmed: unconfirmed, Failed: failed, Skipped: skipped, Error: reason,
	}

}

func timedOutAutoSleepEvent(results []station.BulkPowerStationResult, reason string) autoSleepEvent {

	success, unconfirmed, failed, skipped := summarizeAutoSleepResults(results)

	timedOutSkipped := 0

	for _, entry := range results {

		if entry.Skipped && !entry.Success &&
			(entry.Reason == station.ReasonBulkOperationTimeout || entry.Reason == station.ReasonStationOperationTimeout) {

			timedOutSkipped++

		}

	}

	return autoSleepEvent{

		Phase: "timed-out", Success: success, Unconfirmed: unconfirmed, Failed: failed,

		Skipped: skipped - timedOutSkipped, TimedOut: true, TimedOutSkipped: timedOutSkipped, Error: reason,
	}

}

// runAutoSleep invokes an unkeyed action for direct callers and tests.

func (a *App) runAutoSleep(ctx context.Context) {

	a.runAutoSleepSession(ctx, time.Time{})

}

// runAutoSleepSession is the watcher trigger: rescan for base stations, then put

// every known station to sleep. When the user is running a Bluetooth

// operation this cycle is skipped without a retry, as configured.

func (a *App) runAutoSleepSession(ctx context.Context, closedAt time.Time) {

	if a.shuttingDown.Load() || ctx.Err() != nil {

		return

	}

	// A replacement watcher can start before the cancelled watcher's adapter
	// call has drained. Serialize those actions outside the station manager so
	// the replacement waits instead of being rejected as globally busy. The
	// wait remains cancellable when the watched process starts again.
	select {

	case <-ctx.Done():

		return

	case <-a.autoSleepActionSlot:

	}

	settled := false
	defer func() {

		if settled && !closedAt.IsZero() {

			a.autoSleepSettledSession = closedAt

		}

		a.autoSleepActionSlot <- struct{}{}

	}()

	if a.shuttingDown.Load() || ctx.Err() != nil {

		return

	}

	// The old action may have completed normally between applyAutoSleep taking
	// its owed-trigger snapshot and cancelling the watcher. In that case the
	// carried replacement represents the same closed process session and must
	// not run a duplicate scan/power lifecycle.
	if !closedAt.IsZero() && a.autoSleepSettledSession.Equal(closedAt) {

		return

	}

	finishOperation := a.beginTrackedOperation("auto-sleep")
	defer finishOperation()

	actionID := a.autoSleepActionID.Add(1)
	emitTerminal := func(event autoSleepEvent) {
		if event.Phase != "cancelled" {
			settled = true
		}
		event.ID = actionID
		a.emitTerminalAutoSleep(event)
	}

	a.emitAutoSleep(autoSleepEvent{ID: actionID, Phase: "started"})

	log.Println("Auto-sleep: scanning for base stations")

	scan := a.scanForAutoSleep
	if scan == nil {
		scan = a.stationManager.ScanAndFetchStationsContext
	}
	_, scanErr := scan(ctx)

	switch {

	case errors.Is(scanErr, context.Canceled), errors.Is(scanErr, bluetooth.ErrScanCancelled):

		if !a.shuttingDown.Load() {

			emitTerminal(cancelledAutoSleepEvent(nil, "cancelled before power commands were sent"))

		}

		return

	case errors.Is(scanErr, station.ErrOperationInProgress):

		log.Println("Auto-sleep skipped: another Bluetooth operation is in progress")

		emitTerminal(autoSleepEvent{Phase: "skipped", Error: "another Bluetooth operation is in progress"})

		return

	case errors.Is(scanErr, station.ErrShuttingDown):

		return

	case scanErr != nil:

		// The bulk sleep still targets every known station, so a failed scan

		// degrades to the cached registry instead of aborting the feature.

		log.Printf("Auto-sleep scan failed, continuing with known stations: %v", scanErr)

	}

	if a.shuttingDown.Load() {

		return

	}

	if ctx.Err() != nil {

		emitTerminal(cancelledAutoSleepEvent(nil, "cancelled after scanning and before power commands were sent"))

		return

	}

	log.Println("Auto-sleep: putting all known stations to sleep")

	setPower := a.setPowerForAutoSleep
	if setPower == nil {
		setPower = a.stationManager.SetAllStationsPowerDetailedContext
	}
	result, err := setPower(ctx, "sleep")

	switch {

	case errors.Is(err, context.Canceled):

		if !a.shuttingDown.Load() {

			// Cancellation can arrive after one or more workers have already sent

			// their commands. Preserve those outcomes instead of reporting the

			// entire automatic-sleep action as if nothing happened.

			emitTerminal(cancelledAutoSleepEvent(result.Results, "watched process restarted or automatic sleep was reconfigured"))

		}

		return

	case errors.Is(err, context.DeadlineExceeded), result.TimedOut:

		log.Printf("Auto-sleep timed out with partial results: %v", err)

		emitTerminal(timedOutAutoSleepEvent(result.Results, "bulk power timeout reached"))

		return

	case errors.Is(err, station.ErrOperationInProgress):

		log.Println("Auto-sleep skipped: another Bluetooth operation is in progress")

		emitTerminal(autoSleepEvent{Phase: "skipped", Error: "another Bluetooth operation is in progress"})

	case errors.Is(err, station.ErrShuttingDown):

	case err != nil:

		log.Printf("Auto-sleep failed: %v", err)

		emitTerminal(autoSleepEvent{Phase: "failed", Error: err.Error()})

	default:

		success, unconfirmed, failed, skipped := summarizeAutoSleepResults(result.Results)

		log.Printf("Auto-sleep completed: %d confirmed, %d unconfirmed, %d failed, %d skipped", success, unconfirmed, failed, skipped)

		emitTerminal(autoSleepEvent{

			Phase: "completed", Success: success, Unconfirmed: unconfirmed, Failed: failed, Skipped: skipped,
		})

	}

}

// shutdown is called when the app terminates.
