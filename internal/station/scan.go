package station

import (
	"context"
	"errors"
	"fmt"
	"lhcontrol/internal/bluetooth"
	"log"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"
)

func (m *Manager) ScanAndFetchStations() ([]StationInfo, error) {
	return m.ScanAndFetchStationsContext(context.Background())
}

// ScanAndFetchStationsContext runs a synchronous scan that can be cancelled
// independently of the Manager lifetime. StopScan and BeginShutdown still
// cancel the same published scan lifecycle.
func (m *Manager) ScanAndFetchStationsContext(ctx context.Context) ([]StationInfo, error) {
	if err := m.beginScanContext(ctx, ScanCallbacks{}); err != nil {
		return m.GetStationInfo(), err
	}
	scanCtx := m.currentScanContext()
	_, found, err := m.scanAndFetchStationsSafely(scanCtx)
	m.scanLifecycleMutex.Lock()
	lifecycle := m.scanLifecycle
	m.scanLifecycleMutex.Unlock()
	if lifecycle != nil {
		close(lifecycle.startedDone)
	}
	m.finishScan(found, err, ScanCallbacks{})()
	// finishScan releases the global operation, so a queued read-only recovery
	// may already have produced a newer snapshot than scanAndFetchStations.
	return m.GetStationInfo(), err
}
func (m *Manager) beginScan(callbacks ScanCallbacks) error {
	return m.beginScanContext(context.Background(), callbacks)
}
func (m *Manager) beginScanContext(ctx context.Context, callbacks ScanCallbacks) error {
	lifecycle, err := m.reserveScan(ctx)
	if err != nil {
		return err
	}
	operationAcquired, err := m.prepareScan(lifecycle)
	if err == nil {
		return nil
	}
	cancelled := lifecycle.ctx.Err() != nil || m.shuttingDown.Load()
	if m.shuttingDown.Load() {
		err = ErrShuttingDown
	} else if cancelled {
		err = bluetooth.ErrScanCancelled
	}
	m.abortScanStart(lifecycle, err, cancelled, operationAcquired)
	m.clearScanLifecycle(lifecycle)
	return err
}
func (m *Manager) reserveScan(parent context.Context) (*scanLifecycle, error) {
	if parent == nil {
		parent = context.Background()
	}
	if parent.Err() != nil {
		return nil, bluetooth.ErrScanCancelled
	}
	m.scanTransitionMutex.Lock()
	if m.shuttingDown.Load() {
		m.scanTransitionMutex.Unlock()
		return nil, ErrShuttingDown
	}
	if m.hasForegroundOperation() {
		m.scanTransitionMutex.Unlock()
		return nil, ErrOperationInProgress
	}
	m.scanLifecycleMutex.Lock()
	previousLifecycle := m.scanLifecycle
	m.scanLifecycleMutex.Unlock()
	if previousLifecycle != nil {
		m.scanTransitionMutex.Unlock()
		return nil, ErrOperationInProgress
	}
	ctx, cancel := context.WithCancel(parent)
	statusID := m.markScanStarted()
	lifecycle := &scanLifecycle{
		ctx:         ctx,
		cancel:      cancel,
		done:        make(chan struct{}),
		startedDone: make(chan struct{}),
		statusID:    statusID,
	}
	m.scanLifecycleMutex.Lock()
	m.scanLifecycle = lifecycle
	m.scanLifecycleMutex.Unlock()
	m.isScanning.Store(true)
	// Publish the cancellable lifecycle before adapter initialization or any
	// other potentially blocking start work. StopScan can then record the
	// user's intent immediately instead of racing the platform scan goroutine.
	m.scanTransitionMutex.Unlock()
	if m.scanLifecycleStartHook != nil {
		m.scanLifecycleStartHook()
	}
	return lifecycle, nil
}
// prepareScan reserves the global operation and readies the adapter for a
// scan. A panic during adapter preparation is converted into a scan failure:
// without a terminal error the callers would publish no terminal lifecycle
// state, leaving isScanning set and scanLifecycle populated forever and
// wedging every later scan, StopScan, background recovery, and shared
// configuration operation. The named operationAcquired return lets the
// deferred recover report whether the global operation was already acquired
// so the abort path releases it.
func (m *Manager) prepareScan(lifecycle *scanLifecycle) (operationAcquired bool, returnErr error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			returnErr = fmt.Errorf("scan preparation panicked: %v\n%s", recovered, debug.Stack())
			log.Printf("Recovered panic: %v", returnErr)
		}
	}()
	ctx := lifecycle.ctx
	if err := m.beginForegroundGlobalOperationContext(ctx); err != nil {
		return false, err
	}
	operationAcquired = true
	if err := m.ensureReady(); err != nil {
		return true, err
	}
	if m.scanReadyHook != nil {
		m.scanReadyHook()
	}
	m.lifecycleMutex.Lock()
	if m.shuttingDown.Load() {
		m.lifecycleMutex.Unlock()
		return true, ErrShuttingDown
	}
	if ctx.Err() != nil {
		m.lifecycleMutex.Unlock()
		return true, bluetooth.ErrScanCancelled
	}
	m.lifecycleMutex.Unlock()
	m.markScanRunning()
	return true, nil
}

