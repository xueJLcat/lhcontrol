package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"lhcontrol/internal/autosleep"
	"lhcontrol/internal/bluetooth"
	"lhcontrol/internal/config"
	"lhcontrol/internal/station"

	"github.com/gofiber/fiber/v2"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type App struct {
	ctx                       context.Context
	config                    *config.Config
	stationManager            *station.Manager
	apiSettingsMutex          sync.Mutex
	autoSleepSettingsMutex    sync.Mutex
	bluetoothTimingMutex      sync.Mutex
	presenceSettingsMutex     sync.Mutex
	recoverySettingsMutex     sync.Mutex
	api                       *fiber.App
	apiStatusMutex            sync.RWMutex
	apiStatus                 APIStatus
	configLoadWarning         string
	configSaveWarning         string
	apiLifecycleMutex         sync.Mutex
	apiCancel                 context.CancelFunc
	apiWG                     sync.WaitGroup
	apiLifecycleGate          sync.Mutex
	listen                    func(string, string) (net.Listener, error)
	serveListener             func(net.Listener) error
	apiRetryDelay             time.Duration
	apiBindVerifyWait         time.Duration
	apiShutdownWait           time.Duration
	apiGeneration             uint64
	externalScanID            atomic.Uint64
	externalUpdateMutex       sync.Mutex
	externalUpdateID          atomic.Uint64
	externalOperationID       atomic.Uint64
	operationEventMutex       sync.Mutex
	externalOperationMutex    sync.RWMutex
	activeExternalOperations  map[uint64]string
	externalOperationRevision uint64
	shuttingDown              atomic.Bool
	// shutdownOnce keeps the shutdown sequence idempotent: Wails may have run
	// OnShutdown before returning a run error, in which case the fatal path's
	// explicit shutdown call must not repeat the bounded auto-sleep and API
	// waits for the same draining goroutines.
	shutdownOnce sync.Once
	// configReplayGeneration tracks the last observed config recovery
	// generation so a blocked-save recovery that replaced the startup
	// defaults with the persisted file contents replays the runtime side
	// effects (Bluetooth timing, auto-sleep watcher, API listener) exactly
	// once.
	configReplayGeneration atomic.Uint64
	// configReplayMutex serializes the replay itself so two setters that
	// observe the stale generation concurrently cannot both run it.
	configReplayMutex sync.Mutex
	// absentRetryLimitMutex guards absentRetryLimitApplied, the absent-station
	// retry limit the runtime last converged on. A blocked-save recovery that
	// restores a higher persisted limit must revive recovery entries that
	// exhausted under the lower one, mirroring the explicit setter.
	absentRetryLimitMutex   sync.Mutex
	absentRetryLimitApplied int
	// autoSleepMutex protects autoSleepCancel, autoSleepWatcher,
	// autoSleepRebuildTimer and the self-stop debt fields below.
	autoSleepMutex    sync.Mutex
	autoSleepCancel   context.CancelFunc
	autoSleepWatcher  *autosleep.Watcher
	autoSleepStopWait time.Duration
	autoSleepWG       sync.WaitGroup
	// autoSleepRebuildTimer holds the pending rebuild scheduled after a
	// watcher self-stop. It carries one autoSleepWG count that is released
	// either by the timer callback or by stopScheduledAutoSleepRebuildLocked
	// when the timer is cancelled before firing.
	autoSleepRebuildTimer *time.Timer
	// autoSleepRebuildFailures counts consecutive failed self-stop rebuilds
	// so a persistent failure cannot retry forever; a successful watcher
	// application or a new watcher self-stop resets the count.
	autoSleepRebuildFailures int
	// autoSleepSelfStopOwed remembers an unsettled sleep debt left behind by
	// a self-stopped watcher so the rebuilt watcher re-arms it instead of
	// dropping it until the watched process runs a full new session.
	autoSleepSelfStopOwed     bool
	autoSleepSelfStopClosedAt time.Time
	// autoSleepSelfStopCountdownAt remembers a closed-session countdown that
	// had not fired when a watcher self-stopped, so the rebuilt watcher
	// continues counting it down instead of restarting from idle and never
	// firing for the already-closed session.
	autoSleepSelfStopCountdownAt time.Time
	autoSleepActionID         atomic.Uint64
	autoSleepActionSlot       chan struct{}
	autoSleepSettledSession   time.Time
	scanForAutoSleep          func(context.Context) ([]station.StationInfo, error)
	setPowerForAutoSleep      func(context.Context, string) (station.BulkPowerResult, error)
	autoSleepIsRunning        func(string) (bool, error)
	autoSleepEventSink        func(autoSleepEvent)
}

