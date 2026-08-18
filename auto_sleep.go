package main

import (
	"context"
	"errors"
	"fmt"
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
		// A blocked-save recovery inside the config setter may have replaced
		// the in-memory settings while the save itself still failed. The
		// replay triggered above cannot take this setter's mutex, so converge
		// the runtime onto the recovered configuration here instead of
		// leaving the watcher silently out of sync with it.
		current := a.config.GetAutoSleep()
		if !a.autoSleepMatches(current) {
			a.applyAutoSleep(current)
		}
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

	// An explicit settings application supersedes any pending self-stop
	// rebuild: this call already rebuilds (or detaches) the watcher.
	a.stopScheduledAutoSleepRebuildLocked()

	var monitor *autosleep.Monitor
	seedOwedSession := false
	if previous := a.autoSleepWatcher; previous != nil {
		// Cancel before taking the replacement snapshot. Watcher polls perform
		// their final cancellation check under the same lifecycle lock used by
		// ReplacementMonitor, so an old observation cannot land just after the
		// snapshot and silently disappear.
		a.autoSleepCancel()
		if previous.Settings.Target == settings.Target {
			// Preserve idle/running/countdown state, and re-arm a consumed
			// trigger whose action is still running or queued. A debt already
			// carried from another target keeps its carry semantics.
			var carried bool
			monitor, carried = previous.ReplacementMonitor(settings.Delay())
			seedOwedSession = carried
		} else if owed, closedAt := previous.OwedSession(); owed {
			// A target change discards the old process observation (it belongs
			// to a different session source), but an owed unsettled sleep must
			// survive: seed the session-close time so the replacement watcher
			// re-arms the debt (and keeps the closedAt de-duplication key)
			// instead of dropping it until the new target's next full session.
			// The debt is also seeded as carried: if the new target's process
			// is already running, observing it must keep the debt owed (a
			// different process is not a relaunch) until the re-arm path fires
			// it once that process stops.
			monitor = autosleep.NewMonitorContinuing(settings.Delay(), closedAt)
			seedOwedSession = true
		}
		a.autoSleepCancel = nil
		a.autoSleepWatcher = nil
		// The cancelled watcher exits on its own; if it is still finishing a
		// running sleep action, joining it here would block the settings
		// caller behind a possibly minute-long Bluetooth operation. Shutdown
		// joins every watcher through the shared wait group instead.
	}
	if !settings.Enabled || a.shuttingDown.Load() {
		// Detaching the feature drops a debt a self-stopped watcher left
		// behind: with monitoring disabled there is no session to settle it.
		a.autoSleepSelfStopOwed = false
		a.autoSleepSelfStopClosedAt = time.Time{}
		return

	}

	if monitor == nil {
		if a.autoSleepSelfStopOwed {
			// The previous watcher self-stopped while its sleep debt was
			// unsettled. Continue the closed-session countdown and seed the
			// debt so the rebuilt watcher fires it instead of waiting for a
			// brand-new process session that may never come.
			monitor = autosleep.NewMonitorContinuing(settings.Delay(), a.autoSleepSelfStopClosedAt)
			seedOwedSession = true
			a.autoSleepSelfStopOwed = false
			a.autoSleepSelfStopClosedAt = time.Time{}
		} else {
			monitor = autosleep.NewMonitor(settings.Delay())
		}
	}
	watcher := &autosleep.Watcher{
		Settings: settings,
		Monitor:  monitor,
		IsRunning: func(name string) (bool, error) {
			return platform.IsProcessRunning(name)
		},
		Trigger: a.runAutoSleepSession,
	}
	if seedOwedSession {
		watcher.SeedOwedSession()
	}

	ctx, cancel := context.WithCancel(context.Background())

	a.autoSleepCancel = cancel
	a.autoSleepWatcher = watcher

	a.autoSleepWG.Add(1)

	go func() {

		defer a.autoSleepWG.Done()

		watcher.Run(ctx)

	}()

	a.autoSleepWG.Add(1)

	go func() {

		defer a.autoSleepWG.Done()

		a.reapStoppedAutoSleepWatcher(watcher)

	}()

}