// abortScanStart publishes a terminal lifecycle state before scan processing
// starts, allowing StopScan to wait safely during adapter initialization.
func (m *Manager) abortScanStart(lifecycle *scanLifecycle, err error, cancelled, operationAcquired bool) {
	m.scanTransitionMutex.Lock()
	defer m.scanTransitionMutex.Unlock()
	m.isScanning.Store(false)
	if operationAcquired {
		m.endForegroundGlobalOperation()
	}
	if cancelled {
		m.markScanFinished(0, bluetooth.ErrScanCancelled)
	} else {
		m.markScanFinished(0, err)
	}
	close(lifecycle.done)
}
func (m *Manager) clearScanLifecycle(lifecycle *scanLifecycle) {
	m.scanLifecycleMutex.Lock()
	if m.scanLifecycle == lifecycle {
		m.scanLifecycle = nil
	}
	m.scanLifecycleMutex.Unlock()
	// Release the scan's context tree on every terminal path. StopScan and
	// BeginShutdown already cancel it, but a scan that completes on its own
	// would otherwise leave the derived context (and anything attached to it)
	// alive for the lifetime of the parent. Cancel is idempotent.
	lifecycle.cancel()
}
func (m *Manager) finishScan(found int, err error, callbacks ScanCallbacks) func() {
	m.scanTransitionMutex.Lock()
	m.isScanning.Store(false)
	m.endForegroundGlobalOperation()
	m.markScanFinished(found, err)
	m.scanLifecycleMutex.Lock()
	lifecycle := m.scanLifecycle
	m.scanLifecycleMutex.Unlock()
	if lifecycle != nil {
		close(lifecycle.done)
	}
	m.scanTransitionMutex.Unlock()
	if lifecycle == nil {
		return func() {}
	}
	statusID := lifecycle.statusID
	return func() {
		// StartScan begins scan processing before delivering Started. Keep terminal
		// notifications behind that callback without making processing wait for it.
		<-lifecycle.startedDone
		// Release the scan slot before running terminal callbacks: the scan is
		// already in a terminal state at this point, and holding the lifecycle
		// while callbacks execute would make new scans fail with Busy even
		// though GetScanStatus reports completed/failed/cancelled. A callback
		// that hangs can then no longer wedge every later scan on this manager.
		m.clearScanLifecycle(lifecycle)
		if errors.Is(err, bluetooth.ErrScanCancelled) || errors.Is(err, context.Canceled) {
			if callbacks.Cancelled != nil {
				if callbackErr := runSafely("scan cancelled callback", func() error {
					callbacks.Cancelled(statusID)
					return nil
				}); callbackErr != nil {
					log.Printf("Scan cancelled callback failed: %v", callbackErr)
				}
			}
		} else if err != nil {
			if callbacks.Failed != nil {
				if callbackErr := runSafely("scan failure callback", func() error {
					callbacks.Failed(statusID, err)
					return nil
				}); callbackErr != nil {
					log.Printf("Scan failure callback failed: %v", callbackErr)
				}
			}
		} else if callbacks.Completed != nil {
			if callbackErr := runSafely("scan completion callback", func() error {
				// Use the latest authoritative cache after releasing the scan
				// lock instead of the snapshot captured before recovery could run.
				callbacks.Completed(statusID, m.GetStationInfo())
				return nil
			}); callbackErr != nil {
				log.Printf("Scan completion callback failed: %v", callbackErr)
			}
		}
	}
}
func fallbackStationName(address string) string {
	compact := strings.ReplaceAll(address, ":", "")
	if len(compact) > 8 {
		compact = compact[len(compact)-8:]
	}
	return "LHB-" + strings.ToUpper(compact)
}
// scanAndFetchStations runs one scan in three phases: release cached
// connections so stations advertise again, merge the discovery results into
// the fleet, then read initial power/channel values within a bounded phase.
func (m *Manager) scanAndFetchStations(ctx context.Context) ([]StationInfo, int, error) {
	if err := scanContextError(ctx); err != nil {
		return m.GetStationInfo(), 0, err
	}
	unreliablePresence, err := m.releaseStationsForScan(ctx)
	if err != nil {
		return m.GetStationInfo(), 0, err
	}
	discoveredValues, err := m.bluetoothOps.scanForDurationContext(ctx, m.config.ScanDuration())
	if errors.Is(err, bluetooth.ErrScanCancelled) || errors.Is(err, context.Canceled) {
		return m.GetStationInfo(), 0, bluetooth.ErrScanCancelled
	} else if err != nil {
		m.observeBluetoothError(err)
		return m.GetStationInfo(), 0, fmt.Errorf("bluetooth scan failed: %w", err)
	}
	// The BLE scan completed its full duration, so the discovery results are
	// merged even if the context was cancelled during the stop handshake; only
	// the optional initial reads are skipped below.
	stationsToFetch := m.mergeDiscoveredStations(discoveredValues, unreliablePresence)
	if len(stationsToFetch) > 0 && scanContextError(ctx) == nil {
		if err := m.runInitialScanReads(ctx, stationsToFetch); err != nil {
			// The discovery results above were already merged; only the
			// optional initial reads were interrupted. Report the merged
			// count instead of claiming nothing was found.
			return m.GetStationInfo(), len(discoveredValues), err
		}
	}
	return m.GetStationInfo(), len(discoveredValues), nil
}