func NewApp() *App {
	cfg := config.NewConfig()
	mgr := station.NewManager(cfg)
	autoSleepActionSlot := make(chan struct{}, 1)
	autoSleepActionSlot <- struct{}{}
	api := fiber.New(fiber.Config{
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 0,
		IdleTimeout:  30 * time.Second,
		BodyLimit:    apiBodyLimit,
		ErrorHandler: apiErrorHandler,
	})
	app := &App{
		config:                   cfg,
		stationManager:           mgr,
		api:                      api,
		activeExternalOperations: make(map[uint64]string),
		autoSleepActionSlot:      autoSleepActionSlot,
		apiStatus: APIStatus{
			Address:        "127.0.0.1:7575",
			Warnings:       []string{},
			ConfigWritable: true,
		},
		listen:        net.Listen,
		apiRetryDelay: 2 * time.Second,
	}
	app.serveListener = api.Listener
	app.scanForAutoSleep = mgr.ScanAndFetchStationsContext
	app.setPowerForAutoSleep = mgr.SetAllStationsPowerDetailedContext
	return app
}

// snapshotExternalStationUpdate assigns an update ID and captures the
// corresponding station snapshot as one ordered transaction. Every producer
// of externally-pushed station state uses this sequencer, so a lower ID can
// never describe a snapshot taken after a higher ID's snapshot.
func (a *App) snapshotExternalStationUpdate(snapshot func() []station.StationInfo) (uint64, []station.StationInfo) {
	a.externalUpdateMutex.Lock()
	defer a.externalUpdateMutex.Unlock()

	id := a.externalUpdateID.Add(1)
	stations := []station.StationInfo{}
	if snapshot != nil {
		stations = snapshot()
	}
	return id, stations
}

// startup is called when the app starts.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.shuttingDown.Store(false)

	// Use standard logger (already configured in main)
	log.Println("-----------------------------------------")
	log.Println("Application startup initiated.")
	log.Println("-----------------------------------------")

	// Load configuration before initializing Bluetooth so the very first
	// adapter-initialization retry cooldown honors the persisted
	// bluetoothInitRetrySeconds instead of the built-in default.
	configLoadErr := a.config.Load()
	a.setConfigLoadStatus(configLoadErr)
	if configLoadErr != nil {
		log.Printf("Error loading config: %v", configLoadErr)
	}
	// Seed the replay generation after the startup load: the explicit
	// applyBluetoothTiming/setAPIAddress/applyAutoSleep calls below already
	// derive the runtime state from the loaded configuration, so only later
	// blocked-save recoveries need a replay.
	a.configReplayGeneration.Store(a.config.RecoveryGeneration())
	// Seed the applied retry-limit baseline the same way: only a later
	// recovery that raises the limit revives exhausted absent-station
	// recovery entries.
	a.absentRetryLimitMutex.Lock()
	a.absentRetryLimitApplied = a.config.GetAbsentStationRetryLimit()
	a.absentRetryLimitMutex.Unlock()

	a.applyBluetoothTiming()

	if err := a.stationManager.Initialize(); err != nil {
		log.Printf("Error initializing Bluetooth: %v", err)
	}

	a.setAPIAddress(a.config.GetAPIListenAddress())
	a.applyAutoSleep(a.config.GetAutoSleep())

	registerAPIRoutes(a.api, a.stationManager, scanEventCallbacks{
		nextID: func() uint64 {
			return a.externalScanID.Add(1)
		},
		snapshotUpdate: a.snapshotExternalStationUpdate,
		nextOperationID: func() uint64 {
			return a.externalOperationID.Add(1)
		},
		started: func(event scanEvent) {
			if a.ctx != nil && !a.shuttingDown.Load() {
				runtime.EventsEmit(a.ctx, "external-scan-started", event)
			}
		},
		completed: func(event scanEvent) {
			if a.ctx != nil && !a.shuttingDown.Load() {
				runtime.EventsEmit(a.ctx, "external-scan-completed", event)
			}
		},
		failed: func(event scanEvent) {
			log.Printf("API background scan failed: %s", event.Error)
			if a.ctx != nil && !a.shuttingDown.Load() {
				runtime.EventsEmit(a.ctx, "external-scan-failed", event)
			}
		},
		cancelled: func(event scanEvent) {
			if a.ctx != nil && !a.shuttingDown.Load() {
				runtime.EventsEmit(a.ctx, "external-scan-cancelled", event)
			}
		},
		updated: func(event stationUpdateEvent) {
			if a.ctx != nil && !a.shuttingDown.Load() {
				runtime.EventsEmit(a.ctx, "external-stations-updated", event)
			}
		},
		operation: func(event externalOperationEvent) {
			a.emitTrackedOperation(event)
		},
	}, a.GetAPIStatus)
	a.startAPIServer()

	log.Println("Startup sequence complete.")
}