// reapStoppedAutoSleepWatcher clears the runtime state when a watcher stopped
// on its own (invalid target or persistent process-check failures) instead of
// being cancelled by a settings replacement or shutdown. Without this the
// stale watcher reference keeps autoSleepMatches reporting a healthy watcher,
// so re-saving the same settings never rebuilds it and automatic sleep stays
// silently disabled until the application restarts.
func (a *App) reapStoppedAutoSleepWatcher(watcher *autosleep.Watcher) {

	<-watcher.Done()

	err := watcher.ExitErr()
	if err == nil {
		// Cancelled by a replacement or shutdown; that path already detached
		// the watcher under autoSleepMutex.
		return
	}

	// A self-stop cancels any action the watcher was still running, and an
	// action cancelled before settling leaves its sleep owed. Snapshot that
	// debt before clearing the watcher so the rebuild can re-arm it instead
	// of dropping it until the watched process runs a full new session.
	owed, closedAt := watcher.OwedSession()

	a.autoSleepMutex.Lock()
	cleared := a.autoSleepWatcher == watcher
	if cleared {
		log.Printf("Auto-sleep watcher stopped unexpectedly (%v); clearing it so re-applying settings restarts it", err)
		a.autoSleepWatcher = nil
		if a.autoSleepCancel != nil {
			a.autoSleepCancel()
			a.autoSleepCancel = nil
		}
		if owed {
			a.autoSleepSelfStopOwed = true
			a.autoSleepSelfStopClosedAt = closedAt
		}
	}
	a.autoSleepMutex.Unlock()

	// Surface the failure outside the lock; the event must not queue behind
	// settings calls and the sink must not be able to deadlock on it.
	if cleared {
		a.emitAutoSleep(autoSleepEvent{Phase: "failed", Error: fmt.Sprintf("automatic sleep stopped watching: %v", err)})
		a.scheduleAutoSleepRebuild()
	}

}

// autoSleepRestartDelay spaces automatic watcher rebuilds after a self-stop.
// Persistent process-check failures (for example an AV product briefly
// breaking Toolhelp snapshots) self-stop the watcher; without a rebuild the
// feature would stay silently disabled until somebody re-opened settings and
// saved again. The delay bounds the rebuild rate when failures persist: each
// failed watcher takes at least maxConsecutiveCheckErrors polls to self-stop,
// so the loop stays slow and cheap, and a single successful check afterwards
// restores normal monitoring.
const autoSleepRestartDelay = 30 * time.Second

// scheduleAutoSleepRebuild re-applies the persisted auto-sleep settings after
// a cooldown so a self-stopped watcher is replaced instead of leaving the
// feature disabled. The rebuild funnels through SetAutoSleepSettings, whose
// autoSleepMatches check makes it a no-op when a settings change or shutdown
// already rebuilt or detached the watcher in the meantime.
func (a *App) scheduleAutoSleepRebuild() {
	if a.shuttingDown.Load() {
		return
	}
	a.autoSleepMutex.Lock()
	defer a.autoSleepMutex.Unlock()
	if a.shuttingDown.Load() {
		return
	}
	// A second self-stop before the first rebuild fires must not accumulate
	// timers; the pending one already re-applies the current settings.
	a.stopScheduledAutoSleepRebuildLocked()
	a.autoSleepWG.Add(1)
	var rebuildTimer *time.Timer
	rebuildTimer = time.AfterFunc(autoSleepRestartDelay, func() {
		defer a.autoSleepWG.Done()
		a.autoSleepMutex.Lock()
		if a.autoSleepRebuildTimer == rebuildTimer {
			a.autoSleepRebuildTimer = nil
		}
		a.autoSleepMutex.Unlock()
		if a.shuttingDown.Load() {
			return
		}
		settings := a.config.GetAutoSleep()
		if !settings.Enabled {
			return
		}
		if _, err := autosleep.Target(settings.Target).ProcessName(); err != nil {
			// A configuration error cannot be repaired by a rebuild; it would
			// only loop. Re-saving valid settings remains the recovery path.
			return
		}
		log.Println("Auto-sleep watcher rebuilding after an unexpected stop")
		if err := a.SetAutoSleepSettings(settings); err != nil {
			log.Printf("Auto-sleep watcher rebuild failed: %v", err)
		}
	})
	a.autoSleepRebuildTimer = rebuildTimer
}