// releaseStationsForScan releases cached GATT connections before a fresh scan:
// a Lighthouse commonly stops advertising while a GATT connection is active,
// so previously discovered stations could not advertise again and participate
// in presence and channel-conflict detection. Stations whose connection could
// not be released are returned (lowercased address) so the merge phase marks
// their presence uncertain.
func (m *Manager) releaseStationsForScan(ctx context.Context) (map[string]struct{}, error) {
	m.stationsMutex.RLock()
	connectedStations := make([]*bluetooth.BaseStation, 0, len(m.stations))
	for _, stationPtr := range m.stations {
		if stationPtr != nil {
			connectedStations = append(connectedStations, stationPtr)
		}
	}
	m.stationsMutex.RUnlock()
	releaseErrors := make([]error, 0)
	unreliablePresence := make(map[string]struct{})
	for index, stationPtr := range connectedStations {
		if err := scanContextError(ctx); err != nil {
			return nil, err
		}
		address := stationPtr.Snapshot().Address
		releaseErr := m.releaseStationForScanBounded(stationPtr)
		if releaseErr == nil {
			continue
		}
		m.observeBluetoothError(releaseErr)
		releaseErrors = append(releaseErrors, fmt.Errorf("%s: %w", address, releaseErr))
		unreliablePresence[strings.ToLower(address)] = struct{}{}
		if bluetooth.IsAdapterUnavailable(releaseErr) {
			// The adapter itself is gone: every remaining cached connection
			// would pay the same bounded cleanup for the same outcome. Mark
			// the rest of the fleet unreliable (presence uncertain) and stop
			// issuing releases; recovery re-reads them once the adapter
			// returns.
			for _, remaining := range connectedStations[index+1:] {
				unreliablePresence[strings.ToLower(remaining.Snapshot().Address)] = struct{}{}
			}
			break
		}
	}
	if len(releaseErrors) > 0 {
		m.addScanWarning(fmt.Sprintf(
			"%d station connection(s) could not be fully released before scanning: %v",
			len(releaseErrors),
			errors.Join(releaseErrors...),
		))
	}
	if err := scanContextError(ctx); err != nil {
		return nil, err
	}
	return unreliablePresence, nil
}