func (a *App) ScanAndFetchStations() ([]station.StationInfo, error) {
	return a.stationManager.ScanAndFetchStations()
}

func (a *App) IsScanning() bool {
	return a.stationManager.IsScanning()
}

func (a *App) GetScanStatus() station.ScanStatus {
	return a.stationManager.GetScanStatus()
}

func (a *App) StopScan() error {
	return a.stationManager.StopScan()
}

func (a *App) CheckAllStationStatuses() ([]station.StationInfo, error) {
	return a.stationManager.CheckAllStationStatuses()
}

func (a *App) GetCurrentStationInfo() []station.StationInfo {
	return a.stationManager.GetStationInfo()
}

func (a *App) setAPIStatus(running bool, err error) {
	a.apiStatusMutex.Lock()
	a.apiStatus.Running = running
	if err != nil {
		a.apiStatus.Error = err.Error()
	} else {
		a.apiStatus.Error = ""
	}
	a.apiStatusMutex.Unlock()
}

// setAPIAddress updates the advertised listen address. The server loop reads
// it on every bind attempt, so a hot restart picks the new address up.
func (a *App) setAPIAddress(address string) {
	a.apiStatusMutex.Lock()
	a.apiStatus.Address = address
	a.apiStatusMutex.Unlock()
}

func (a *App) setConfigLoadStatus(err error) {
	a.apiStatusMutex.Lock()
	defer a.apiStatusMutex.Unlock()
	persistenceErr := a.config.PersistenceError()
	if err == nil {
		a.configLoadWarning = ""
	} else {
		a.configLoadWarning = fmt.Sprintf("Configuration could not be loaded: %v", err)
	}
	a.refreshConfigStatusLocked(persistenceErr)
}

func (a *App) setConfigPersistenceStatus() {
	a.apiStatusMutex.Lock()
	persistenceErr := a.config.PersistenceError()
	if persistenceErr != nil {
		a.configSaveWarning = fmt.Sprintf("Configuration changes could not be saved: %v", persistenceErr)
	} else {
		a.configSaveWarning = ""
		// A successful save proves the configuration file is usable again:
		// the runtime state is now persisted to it, so a startup load failure
		// is no longer current and must not keep surfacing for the session.
		a.configLoadWarning = ""
	}
	a.refreshConfigStatusLocked(persistenceErr)
	a.apiStatusMutex.Unlock()
	// A blocked-save recovery can land inside the setter that triggered this
	// status refresh; re-apply the startup-derived runtime side effects after
	// releasing the status lock so the recovered configuration takes effect.
	a.replayRecoveredConfigRuntime()
}

