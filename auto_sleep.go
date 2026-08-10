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

func (a *App) GetScanOnStartup() bool {

	return a.config.GetScanOnStartup()

}

func (a *App) SetScanOnStartup(enabled bool) error {

	err := a.config.SetScanOnStartup(enabled)

	a.setConfigPersistenceStatus()

	return err

}

func (a *App) GetScanDurationSeconds() int {

	return a.config.GetScanDurationSeconds()

}

func (a *App) SetScanDurationSeconds(durationSeconds int) error {

	err := a.config.SetScanDurationSeconds(durationSeconds)

	a.setConfigPersistenceStatus()

	return err

}

func (a *App) GetStatusPollingEnabled() bool {

	return a.config.GetStatusPollingEnabled()

}

func (a *App) SetStatusPollingEnabled(enabled bool) error {

	err := a.config.SetStatusPollingEnabled(enabled)

	a.setConfigPersistenceStatus()

	return err

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

func (a *App) GetStationOperationTimeoutSeconds() int {

	return a.config.GetStationOperationTimeoutSeconds()

}

func (a *App) SetStationOperationTimeoutSeconds(timeoutSeconds int) error {

	err := a.config.SetStationOperationTimeoutSeconds(timeoutSeconds)

	a.setConfigPersistenceStatus()

	return err

}

func (a *App) GetPowerConfirmAttemptsOn() int {

	return a.config.GetPowerConfirmAttemptsOn()

}

func (a *App) SetPowerConfirmAttemptsOn(attempts int) error {

	err := a.config.SetPowerConfirmAttemptsOn(attempts)

	a.setConfigPersistenceStatus()

	if err == nil {

		a.applyBluetoothTiming()

	}

	return err

}

func (a *App) GetPowerConfirmAttemptsOff() int {

	return a.config.GetPowerConfirmAttemptsOff()

}

func (a *App) SetPowerConfirmAttemptsOff(attempts int) error {

	err := a.config.SetPowerConfirmAttemptsOff(attempts)

	a.setConfigPersistenceStatus()

	if err == nil {

		a.applyBluetoothTiming()

	}

	return err

}

func (a *App) GetPowerConfirmPollIntervalMs() int {

	return a.config.GetPowerConfirmPollIntervalMs()

}

func (a *App) SetPowerConfirmPollIntervalMs(intervalMs int) error {

	err := a.config.SetPowerConfirmPollIntervalMs(intervalMs)

	a.setConfigPersistenceStatus()

	if err == nil {

		a.applyBluetoothTiming()

	}

	return err

}

func (a *App) GetBootFallbackSeconds() int {

	return a.config.GetBootFallbackSeconds()

}

func (a *App) SetBootFallbackSeconds(fallbackSeconds int) error {

	err := a.config.SetBootFallbackSeconds(fallbackSeconds)

	a.setConfigPersistenceStatus()

	if err == nil {

		a.applyBluetoothTiming()

	}

	return err

}

func (a *App) GetSleepFinalWriteTimeoutSeconds() int {

	return a.config.GetSleepFinalWriteTimeoutSeconds()

}

func (a *App) SetSleepFinalWriteTimeoutSeconds(timeoutSeconds int) error {

	err := a.config.SetSleepFinalWriteTimeoutSeconds(timeoutSeconds)

	a.setConfigPersistenceStatus()

	if err == nil {

		a.applyBluetoothTiming()

	}

	return err

}

func (a *App) GetSleepPrepareGapMs() int {

	return a.config.GetSleepPrepareGapMs()

}

func (a *App) SetSleepPrepareGapMs(gapMs int) error {

	err := a.config.SetSleepPrepareGapMs(gapMs)

	a.setConfigPersistenceStatus()

	if err == nil {

		a.applyBluetoothTiming()

	}

	return err

}

func (a *App) GetDiscoveryAttempts() int {

	return a.config.GetDiscoveryAttempts()

}

func (a *App) SetDiscoveryAttempts(attempts int) error {

	err := a.config.SetDiscoveryAttempts(attempts)

	a.setConfigPersistenceStatus()

	if err == nil {

		a.applyBluetoothTiming()

	}

	return err

}

func (a *App) GetDiscoveryRetryDelayMs() int {

	return a.config.GetDiscoveryRetryDelayMs()

}

func (a *App) SetDiscoveryRetryDelayMs(delayMs int) error {

	err := a.config.SetDiscoveryRetryDelayMs(delayMs)

	a.setConfigPersistenceStatus()

	if err == nil {

		a.applyBluetoothTiming()

	}

	return err

}

// GetAPIListenAddress returns the configured HTTP API listen address.

func (a *App) GetAPIListenAddress() string {

	return a.config.GetAPIListenAddress()

}

// SetAPIListenAddress validates and persists the HTTP API listen address and
// hot-restarts the listener loop so the change applies without app restart.

func (a *App) SetAPIListenAddress(address string) error {

	err := a.config.SetAPIListenAddress(address)

	a.setConfigPersistenceStatus()

	if err != nil {

		return err

	}

	// The listener loop binds whatever address the status advertises, so
	// publish the new address before restarting the loop.

	a.setAPIAddress(address)

	a.restartAPIServer()

	return nil

}

func (a *App) GetPowerWriteAttempts() int {

	return a.config.GetPowerWriteAttempts()

}

func (a *App) SetPowerWriteAttempts(attempts int) error {

	err := a.config.SetPowerWriteAttempts(attempts)

	a.setConfigPersistenceStatus()

	if err == nil {

		a.applyBluetoothTiming()

	}

	return err

}

func (a *App) GetOperationRetryDelayMs() int {

	return a.config.GetOperationRetryDelayMs()

}

func (a *App) SetOperationRetryDelayMs(delayMs int) error {

	err := a.config.SetOperationRetryDelayMs(delayMs)

	a.setConfigPersistenceStatus()

	if err == nil {

		a.applyBluetoothTiming()

	}

	return err

}

func (a *App) GetChannelConfirmAttempts() int {

	return a.config.GetChannelConfirmAttempts()

}

func (a *App) SetChannelConfirmAttempts(attempts int) error {

	err := a.config.SetChannelConfirmAttempts(attempts)

	a.setConfigPersistenceStatus()

	if err == nil {

		a.applyBluetoothTiming()

	}

	return err

}

func (a *App) GetChannelConfirmIntervalMs() int {

	return a.config.GetChannelConfirmIntervalMs()

}

func (a *App) SetChannelConfirmIntervalMs(intervalMs int) error {

	err := a.config.SetChannelConfirmIntervalMs(intervalMs)

	a.setConfigPersistenceStatus()

	if err == nil {

		a.applyBluetoothTiming()

	}

	return err

}

func (a *App) GetConfirmReconnectThreshold() int {

	return a.config.GetConfirmReconnectThreshold()

}

func (a *App) SetConfirmReconnectThreshold(threshold int) error {

	err := a.config.SetConfirmReconnectThreshold(threshold)

	a.setConfigPersistenceStatus()

	if err == nil {

		a.applyBluetoothTiming()

	}

	return err

}

func (a *App) GetConfirmReconnectDelayMs() int {

	return a.config.GetConfirmReconnectDelayMs()

}

func (a *App) SetConfirmReconnectDelayMs(delayMs int) error {

	err := a.config.SetConfirmReconnectDelayMs(delayMs)

	a.setConfigPersistenceStatus()

	if err == nil {

		a.applyBluetoothTiming()

	}

	return err

}

func (a *App) GetIdentifyAttempts() int {

	return a.config.GetIdentifyAttempts()

}

func (a *App) SetIdentifyAttempts(attempts int) error {

	err := a.config.SetIdentifyAttempts(attempts)

	a.setConfigPersistenceStatus()

	if err == nil {

		a.applyBluetoothTiming()

	}

	return err

}

func (a *App) GetPresenceMissThreshold() int {

	return a.config.GetPresenceMissThreshold()

}

func (a *App) SetPresenceMissThreshold(threshold int) error {

	err := a.config.SetPresenceMissThreshold(threshold)

	a.setConfigPersistenceStatus()

	if err == nil {

		a.applyBluetoothTiming()

	}

	return err

}

func (a *App) GetRecoveryRetryBaseSeconds() int {

	return a.config.GetRecoveryRetryBaseSeconds()

}

func (a *App) SetRecoveryRetryBaseSeconds(baseSeconds int) error {

	err := a.config.SetRecoveryRetryBaseSeconds(baseSeconds)

	a.setConfigPersistenceStatus()

	return err

}

func (a *App) GetRecoveryRetryMaxSeconds() int {

	return a.config.GetRecoveryRetryMaxSeconds()

}

func (a *App) SetRecoveryRetryMaxSeconds(maxSeconds int) error {

	err := a.config.SetRecoveryRetryMaxSeconds(maxSeconds)

	a.setConfigPersistenceStatus()

	return err

}

func (a *App) GetAbsentStationRetryLimit() int {

	return a.config.GetAbsentStationRetryLimit()

}

func (a *App) SetAbsentStationRetryLimit(limit int) error {

	err := a.config.SetAbsentStationRetryLimit(limit)

	a.setConfigPersistenceStatus()

	return err

}

func (a *App) GetInitialReadTimeoutSeconds() int {

	return a.config.GetInitialReadTimeoutSeconds()

}

func (a *App) SetInitialReadTimeoutSeconds(timeoutSeconds int) error {

	err := a.config.SetInitialReadTimeoutSeconds(timeoutSeconds)

	a.setConfigPersistenceStatus()

	return err

}

func (a *App) GetScanReadPhaseTimeoutSeconds() int {

	return a.config.GetScanReadPhaseTimeoutSeconds()

}

func (a *App) SetScanReadPhaseTimeoutSeconds(timeoutSeconds int) error {

	err := a.config.SetScanReadPhaseTimeoutSeconds(timeoutSeconds)

	a.setConfigPersistenceStatus()

	return err

}

func (a *App) GetStatusReadTimeoutSeconds() int {

	return a.config.GetStatusReadTimeoutSeconds()

}

func (a *App) SetStatusReadTimeoutSeconds(timeoutSeconds int) error {

	err := a.config.SetStatusReadTimeoutSeconds(timeoutSeconds)

	a.setConfigPersistenceStatus()

	return err

}

func (a *App) GetStatusRefreshTimeoutSeconds() int {

	return a.config.GetStatusRefreshTimeoutSeconds()

}

func (a *App) SetStatusRefreshTimeoutSeconds(timeoutSeconds int) error {

	err := a.config.SetStatusRefreshTimeoutSeconds(timeoutSeconds)

	a.setConfigPersistenceStatus()

	return err

}

func (a *App) GetChannelScanFreshnessSeconds() int {

	return a.config.GetChannelScanFreshnessSeconds()

}

func (a *App) SetChannelScanFreshnessSeconds(freshnessSeconds int) error {

	err := a.config.SetChannelScanFreshnessSeconds(freshnessSeconds)

	a.setConfigPersistenceStatus()

	return err

}

func (a *App) GetBluetoothInitRetrySeconds() int {

	return a.config.GetBluetoothInitRetrySeconds()

}

func (a *App) SetBluetoothInitRetrySeconds(retrySeconds int) error {

	err := a.config.SetBluetoothInitRetrySeconds(retrySeconds)

	a.setConfigPersistenceStatus()

	return err

}

// applyBluetoothTiming pushes the persisted protocol-timing settings into the
// bluetooth layer, which intentionally does not read the config package.

func (a *App) applyBluetoothTiming() {

	bluetooth.ConfigureTiming(bluetooth.TimingPolicy{
		ConfirmAttemptsOn:         a.config.GetPowerConfirmAttemptsOn(),
		ConfirmAttemptsOff:        a.config.GetPowerConfirmAttemptsOff(),
		ConfirmPollInterval:       a.config.PowerConfirmPollInterval(),
		BootFallbackAfter:         a.config.BootFallback(),
		FinalSleepWrite:           a.config.SleepFinalWriteTimeout(),
		PrepareGap:                a.config.SleepPrepareGap(),
		DiscoveryAttempts:         a.config.GetDiscoveryAttempts(),
		DiscoveryRetryDelay:       a.config.DiscoveryRetryDelay(),
		WriteAttempts:             a.config.GetPowerWriteAttempts(),
		OperationRetryDelay:       a.config.OperationRetryDelay(),
		ChannelConfirmAttempts:    a.config.GetChannelConfirmAttempts(),
		ChannelConfirmInterval:    a.config.ChannelConfirmInterval(),
		ConfirmReconnectThreshold: a.config.GetConfirmReconnectThreshold(),
		ConfirmReconnectDelay:     a.config.ConfirmReconnectDelay(),
		IdentifyAttempts:          a.config.GetIdentifyAttempts(),
		PresenceMissThreshold:     a.config.GetPresenceMissThreshold(),
	})

}

// applyAutoSleep (re)starts the auto-sleep watcher goroutine to match the
// given settings. Calling it repeatedly is safe: the previous watcher is
// cancelled (and joined at shutdown), never torn down mid-trigger.
func (a *App) applyAutoSleep(settings autosleep.Settings) {

	a.autoSleepMutex.Lock()

	defer a.autoSleepMutex.Unlock()

	var monitor *autosleep.Monitor
	if previous := a.autoSleepWatcher; previous != nil {
		if previous.Settings.Target == settings.Target {
			// A settings change for the same watched target must not silently
			// drop a pending sleep: carry the in-flight countdown over to the
			// replacement watcher.
			if active, closedAt := previous.Monitor.Countdown(); active {
				monitor = autosleep.NewMonitorContinuing(settings.Delay(), closedAt)
			}
		}
		a.autoSleepCancel()
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
		Trigger: a.runAutoSleep,
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

// replaced them.

func (a *App) stopAutoSleep() {

	a.autoSleepMutex.Lock()

	defer a.autoSleepMutex.Unlock()

	if a.autoSleepCancel != nil {

		a.autoSleepCancel()

		a.autoSleepCancel = nil

		a.autoSleepWatcher = nil

	}

	a.autoSleepWG.Wait()

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

	case errors.Is(err, context.DeadlineExceeded), result.TimedOut:

		log.Printf("Auto-sleep timed out with partial results: %v", err)

		a.emitTerminalAutoSleep(timedOutAutoSleepEvent(result.Results, "bulk power timeout reached"))

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