// mergeDiscoveredStations applies one scan's discovery results to the fleet:
// marks missed stations, registers newly discovered stations, records revivals
// (re-arming recovery for genuinely absent returners), and returns the
// disconnected stations that need an initial state read.
func (m *Manager) mergeDiscoveredStations(
	discoveredValues []bluetooth.DiscoveredStation,
	unreliablePresence map[string]struct{},
) []*bluetooth.BaseStation {
	stationsToFetch := make([]*bluetooth.BaseStation, 0)
	scanTime := time.Now()
	m.stationsMutex.Lock()
	// Snapshot absence before the miss marking below: a station seen this
	// round can still cross the miss threshold during MarkMissed, and MarkSeen
	// would then report an absent-to-present transition that never happened.
	// Only genuinely absent stations are revivals worth re-arming recovery
	// for; the others already participate in this scan's own initial reads.
	previouslyAbsent := make(map[*bluetooth.BaseStation]bool, len(m.stations))
	for _, stationPtr := range m.stations {
		if stationPtr == nil {
			continue
		}
		if _, unreliable := unreliablePresence[strings.ToLower(stationPtr.Snapshot().Address)]; unreliable {
			// An unreliable station still needs its absence snapshotted: when
			// its cached connection could not be released, a genuinely absent
			// station this scan sees advertising again must still count as a
			// revival so recovery is re-armed below.
			if !stationPtr.Snapshot().Present {
				previouslyAbsent[stationPtr] = true
			}
			stationPtr.MarkPresenceUncertain()
			continue
		}
		if !stationPtr.Snapshot().Present {
			previouslyAbsent[stationPtr] = true
		}
		stationPtr.MarkMissed()
	}
	revivedStations := make([]*bluetooth.BaseStation, 0)
	for _, currentScanStation := range discoveredValues {
		addrStr := currentScanStation.Address.String()
		if existingStation, found := m.stations[addrStr]; found {
			if currentScanStation.Name != "" {
				existingStation.UpdateName(currentScanStation.Name)
			}
			if existingStation.MarkSeen(scanTime) && previouslyAbsent[existingStation] {
				revivedStations = append(revivedStations, existingStation)
			}
			if !existingStation.Snapshot().Connected {
				stationsToFetch = append(stationsToFetch, existingStation)
			}
		} else {
			name := currentScanStation.Name
			if name == "" {
				name = fallbackStationName(addrStr)
			}
			newStationPtr := &bluetooth.BaseStation{
				Name:          name,
				Address:       currentScanStation.Address,
				PowerState:    bluetooth.PowerStateUnknown,
				RawPowerState: bluetooth.RawPowerStateUnknown,
				Channel:       bluetooth.ChannelUnknown,
				Present:       true,
				LastSeenAt:    scanTime,
			}
			m.stations[addrStr] = newStationPtr
			stationsToFetch = append(stationsToFetch, newStationPtr)
		}
	}
	m.stationsMutex.Unlock()
	// A station that returns after its absent recovery was pruned or exhausted
	// has no retry entry left, and the initial read below can still be skipped
	// (a cancellation landing in the merge window, or an already connected
	// station). Re-arm recovery for a revived but disconnected station so it
	// does not sit untracked until the next status poll or user action.
	for _, stationPtr := range revivedStations {
		snapshot := stationPtr.Snapshot()
		if !snapshot.Connected {
			m.rebaseRecoveryForRevivedStation(snapshot.Address)
		}
	}
	return stationsToFetch
}

type initialScanReadResult struct {
	address               string
	station               *bluetooth.BaseStation
	err                   error
	phaseDeadlineExceeded bool
	// cancelSkipped marks a read whose only outcome was the scan's own
	// cancellation. The station was never read, so it must not be booked as a
	// success (no failure to clear), a failure, or a pending recovery: a
	// user-cancelled scan stays clean instead of emitting warnings and
	// background reads for work that never ran. Real transport failures and
	// phase deadlines are classified separately and keep their handling.
	cancelSkipped bool
}