// replayRecoveredConfigRuntime re-applies the runtime side effects that
// startup derived from the configuration when a blocked-save recovery has
// replaced the startup defaults with the persisted contents since the last
// observation. The generation check makes concurrent setters replay exactly
// once per recovery. Subsystems whose owning setter may itself be holding its
// serialization mutex (auto-sleep, API address) are skipped under TryLock
// contention: the owner converges the runtime itself on both its success and
// failure paths. The generation is committed only when neither subsystem was
// skipped, so a later setter re-verifies the convergence instead of trusting
// a replay that could not observe every subsystem.
func (a *App) replayRecoveredConfigRuntime() {
	generation := a.config.RecoveryGeneration()
	if a.configReplayGeneration.Load() == generation {
		return
	}
	// Serialize concurrent replays: two setters can observe the stale
	// generation at once, and the check-then-act gate would otherwise run the
	// shared convergence actions (timing, recovery settings, retry limits)
	// twice. TryLock-guarded subsystems keep their skip semantics inside.
	a.configReplayMutex.Lock()
	defer a.configReplayMutex.Unlock()
	if a.configReplayGeneration.Load() == generation {
		return
	}
	a.applyBluetoothTiming()
	a.stationManager.ApplyRecoverySettings()
	a.convergeAbsentStationRetryLimit()
	a.stationManager.ApplyPresenceMissThreshold()
	replayed := true
	if a.autoSleepSettingsMutex.TryLock() {
		// Re-applying settings the watcher already serves would cancel and
		// rebuild it, aborting a sleep action mid-flight and re-arming the
		// same session debt on the replacement. Keep the SetAutoSleepSettings
		// invariant: only diverged or missing runtime state is repaired.
		recovered := a.config.GetAutoSleep()
		if !a.autoSleepMatches(recovered) {
			a.applyAutoSleep(recovered)
		}
		a.autoSleepSettingsMutex.Unlock()
	} else {
		replayed = false
	}
	if a.apiSettingsMutex.TryLock() {
		a.convergeAPIListenerAfterRecovery()
		a.apiSettingsMutex.Unlock()
	} else {
		replayed = false
	}
	if replayed {
		log.Printf("Configuration recovered from a blocked load; re-applied runtime settings")
		a.configReplayGeneration.Store(generation)
	}
}

// convergeAPIListenerAfterRecovery restarts the API listener onto the
// recovered listen address when it still serves the pre-recovery one, or when
// it already targets the recovered address but is down (a bind-retry loop the
// explicit setter paths treat as repair-worthy the same way). A recovered
// address that cannot bind restores the previously serving address so the API
// stays reachable; the persisted address remains the recovered one and the
// listener converges onto it on the next address change.
func (a *App) convergeAPIListenerAfterRecovery() {
	if a.shuttingDown.Load() {
		return
	}
	configured := a.config.GetAPIListenAddress()
	status := a.GetAPIStatus()
	if status.Address == configured && status.Running {
		return
	}
	previous := status.Address
	a.setAPIAddress(configured)
	a.restartAPIServer()
	if bound, _ := a.waitForAPIBind(); bound {
		return
	}
	a.rollbackListener(previous, previous)
}

// convergeAbsentStationRetryLimit applies the persisted absent-station retry
// limit to the runtime baseline and revives exhausted absent-station recovery
// entries when the limit rises. A recovery that lowered the limit must not
// hand fresh retry budget back, so only a raise revives. Callers: the
// explicit retry-limit setter and the blocked-save recovery replay, keeping
// both paths on the same invariant.
func (a *App) convergeAbsentStationRetryLimit() {
	current := a.config.GetAbsentStationRetryLimit()
	a.absentRetryLimitMutex.Lock()
	previous := a.absentRetryLimitApplied
	a.absentRetryLimitApplied = current
	a.absentRetryLimitMutex.Unlock()
	if current > previous {
		a.stationManager.ReviveAbsentStationRecovery()
	}
}

func (a *App) refreshConfigStatusLocked(persistenceErr error) {
	// Warning text and writability must describe the same persistence snapshot.
	// A concurrent successful save cannot otherwise clear one between two
	// independent reads and briefly publish a contradictory API status.
	a.apiStatus.ConfigWritable = persistenceErr == nil
	a.apiStatus.Warnings = make([]string, 0, 2)
	if a.configLoadWarning != "" {
		a.apiStatus.Warnings = append(a.apiStatus.Warnings, a.configLoadWarning)
	}
	if a.configSaveWarning != "" && a.configSaveWarning != a.configLoadWarning {
		a.apiStatus.Warnings = append(a.apiStatus.Warnings, a.configSaveWarning)
	}
}

