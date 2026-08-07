package main

import (
	"context"
	"errors"
	"log"

	"lhcontrol/internal/autosleep"
	"lhcontrol/internal/bluetooth"
	"lhcontrol/internal/platform"
	"lhcontrol/internal/station"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type autoSleepEvent struct {
	Phase string `json:"phase"`

	Success int `json:"success"`

	Failed int `json:"failed"`

	Unconfirmed int `json:"unconfirmed,omitempty"`

	Skipped int `json:"skipped,omitempty"`

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

	log.Printf("Setting auto-sleep: enabled=%v target=%s delay=%ds", settings.Enabled, settings.Target, settings.DelaySeconds)

	err := a.config.SetAutoSleep(settings)

	a.setConfigPersistenceStatus()

	if err != nil {

		return err

	}

	a.applyAutoSleep(settings)

	return nil

}

// GetLanguage returns the persisted UI language. An empty string tells the

// frontend to follow the operating-system language for this launch.

func (a *App) GetLanguage() string {

	return a.config.GetLanguage()

}

// SetLanguage validates and persists the UI language.

func (a *App) SetLanguage(language string) error {

	err := a.config.SetLanguage(language)

	a.setConfigPersistenceStatus()

	return err

}

func (a *App) GetBulkPowerTimeoutSeconds() int {

	return a.config.GetBulkPowerTimeoutSeconds()

}

func (a *App) SetBulkPowerTimeoutSeconds(timeoutSeconds int) error {

	err := a.config.SetBulkPowerTimeoutSeconds(timeoutSeconds)

	a.setConfigPersistenceStatus()

	return err

}

func (a *App) GetStatusPollIntervalSeconds() int {

	return a.config.GetStatusPollIntervalSeconds()

}

func (a *App) SetStatusPollIntervalSeconds(intervalSeconds int) error {

	err := a.config.SetStatusPollIntervalSeconds(intervalSeconds)

	a.setConfigPersistenceStatus()

	return err

}

// applyAutoSleep (re)starts the auto-sleep watcher goroutine to match the

// given settings. Calling it repeatedly is safe: the previous watcher is

// cancelled and joined first.

func (a *App) applyAutoSleep(settings autosleep.Settings) {

	a.autoSleepMutex.Lock()

	defer a.autoSleepMutex.Unlock()

	if a.autoSleepCancel != nil {

		a.autoSleepCancel()

		a.autoSleepWG.Wait()

		a.autoSleepCancel = nil

	}

	if !settings.Enabled || a.shuttingDown.Load() {

		return

	}

	ctx, cancel := context.WithCancel(context.Background())

	a.autoSleepCancel = cancel

	a.autoSleepWG.Add(1)

	go func() {

		defer a.autoSleepWG.Done()

		watcher := &autosleep.Watcher{

			Settings: settings,

			IsRunning: func(name string) (bool, error) {

				return platform.IsProcessRunning(name)

			},

			Trigger: a.runAutoSleep,
		}

		watcher.Run(ctx)

	}()

}

// stopAutoSleep terminates the auto-sleep watcher and waits for it,

// including a trigger action that may still be running.

func (a *App) stopAutoSleep() {

	a.autoSleepMutex.Lock()

	defer a.autoSleepMutex.Unlock()

	if a.autoSleepCancel != nil {

		a.autoSleepCancel()

		a.autoSleepWG.Wait()

		a.autoSleepCancel = nil

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

	// Allocate the shared update ID before taking the snapshot. Concurrent HTTP

	// updates with a larger ID are then guaranteed to start their snapshot no

	// earlier than this one, so an older snapshot can never look newer.

	event.UpdateID = a.externalUpdateID.Add(1)

	event.Stations = a.stationManager.GetStationInfo()

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

// runAutoSleep is the watcher trigger: rescan for base stations, then put

// every known station to sleep. When the user is running a Bluetooth

// operation this cycle is skipped without a retry, as configured.

func (a *App) runAutoSleep(ctx context.Context) {

	if a.shuttingDown.Load() || ctx.Err() != nil {

		return

	}

	a.emitAutoSleep(autoSleepEvent{Phase: "started"})

	log.Println("Auto-sleep: scanning for base stations")

	scan := a.scanForAutoSleep
	if scan == nil {
		scan = a.stationManager.ScanAndFetchStationsContext
	}
	_, scanErr := scan(ctx)

	switch {

	case errors.Is(scanErr, context.Canceled), errors.Is(scanErr, bluetooth.ErrScanCancelled):

		if !a.shuttingDown.Load() {

			a.emitTerminalAutoSleep(cancelledAutoSleepEvent(nil, "cancelled before power commands were sent"))

		}

		return

	case errors.Is(scanErr, station.ErrOperationInProgress):

		log.Println("Auto-sleep skipped: another Bluetooth operation is in progress")

		a.emitTerminalAutoSleep(autoSleepEvent{Phase: "skipped", Error: "another Bluetooth operation is in progress"})

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

		a.emitTerminalAutoSleep(cancelledAutoSleepEvent(nil, "cancelled after scanning and before power commands were sent"))

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

			a.emitTerminalAutoSleep(cancelledAutoSleepEvent(result.Results, "watched process restarted or automatic sleep was reconfigured"))

		}

		return

	case errors.Is(err, station.ErrOperationInProgress):

		log.Println("Auto-sleep skipped: another Bluetooth operation is in progress")

		a.emitTerminalAutoSleep(autoSleepEvent{Phase: "skipped", Error: "another Bluetooth operation is in progress"})

	case errors.Is(err, station.ErrShuttingDown):

	case err != nil:

		log.Printf("Auto-sleep failed: %v", err)

		a.emitTerminalAutoSleep(autoSleepEvent{Phase: "failed", Error: err.Error()})

	default:

		success, unconfirmed, failed, skipped := summarizeAutoSleepResults(result.Results)

		log.Printf("Auto-sleep completed: %d confirmed, %d unconfirmed, %d failed, %d skipped", success, unconfirmed, failed, skipped)

		a.emitTerminalAutoSleep(autoSleepEvent{

			Phase: "completed", Success: success, Unconfirmed: unconfirmed, Failed: failed, Skipped: skipped,
		})

	}

}

// shutdown is called when the app terminates.