// runInitialScanReads reads initial power/channel values for freshly seen
// disconnected stations within one bounded phase. It returns nil when the
// phase ran (results recorded as side effects and warnings) or the scan
// cancellation error when the phase was interrupted.
func (m *Manager) runInitialScanReads(ctx context.Context, stationsToFetch []*bluetooth.BaseStation) error {
	phaseContext, cancelPhase := context.WithTimeout(ctx, m.scanReadPhaseTimeoutDuration())
	defer cancelPhase()
	var wg sync.WaitGroup
	readResults := make([]initialScanReadResult, len(stationsToFetch))
	semaphore := make(chan struct{}, 2)
	for index, stationToFetch := range stationsToFetch {
		wg.Add(1)
		go func(resultIndex int, ptr *bluetooth.BaseStation) {
			defer wg.Done()
			readResults[resultIndex].address = ptr.Snapshot().Address
			readResults[resultIndex].station = ptr
			select {
			case semaphore <- struct{}{}:
			case <-phaseContext.Done():
				if ctx.Err() != nil {
					readResults[resultIndex].err = bluetooth.ErrScanCancelled
				} else {
					readResults[resultIndex].err = fmt.Errorf("initial read phase deadline exceeded: %w", phaseContext.Err())
					readResults[resultIndex].phaseDeadlineExceeded = true
				}
				return
			}
			defer func() { <-semaphore }()
			if err := scanContextError(ctx); err != nil {
				readResults[resultIndex].err = err
				return
			}
			readContext, cancelRead := context.WithTimeout(phaseContext, m.initialReadTimeoutDuration())
			defer cancelRead()
			// A per-station read can exhaust its own budget at the same instant
			// the whole phase deadline lands. Attribute the failure to its real
			// owner by comparing deadlines: only when the phase deadline is the
			// binding constraint is this a phase-wide timeout; otherwise the
			// structured per-station handling below must run so the failure is
			// recorded and retried instead of being folded into the phase.
			readDeadline, hasReadDeadline := readContext.Deadline()
			phaseDeadline, hasPhaseDeadline := phaseContext.Deadline()
			ownBudget := hasReadDeadline && hasPhaseDeadline && readDeadline.Before(phaseDeadline)
			readResults[resultIndex].err = runSafely("initial station read", func() error {
				return m.bluetoothOps.fetchInitialPowerState(readContext, ptr)
			})
			if ctx.Err() == nil && !ownBudget && errors.Is(phaseContext.Err(), context.DeadlineExceeded) &&
				errors.Is(readResults[resultIndex].err, context.DeadlineExceeded) {
				readResults[resultIndex].phaseDeadlineExceeded = true
			}
		}(index, stationToFetch)
	}
	wg.Wait()
	interrupted := scanContextError(ctx)
	if interrupted != nil {
		// The discovery results were already merged into the fleet before the
		// phase stopped, so the read outcomes are still booked even though the
		// scan was cancelled. A read that only observed the cancellation itself
		// (the gate, the pre-read guard, or an in-flight read torn down by the
		// stop) is a pure interruption and stays clean: no warning, no failure,
		// no recovery, matching a user-cancelled scan's contract. A read that
		// hit a genuine transport fault before the stop landed keeps its real
		// error so the disconnect/backoff bookkeeping below still runs instead
		// of being silently dropped along with the interruption.
		for index := range readResults {
			result := &readResults[index]
			if result.err == nil || result.phaseDeadlineExceeded {
				continue
			}
			if errors.Is(result.err, bluetooth.ErrScanCancelled) || isPureContextError(result.err) {
				result.err = nil
				result.cancelSkipped = true
			}
		}
	}
	m.recordInitialScanReadResults(readResults)
	return interrupted
}