// GetAPIStatus assembles the server state and the external-operation snapshot
// from two independent locks. The two halves can reflect slightly different
// moments under concurrent requests; each half is internally consistent, and
// consumers gate merges with OperationRevision rather than assuming a single
// atomic instant.
func (a *App) GetAPIStatus() APIStatus {
	a.apiStatusMutex.RLock()
	status := a.apiStatus
	status.Warnings = append([]string{}, a.apiStatus.Warnings...)
	a.apiStatusMutex.RUnlock()
	status.ActiveOperations, status.OperationRevision = a.externalOperationSnapshot()
	return status
}

func (a *App) recordExternalOperation(event externalOperationEvent) externalOperationEvent {
	a.externalOperationMutex.Lock()
	defer a.externalOperationMutex.Unlock()
	if a.activeExternalOperations == nil {
		a.activeExternalOperations = make(map[uint64]string)
	}
	a.externalOperationRevision++
	event.Revision = a.externalOperationRevision
	if event.Phase == "started" {
		a.activeExternalOperations[event.ID] = event.Kind
	} else if event.Phase == "finished" {
		delete(a.activeExternalOperations, event.ID)
	}
	return event
}

func (a *App) emitTrackedOperation(event externalOperationEvent) {
	a.dispatchExternalOperation(event, func(sequenced externalOperationEvent) {
		if a.ctx != nil && !a.shuttingDown.Load() {
			runtime.EventsEmit(a.ctx, "external-operation", sequenced)
		}
	})
}

// dispatchExternalOperation keeps delta-event delivery in the same order as
// the revisions assigned to the authoritative operation set. The frontend
// intentionally discards older revisions, so allowing a later event to pass
// a delayed earlier event would permanently lose that earlier delta until a
// health poll repaired the full set.
func (a *App) dispatchExternalOperation(event externalOperationEvent, emit func(externalOperationEvent)) {
	a.operationEventMutex.Lock()
	defer a.operationEventMutex.Unlock()

	event = a.recordExternalOperation(event)
	if emit != nil {
		emit(event)
	}
}

// beginTrackedOperation makes non-HTTP background work visible through the
// same health snapshot used to recover HTTP operations that started before
// the desktop event listeners mounted. In particular, this prevents a UI
// reload from mistaking an automatic-sleep scan for a user-stoppable external
// scan when its earlier auto-sleep "started" event was missed.
func (a *App) beginTrackedOperation(kind string) func() {
	id := a.externalOperationID.Add(1)
	a.emitTrackedOperation(externalOperationEvent{ID: id, Phase: "started", Kind: kind})
	var once sync.Once
	return func() {
		once.Do(func() {
			a.emitTrackedOperation(externalOperationEvent{ID: id, Phase: "finished", Kind: kind})
		})
	}
}

func (a *App) externalOperationSnapshot() ([]OperationStatus, uint64) {
	a.externalOperationMutex.RLock()
	defer a.externalOperationMutex.RUnlock()
	operations := make([]OperationStatus, 0, len(a.activeExternalOperations))
	for id, kind := range a.activeExternalOperations {
		operations = append(operations, OperationStatus{ID: id, Kind: kind})
	}
	sort.Slice(operations, func(i, j int) bool { return operations[i].ID < operations[j].ID })
	return operations, a.externalOperationRevision
}

func (a *App) PowerOnStation(address string) error {
	log.Printf("Requesting Power ON for address %s", address)
	result, err := a.stationManager.SetStationPower(address, "on")
	return legacyPowerActionError("on", address, result, err)
}

func (a *App) PowerOffStation(address string) error {
	log.Printf("Requesting Power OFF for address %s", address)
	result, err := a.stationManager.SetStationPower(address, "sleep")
	return legacyPowerActionError("sleep", address, result, err)
}

