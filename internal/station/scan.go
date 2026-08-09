package station

import (
	"context"
	"errors"
	"fmt"
	"lhcontrol/internal/bluetooth"
	"log"
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
	lifecycle := &scanLifecycle{
		ctx:         ctx,
		cancel:      cancel,
		done:        make(chan struct{}),
		startedDone: make(chan struct{}),
	}
	m.scanLifecycleMutex.Lock()
	m.scanLifecycle = lifecycle
	m.scanLifecycleMutex.Unlock()
	m.isScanning.Store(true)
	m.markScanStarted()
	// Publish the cancellable lifecycle before adapter initialization or any
	// other potentially blocking start work. StopScan can then record the
	// user's intent immediately instead of racing the platform scan goroutine.
	m.scanTransitionMutex.Unlock()
	if m.scanLifecycleStartHook != nil {
		m.scanLifecycleStartHook()
	}
	return lifecycle, nil
}
func (m *Manager) prepareScan(lifecycle *scanLifecycle) (bool, error) {
	ctx := lifecycle.ctx
	if err := m.beginForegroundGlobalOperationContext(ctx); err != nil {
		return false, err
	}
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
	return func() {
		// StartScan begins scan processing before delivering Started. Keep terminal
		// notifications behind that callback without making processing wait for it.
		<-lifecycle.startedDone
		defer m.clearScanLifecycle(lifecycle)
		if errors.Is(err, bluetooth.ErrScanCancelled) || errors.Is(err, context.Canceled) {
			if callbacks.Cancelled != nil {
				if callbackErr := runSafely("scan cancelled callback", func() error {
					callbacks.Cancelled()
					return nil
				}); callbackErr != nil {
					log.Printf("Scan cancelled callback failed: %v", callbackErr)
				}
			}
		} else if err != nil {
			if callbacks.Failed != nil {
				if callbackErr := runSafely("scan failure callback", func() error {
					callbacks.Failed(err)
					return nil
				}); callbackErr != nil {
					log.Printf("Scan failure callback failed: %v", callbackErr)
				}
			}
		} else if callbacks.Completed != nil {
			if callbackErr := runSafely("scan completion callback", func() error {
				// Use the latest authoritative cache after releasing the scan
				// lock instead of the snapshot captured before recovery could run.
				callbacks.Completed(m.GetStationInfo())
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
func (m *Manager) scanAndFetchStations(ctx context.Context) ([]StationInfo, int, error) {
	scanDuration := m.config.ScanDuration()
	if err := scanContextError(ctx); err != nil {
		return m.GetStationInfo(), 0, err
	}
	// A Lighthouse commonly stops advertising while a GATT connection is
	// active. Release our cached connections before a fresh scan so previously
	// discovered stations can advertise again and participate in presence and
	// channel-conflict detection.
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
	for _, stationPtr := range connectedStations {
		if err := scanContextError(ctx); err != nil {
			return m.GetStationInfo(), 0, err
		}
		address := stationPtr.Snapshot().Address
		if releaseErr := m.bluetoothOps.releaseStationForScan(stationPtr); releaseErr != nil {
			m.observeBluetoothError(releaseErr)
			releaseErrors = append(releaseErrors, fmt.Errorf("%s: %w", address, releaseErr))
			unreliablePresence[strings.ToLower(address)] = struct{}{}
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
		return m.GetStationInfo(), 0, err
	}
	discoveredValues, err := m.bluetoothOps.scanForDurationContext(ctx, scanDuration)
	if errors.Is(err, bluetooth.ErrScanCancelled) || errors.Is(err, context.Canceled) {
		return m.GetStationInfo(), 0, bluetooth.ErrScanCancelled
	} else if err != nil {
		m.observeBluetoothError(err)
		return m.GetStationInfo(), 0, fmt.Errorf("bluetooth scan failed: %w", err)
	}
	// The BLE scan completed its full duration, so the discovery results
	// are merged even if the context was cancelled during the stop
	// handshake; only the optional initial reads are skipped below.
	stationsToFetch := make([]*bluetooth.BaseStation, 0)
	scanTime := time.Now()
	m.stationsMutex.Lock()
	for _, stationPtr := range m.stations {
		if stationPtr != nil {
			if _, unreliable := unreliablePresence[strings.ToLower(stationPtr.Snapshot().Address)]; unreliable {
				stationPtr.MarkPresenceUncertain()
				continue
			}
			stationPtr.MarkMissed()
		}
	}
	for _, currentScanStation := range discoveredValues {
		addrStr := currentScanStation.Address.String()
		if existingStation, found := m.stations[addrStr]; found {
			if currentScanStation.Name != "" {
				existingStation.UpdateName(currentScanStation.Name)
			}
			existingStation.MarkSeen(scanTime)
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
	if len(stationsToFetch) > 0 && scanContextError(ctx) == nil {
		phaseContext, cancelPhase := context.WithTimeout(ctx, m.scanReadPhaseTimeoutDuration())
		defer cancelPhase()
		var wg sync.WaitGroup
		type initialReadResult struct {
			address               string
			station               *bluetooth.BaseStation
			err                   error
			phaseDeadlineExceeded bool
		}
		readResults := make([]initialReadResult, len(stationsToFetch))
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
				readResults[resultIndex].err = runSafely("initial station read", func() error {
					return m.bluetoothOps.fetchInitialPowerState(readContext, ptr)
				})
				if ctx.Err() == nil && errors.Is(phaseContext.Err(), context.DeadlineExceeded) &&
					errors.Is(readResults[resultIndex].err, context.DeadlineExceeded) {
					readResults[resultIndex].phaseDeadlineExceeded = true
				}
			}(index, stationToFetch)
		}
		wg.Wait()
		if err := scanContextError(ctx); err != nil {
			return m.GetStationInfo(), 0, err
		}
		sort.Slice(readResults, func(i, j int) bool {
			return strings.ToLower(readResults[i].address) < strings.ToLower(readResults[j].address)
		})
		readErrors := make([]error, 0)
		for _, result := range readResults {
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
					Channel: initialErr.Channel,
				}
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
	return m.GetStationInfo(), len(discoveredValues), nil
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

// StopScan cancels the active scan and waits until scan processing has
// finished. Terminal callbacks are delivered afterwards so callbacks can
// safely call StopScan themselves. Repeated and no-op calls are safe.
// The scan's own outcome is reported through GetScanStatus and the scan
// callbacks; StopScan only reports whether the stop completed, so a scan
// that already failed on its own is not surfaced as a stop failure.
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
	<-lifecycle.done
	return nil
}
func (m *Manager) IsScanning() bool {
	return m.isScanning.Load()
}
func (m *Manager) markScanStarted() {
	m.scanStatusMutex.Lock()
	m.scanStatus = ScanStatus{
		State:     "starting",
		StartedAt: time.Now().Format(time.RFC3339Nano),
		Warnings:  []string{},
	}
	m.scanStatusMutex.Unlock()
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
			m.deliverAbortedScanCallback(prepareErr, cancelled, callbacks)
			m.clearScanLifecycle(lifecycle)
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
func (m *Manager) deliverAbortedScanCallback(err error, cancelled bool, callbacks ScanCallbacks) {
	if cancelled {
		if callbacks.Cancelled != nil {
			if callbackErr := runSafely("scan cancelled callback", func() error {
				callbacks.Cancelled()
				return nil
			}); callbackErr != nil {
				log.Printf("Scan cancelled callback failed: %v", callbackErr)
			}
		}
		return
	}
	if callbacks.Failed != nil {
		if callbackErr := runSafely("scan failure callback", func() error {
			callbacks.Failed(err)
			return nil
		}); callbackErr != nil {
			log.Printf("Scan failure callback failed: %v", callbackErr)
		}
	}
}