// recordInitialScanReadResults classifies each initial read outcome: success
// clears recovery, phase timeouts defer a refresh, structured failures record
// per-field recovery, and everything else goes through the generic
// failure path. Failures are aggregated into one scan warning.
func (m *Manager) recordInitialScanReadResults(readResults []initialScanReadResult) {
	sort.Slice(readResults, func(i, j int) bool {
		return strings.ToLower(readResults[i].address) < strings.ToLower(readResults[j].address)
	})
	readErrors := make([]error, 0)
	for _, result := range readResults {
		if result.cancelSkipped {
			continue
		}
		if result.err == nil {
			m.clearStatusFailure(result.address)
			continue
		}
		if result.phaseDeadlineExceeded {
			m.noteStatusRefreshPending(result.address)
			readErrors = append(readErrors, fmt.Errorf("%s: %w", result.address, result.err))
			continue
		}
		m.observeBluetoothError(result.err)
		var initialErr *bluetooth.InitialReadError
		if errors.As(result.err, &initialErr) {
			m.recordStructuredReadResult(result.station, result.address, initialErr.Power, initialErr.Channel)
			m.recordMetadataReadResult(result.address, initialErr.Metadata)
			if initialErr.Power == nil && initialErr.Channel == nil {
				// Device information is optional. Retry it in the background
				// without turning an otherwise healthy scan into a warning.
				continue
			}
			result.err = &bluetooth.InitialReadError{
				Power:   initialErr.Power,
				Channel:   initialErr.Channel,
			}
		} else if errors.Is(result.err, context.DeadlineExceeded) && !bluetooth.RequiresReconnect(result.err) {
			// A per-station read-budget deadline is not evidence the link
			// is broken, matching recoverOneStation and the status-refresh
			// rule. Back off and let recovery retry instead of disconnecting
			// a possibly-healthy station.
			m.noteStatusFailure(result.address)
		} else {
			m.recordUnstructuredStationFailure(result.station, result.address, result.err)
		}
		readErrors = append(readErrors, fmt.Errorf("%s: %w", result.address, result.err))
	}
	if len(readErrors) > 0 {
		m.addScanWarning(fmt.Sprintf(
			"%d station(s) were discovered, but some initial values could not be read: %v",
			len(readErrors),
			errors.Join(readErrors...),
		))
	}
}
func scanContextError(ctx context.Context) error {
	if ctx != nil && ctx.Err() != nil {
		return bluetooth.ErrScanCancelled
	}
	return nil
}
func (m *Manager) currentScanContext() context.Context {
	m.scanLifecycleMutex.Lock()
	defer m.scanLifecycleMutex.Unlock()
	if m.scanLifecycle == nil {
		return context.Background()
	}
	return m.scanLifecycle.ctx
}

// stopScanWaitLimit bounds the StopScan wait. Scan processing can stall in an
// adapter call that ignores cancellation (adapter initialization before the
// platform scan starts, in particular); the caller must receive a result even
// then. The cancellation stays in effect and status polling observes the
// terminal state once the blocked call returns.
const stopScanWaitLimit = 30 * time.Second

// StopScan cancels the active scan and waits until scan processing has
// finished. Terminal callbacks are delivered afterwards so callbacks can
// safely call StopScan themselves. Repeated and no-op calls are safe.
// The scan's own outcome is reported through GetScanStatus and the scan
// callbacks; StopScan only reports whether the stop completed, so a scan
// that already failed on its own is not surfaced as a stop failure. When
// scan processing does not finish within the bounded wait, StopScan reports
// ErrScanStopTimeout instead of hanging indefinitely.
func (m *Manager) StopScan() error {
	m.scanLifecycleMutex.Lock()
	lifecycle := m.scanLifecycle
	m.scanLifecycleMutex.Unlock()
	if lifecycle == nil {
		// A scan may have acquired the transition lock but not yet published
		// its lifecycle. Wait for that short publication window, then recheck.
		m.scanTransitionMutex.Lock()
		m.scanLifecycleMutex.Lock()
		lifecycle = m.scanLifecycle
		m.scanLifecycleMutex.Unlock()
		if lifecycle != nil {
			lifecycle.cancel()
		}
		m.scanTransitionMutex.Unlock()
		if lifecycle == nil {
			return nil
		}
	} else {
		lifecycle.cancel()
	}
	limit := m.stopScanTimeout
	if limit <= 0 {
		limit = stopScanWaitLimit
	}
	stopTimer := time.NewTimer(limit)
	defer stopTimer.Stop()
	select {
	case <-lifecycle.done:
		return nil
	case <-stopTimer.C:
		log.Printf("Scan stop waited %s without scan processing finishing; cancellation remains pending", limit)
		return ErrScanStopTimeout
	}
}
func (m *Manager) IsScanning() bool {
	return m.isScanning.Load()
}
func (m *Manager) markScanStarted() uint64 {
	m.scanStatusMutex.Lock()
	m.scanStatusID++
	statusID := m.scanStatusID
	m.scanStatus = ScanStatus{
		ID:        statusID,
		State:     "starting",
		StartedAt: time.Now().Format(time.RFC3339Nano),
		Warnings:  []string{},
	}
	m.scanStatusMutex.Unlock()
	return statusID
}
func (m *Manager) markScanRunning() {
	m.scanStatusMutex.Lock()
	if m.scanStatus.State == "starting" {
		m.scanStatus.State = "running"
	}
	m.scanStatusMutex.Unlock()
}
func (m *Manager) addScanWarning(warning string) {
	m.scanStatusMutex.Lock()
	m.scanStatus.Warnings = append(m.scanStatus.Warnings, warning)
	m.scanStatusMutex.Unlock()
}