func legacyPowerActionError(state, address string, result station.PowerActionResult, err error) error {
	var confirmationErr *bluetooth.PowerConfirmationError
	if errors.As(err, &confirmationErr) && result.CommandSent {
		log.Printf(
			"Legacy %s request for %s was accepted but could not be confirmed: %v",
			state,
			address,
			err,
		)
		return nil
	}
	return err
}

func (a *App) SetStationPower(address, state string) (station.PowerActionResult, error) {
	log.Printf("Requesting power state %s for address %s", state, address)
	result, err := a.stationManager.SetStationPower(address, state)
	return powerResultForWails(result, err)
}

func powerResultForWails(result station.PowerActionResult, err error) (station.PowerActionResult, error) {
	var confirmationErr *bluetooth.PowerConfirmationError
	if errors.As(err, &confirmationErr) && result.CommandSent {
		// Wails discards return values when a Go error is returned. Preserve the
		// structured command-sent/readback-failed result for the desktop UI. A
		// confirmation error without a sent command carries no structured state
		// to preserve; surfacing it keeps the UI from treating an empty result
		// as a successful operation, matching the HTTP response rule.
		return result, nil
	}
	return result, err
}

func (a *App) IdentifyStation(address string) error {
	log.Printf("Requesting identify for address %s", address)
	return a.stationManager.IdentifyStation(address)
}

func (a *App) RefreshStationCapabilities(address string) (station.StationInfo, error) {
	log.Printf("Refreshing capabilities for address %s", address)
	return a.stationManager.RefreshStationCapabilities(address)
}

func (a *App) SetStationChannel(address string, channel int, allowUnknownConflictRisk bool) (station.ChannelChangeResult, error) {
	log.Printf("Requesting channel %d for address %s", channel, address)
	result, err := a.stationManager.SetStationChannel(address, channel, allowUnknownConflictRisk)
	return channelResultForWails(result, err)
}

func channelResultForWails(result station.ChannelChangeResult, err error) (station.ChannelChangeResult, error) {
	if err != nil && result.CommandSent && !result.Confirmed {
		// Wails discards structured return values when a Go error is returned.
		return result, nil
	}
	return result, err
}

func (a *App) PowerOnAllStations() error {
	return a.stationManager.PowerOnAllStations()
}

func (a *App) PowerOffAllStations() error {
	return a.stationManager.PowerOffAllStations()
}

func (a *App) SetAllStationsPower(state string) error {
	log.Printf("Requesting power state %s for all known stations", state)
	return a.stationManager.SetAllStationsPower(state)
}

func (a *App) SetAllStationsPowerDetailed(state string) (station.BulkPowerResult, error) {
	log.Printf("Requesting detailed power state %s for all known stations", state)
	return a.stationManager.SetAllStationsPowerDetailed(state)
}

func (a *App) CancelBulkPower() error {
	log.Printf("Requesting cancellation of the active bulk power operation")
	return a.stationManager.CancelBulkPower()
}

// RenameStation is the legacy name-based rename kept for compatibility: it
// renames every station sharing the same original factory name. New
// integrations should call RenameStationByAddress instead.
func (a *App) RenameStation(originalName string, newName string) error {
	log.Printf("Renaming %s to %s", originalName, newName)
	err := a.stationManager.RenameStation(originalName, newName)
	a.setConfigPersistenceStatus()
	return err
}

func (a *App) RenameStationByAddress(address string, newName string) error {
	log.Printf("Renaming station at %s to %s", address, newName)
	err := a.stationManager.RenameStationByAddress(address, newName)
	a.setConfigPersistenceStatus()
	return err
}

func (a *App) SaveConfig() error {
	err := a.stationManager.SaveConfig()
	a.setConfigPersistenceStatus()
	return err
}

// ListBluetoothAdapters returns the Bluetooth radios known to the operating
// system, in enumeration order.
func (a *App) ListBluetoothAdapters() ([]bluetooth.AdapterInfo, error) {
	return bluetooth.ListAdapters()
}

// autoSleepEvent is the frontend payload for the "auto-sleep" event. Phase is
// "started", "completed", "cancelled" (the watched session restarted),
// "skipped" (user was operating Bluetooth) or "failed".