// stopScheduledAutoSleepRebuildLocked cancels a pending self-stop rebuild and
// releases the wait-group count it reserved. A timer that already fired keeps
// its own count until the callback finishes. Callers hold autoSleepMutex.
func (a *App) stopScheduledAutoSleepRebuildLocked() {
	if a.autoSleepRebuildTimer == nil {
		return
	}
	if a.autoSleepRebuildTimer.Stop() {
		a.autoSleepWG.Done()
	}
	a.autoSleepRebuildTimer = nil
}

// stopAutoSleep terminates the auto-sleep watcher and waits for it,

// including a trigger action that may still be running. It also joins

// superseded watchers whose sleep action was still draining when settings

// replaced them. The wait is bounded: a sleep action stuck in an adapter

// call that ignores cancellation must not keep the window from closing;

// the process exits after the limit and the OS reclaims the handles.

func (a *App) stopAutoSleep() {

	// Detach the watcher under the lock, then join outside it: the wait can
	// take up to the stop limit, and settings calls that only need
	// autoSleepMatches must not queue behind a draining sleep action.

	a.autoSleepMutex.Lock()

	// Cancel a pending self-stop rebuild first: its wait-group count would
	// otherwise keep the shutdown wait blocked until the timer fired.
	a.stopScheduledAutoSleepRebuildLocked()
	a.autoSleepSelfStopOwed = false
	a.autoSleepSelfStopClosedAt = time.Time{}

	if a.autoSleepCancel != nil {

		a.autoSleepCancel()

		a.autoSleepCancel = nil

		a.autoSleepWatcher = nil

	}

	a.autoSleepMutex.Unlock()

	waited := make(chan struct{})

	go func() {

		defer close(waited)

		a.autoSleepWG.Wait()

	}()

	limit := a.autoSleepStopWait
	if limit <= 0 {
		limit = autoSleepStopLimit
	}

	stopTimer := time.NewTimer(limit)
	defer stopTimer.Stop()

	select {

	case <-waited:

	case <-stopTimer.C:

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

var _ autosleep.TriggerFunc = (*App)(nil).runAutoSleepSession

// runAutoSleepSession is the watcher trigger: rescan for base stations, then put

// every known station to sleep. When the user is running a Bluetooth

// operation this cycle is skipped without a retry, as configured.

// The returned settled flag implements autosleep.TriggerFunc: it is true once
// the session's sleep reached a terminal outcome (completed, failed, skipped,
// timed out, or was already settled by an earlier run), and false while the
// action was cancelled before settling so the watcher keeps the sleep owed.

func (a *App) runAutoSleepSession(ctx context.Context, closedAt time.Time) (settled bool) {

	if a.shuttingDown.Load() || ctx.Err() != nil {

		return false

	}

	// A replacement watcher can start before the cancelled watcher's adapter
	// call has drained. Serialize those actions outside the station manager so
	// the replacement waits instead of being rejected as globally busy. The
	// wait remains cancellable when the watched process starts again.
	select {

	case <-ctx.Done():

		return false

	case <-a.autoSleepActionSlot:

	}

	defer func() {

		if settled && !closedAt.IsZero() {

			a.autoSleepSettledSession = closedAt

		}

		a.autoSleepActionSlot <- struct{}{}

	}()

	if a.shuttingDown.Load() || ctx.Err() != nil {

		return false

	}

	// The old action may have completed normally between applyAutoSleep taking
	// its owed-trigger snapshot and cancelling the watcher. In that case the
	// carried replacement represents the same closed process session and must
	// not run a duplicate scan/power lifecycle. Report the session as settled
	// so the watcher clears the owed trigger instead of re-arming a session
	// whose sleep already ran.
	if !closedAt.IsZero() && a.autoSleepSettledSession.Equal(closedAt) {

		return true

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

			// The cancellation can also come from an explicit bulk cancel
			// while this session runs, so report the interruption itself
			// instead of asserting one cause.
			emitTerminal(cancelledAutoSleepEvent(result.Results, "cancelled while power commands were in progress"))

		}

		return

	case errors.Is(err, context.DeadlineExceeded), result.TimedOut:

		// The structured timeout flag can be set while err is nil; log both
		// halves instead of printing a nil placeholder.

		log.Printf("Auto-sleep timed out with partial results (timedOut=%v, err=%v)", result.TimedOut, err)

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

	return settled

}

// shutdown is called when the app terminates.