// markScanFinished records only the terminal scan status. It intentionally
// takes no station list: the snapshot is not used here, and building one
// (GetStationInfo) inside the transition lock would needlessly block every
// scan transition and StopScan behind a full-fleet snapshot.
func (m *Manager) markScanFinished(found int, err error) {
	m.scanStatusMutex.Lock()
	m.scanStatus.CompletedAt = time.Now().Format(time.RFC3339Nano)
	m.scanStatus.Found = found
	if errors.Is(err, bluetooth.ErrScanCancelled) || errors.Is(err, context.Canceled) {
		m.scanStatus.State = "cancelled"
		m.scanStatus.Error = ""
	} else if err != nil {
		m.scanStatus.State = "failed"
		m.scanStatus.Error = err.Error()
	} else {
		m.scanStatus.State = "completed"
		m.scanStatus.Error = ""
	}
	m.scanStatusMutex.Unlock()
}
func (m *Manager) GetScanStatus() ScanStatus {
	m.scanStatusMutex.RLock()
	defer m.scanStatusMutex.RUnlock()
	status := m.scanStatus
	status.Warnings = append([]string(nil), m.scanStatus.Warnings...)
	return status
}

// StartScan reserves the Bluetooth adapter, starts scan processing, then emits
// Started synchronously. Terminal callbacks run after processing has finished.
func (m *Manager) StartScan(callbacks ScanCallbacks) error {
	lifecycle, err := m.reserveScan(context.Background())
	if err != nil {
		return err
	}
	m.asyncScanWg.Add(1)
	m.scanCallbackWg.Add(1)
	go func() {
		operationAcquired, prepareErr := m.prepareScan(lifecycle)
		if prepareErr != nil {
			cancelled := lifecycle.ctx.Err() != nil || m.shuttingDown.Load()
			if m.shuttingDown.Load() {
				prepareErr = ErrShuttingDown
			} else if cancelled {
				prepareErr = bluetooth.ErrScanCancelled
			}
			m.abortScanStart(lifecycle, prepareErr, cancelled, operationAcquired)
			m.asyncScanWg.Done()
			defer m.scanCallbackWg.Done()
			<-lifecycle.startedDone
			// Same rule as finishScan: release the slot before delivering
			// callbacks so a slow callback cannot block the next scan.
			m.clearScanLifecycle(lifecycle)
			m.deliverAbortedScanCallback(lifecycle.statusID, prepareErr, cancelled, callbacks)
			return
		}
		ctx := lifecycle.ctx
		_, found, err := m.scanAndFetchStationsSafely(ctx)
		deliverTerminal := m.finishScan(found, err, callbacks)
		m.asyncScanWg.Done()
		defer m.scanCallbackWg.Done()
		deliverTerminal()
	}()
	if callbacks.Started != nil {
		if callbackErr := runSafely("scan started callback", func() error {
			callbacks.Started()
			return nil
		}); callbackErr != nil {
			log.Printf("Scan started callback failed: %v", callbackErr)
		}
	}
	close(lifecycle.startedDone)
	return nil
}
func (m *Manager) deliverAbortedScanCallback(statusID uint64, err error, cancelled bool, callbacks ScanCallbacks) {
	if cancelled {
		if callbacks.Cancelled != nil {
			if callbackErr := runSafely("scan cancelled callback", func() error {
				callbacks.Cancelled(statusID)
				return nil
			}); callbackErr != nil {
				log.Printf("Scan cancelled callback failed: %v", callbackErr)
			}
		}
		return
	}
	if callbacks.Failed != nil {
		if callbackErr := runSafely("scan failure callback", func() error {
			callbacks.Failed(statusID, err)
			return nil
		}); callbackErr != nil {
			log.Printf("Scan failure callback failed: %v", callbackErr)
		}
	}
}
