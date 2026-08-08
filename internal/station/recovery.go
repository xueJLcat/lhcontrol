package station

import (
	"context"
	"errors"
	"fmt"
	"lhcontrol/internal/bluetooth"
	"sort"
	"strings"
	"sync"
	"time"
)

func (m *Manager) CheckAllStationStatuses() ([]StationInfo, error) {
	if !m.statusOperationMutex.TryLock() {
		return m.GetStationInfo(), fmt.Errorf("status refresh already in progress: %w", ErrOperationInProgress)
	}
	defer m.statusOperationMutex.Unlock()
	refreshContext, cancelRefresh := context.WithTimeout(m.lifecycleContext, m.statusRefreshTimeoutDuration())
	defer cancelRefresh()
	statusDone := m.beginStatusLifecycle(cancelRefresh)
	defer m.endStatusLifecycle(statusDone)
	if err := m.beginSharedOperation(); err != nil {
		return m.GetStationInfo(), err
	}
	defer m.endSharedOperation()
	if err := m.ensureReady(); err != nil {
		return m.GetStationInfo(), err
	}
	stationsToRead := make([]*bluetooth.BaseStation, 0)
	disconnectedAddresses := make([]string, 0)
	m.stationsMutex.RLock()
	for _, stationPtr := range m.stations {
		if stationPtr == nil {
			continue
		}
		snapshot := stationPtr.Snapshot()
		if !snapshot.Present {
			continue
		}
		if m.bluetoothOps.stationConnected(stationPtr) {
			stationsToRead = append(stationsToRead, stationPtr)
		} else {
			disconnectedAddresses = append(disconnectedAddresses, snapshot.Address)
		}
	}
	m.stationsMutex.RUnlock()
	sort.Strings(disconnectedAddresses)
	if len(stationsToRead) == 0 {
		for _, address := range disconnectedAddresses {
			m.ensureStatusRecoveryTracked(address)
		}
		return m.GetStationInfo(), nil
	}
	sort.Slice(stationsToRead, func(i, j int) bool {
		return strings.ToLower(stationsToRead[i].Snapshot().Address) <
			strings.ToLower(stationsToRead[j].Snapshot().Address)
	})
	type statusReadWork struct {
		index   int
		station *bluetooth.BaseStation
	}
	statusErrors := make([]error, len(stationsToRead))
	work := make(chan statusReadWork)
	// Keep one GATT slot available for foreground commands while the periodic
	// refresh reads connected stations.
	workerCount := 1
	if len(stationsToRead) < workerCount {
		workerCount = len(stationsToRead)
	}
	var wg sync.WaitGroup
	for worker := 0; worker < workerCount; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range work {
				ptr := item.station
				address := ptr.Snapshot().Address
				readContext, cancelRead := context.WithTimeout(refreshContext, m.statusReadTimeoutDuration())
				if err := m.beginStationOperationKindContext(address, deviceOperationStatus, cancelRead); err != nil {
					cancelRead()
					if errors.Is(err, ErrOperationInProgress) {
						m.trackStatusRefreshPending(address)
						continue
					}
					statusErrors[item.index] = fmt.Errorf("%s: status read skipped: %w", address, err)
					continue
				}
				func() {
					defer m.endStationOperation(address)
					defer cancelRead()
					workerErr := runSafely("station status worker", func() error {
						return m.bluetoothOps.readPowerStateContext(readContext, ptr)
					})
					if workerErr != nil {
						if m.shuttingDown.Load() && errors.Is(workerErr, context.Canceled) {
							statusErrors[item.index] = fmt.Errorf("%s: status read cancelled: %w", address, workerErr)
							return
						}
						if errors.Is(workerErr, context.Canceled) && m.lifecycleContext.Err() == nil {
							m.trackStatusRefreshPending(address)
							return
						}
						if errors.Is(refreshContext.Err(), context.DeadlineExceeded) &&
							errors.Is(workerErr, context.DeadlineExceeded) {
							m.trackStatusRefreshPending(address)
							statusErrors[item.index] = fmt.Errorf("%s: status refresh deadline exceeded: %w", address, workerErr)
							return
						}
						m.observeBluetoothError(workerErr)
						var readErr *bluetooth.StatusReadError
						if errors.As(workerErr, &readErr) {
							m.recordStructuredReadResult(ptr, address, readErr.Power, readErr.Channel)
						} else {
							m.recordUnstructuredStationFailure(ptr, address, workerErr)
						}
						statusErrors[item.index] = fmt.Errorf("%s: %w", address, workerErr)
					} else {
						m.clearStatusFailureKind(
							address,
							statusRetryConnection|statusRetryChannel|statusRetryRefresh,
						)
					}
				}()
			}
		}()
	}
dispatch:
	for index, station := range stationsToRead {
		select {
		case work <- statusReadWork{index: index, station: station}:
		case <-refreshContext.Done():
			for skippedIndex := index; skippedIndex < len(stationsToRead); skippedIndex++ {
				address := stationsToRead[skippedIndex].Snapshot().Address
				m.trackStatusRefreshPending(address)
				if !errors.Is(refreshContext.Err(), context.Canceled) || m.lifecycleContext.Err() != nil {
					statusErrors[skippedIndex] = fmt.Errorf("%s: status refresh deadline exceeded: %w", address, refreshContext.Err())
				}
			}
			break dispatch
		}
	}
	close(work)
	wg.Wait()
	// Start newly discovered disconnect recovery only after foreground status
	// reads have released their slots. Otherwise this refresh can make one of
	// its own connected-device reads fail with Busy.
	for _, address := range disconnectedAddresses {
		m.ensureStatusRecoveryTracked(address)
	}
	m.scheduleStatusRecovery()
	statusInfos := m.GetStationInfo()
	incomplete := make([]error, 0, len(statusErrors))
	for _, statusErr := range statusErrors {
		if statusErr != nil {
			incomplete = append(incomplete, statusErr)
		}
	}
	if len(incomplete) > 0 {
		return statusInfos, fmt.Errorf("one or more station status reads were incomplete: %w", errors.Join(incomplete...))
	}
	return statusInfos, nil
}
func (m *Manager) scheduleStatusRecovery() {
	m.startStatusRecoveryScheduler()
	m.wakeStatusRecovery()
}
func (m *Manager) startStatusRecoveryScheduler() {
	m.lifecycleMutex.Lock()
	defer m.lifecycleMutex.Unlock()
	if m.shuttingDown.Load() {
		return
	}
	m.statusRecoveryStart.Do(func() {
		m.statusRecoveryWg.Add(1)
		go func() {
			defer m.statusRecoveryWg.Done()
			m.statusRecoveryLoop()
		}()
	})
}
func (m *Manager) wakeStatusRecovery() {
	if m.shuttingDown.Load() {
		return
	}
	select {
	case m.statusRecoveryWake <- struct{}{}:
	default:
	}
}
func (m *Manager) ensureStatusRecoveryTracked(address string) {
	m.statusRetryMutex.Lock()
	retry, exists := m.statusRetries[address]
	if !exists {
		retry = statusRetry{
			kinds:  statusRetryConnection,
			nextAt: time.Now(),
		}
	} else if effectiveStatusRetryKinds(retry)&statusRetryConnection == 0 {
		// A channel-only retry must not delay a newly observed disconnect.
		// Add an immediate, independent connection schedule while preserving
		// the existing channel backoff.
		retry.kinds |= statusRetryConnection
		retry.failures = 0
		retry.lastAttempt = time.Time{}
		retry.nextAt = time.Now()
	}
	m.statusRetries[address] = retry
	m.statusRetryMutex.Unlock()
	m.scheduleStatusRecovery()
}
func (m *Manager) statusRecoveryLoop() {
	var timer *time.Timer
	stopTimer := func() {
		if timer == nil {
			return
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	}
	defer stopTimer()
	for {
		select {
		case <-m.shutdownCh:
			return
		default:
		}
		delay, scheduled := m.nextStatusRecoveryDelay()
		if !scheduled {
			select {
			case <-m.shutdownCh:
				return
			case <-m.statusRecoveryWake:
				continue
			}
		}
		if delay > 0 {
			if timer == nil {
				timer = time.NewTimer(delay)
			} else {
				stopTimer()
				timer.Reset(delay)
			}
			select {
			case <-m.shutdownCh:
				return
			case <-m.statusRecoveryWake:
				stopTimer()
				continue
			case <-timer.C:
			}
		}
		m.statusRecoveryRunning.Store(true)
		retryAfter := m.runStatusRecoveryRound()
		m.statusRecoveryRunning.Store(false)
		if retryAfter > 0 {
			if timer == nil {
				timer = time.NewTimer(retryAfter)
			} else {
				stopTimer()
				timer.Reset(retryAfter)
			}
			select {
			case <-m.shutdownCh:
				return
			case <-m.statusRecoveryWake:
				stopTimer()
			case <-timer.C:
			}
		}
	}
}
func (m *Manager) nextStatusRecoveryDelay() (time.Duration, bool) {
	m.statusRetryMutex.Lock()
	retries := make(map[string]statusRetry, len(m.statusRetries))
	for address, retry := range m.statusRetries {
		retries[address] = retry
	}
	m.statusRetryMutex.Unlock()
	if len(retries) == 0 {
		return 0, false
	}
	disconnected := make(map[string]struct{}, len(retries))
	m.stationsMutex.RLock()
	for _, station := range m.stations {
		if station == nil {
			continue
		}
		snapshot := station.Snapshot()
		kinds := effectiveStatusRetryKinds(retries[snapshot.Address])
		if kinds&(statusRetryChannel|statusRetryMetadata|statusRetryRefresh) != 0 || !snapshot.Connected {
			disconnected[snapshot.Address] = struct{}{}
		}
	}
	m.stationsMutex.RUnlock()
	now := time.Now()
	var earliest time.Time
	for address, retry := range retries {
		if _, eligible := disconnected[address]; !eligible {
			continue
		}
		_, _, nextAt := statusRetryOrder(retry)
		if nextAt.IsZero() {
			// A zero schedule is due immediately. Reporting it as "no work"
			// would strand the station until an unrelated wake arrives.
			return 0, true
		}
		if earliest.IsZero() || nextAt.Before(earliest) {
			earliest = nextAt
		}
	}
	if earliest.IsZero() {
		return 0, false
	}
	if !now.Before(earliest) {
		return 0, true
	}
	return earliest.Sub(now), true
}
func (m *Manager) initializationRetryDelay() time.Duration {
	m.initializeMutex.Lock()
	defer m.initializeMutex.Unlock()
	delay := time.Until(m.nextInitializeAt)
	if delay <= 0 {
		return m.statusBusyRetry
	}
	return delay
}
func (m *Manager) runStatusRecoveryRound() time.Duration {
	select {
	case <-m.shutdownCh:
		return 0
	default:
	}
	if m.shuttingDown.Load() {
		return 0
	}
	if m.isScanning.Load() {
		return m.statusBusyRetry
	}
	now := time.Now()
	type recoveryCandidate struct {
		station *bluetooth.BaseStation
		address string
		retry   statusRetry
		kind    statusRetryKind
	}
	m.statusRetryMutex.Lock()
	retries := make(map[string]statusRetry, len(m.statusRetries))
	for address, retry := range m.statusRetries {
		retries[address] = retry
	}
	m.statusRetryMutex.Unlock()
	candidates := make([]recoveryCandidate, 0)
	m.stationsMutex.RLock()
	for _, station := range m.stations {
		if station == nil {
			continue
		}
		snapshot := station.Snapshot()
		retry, tracked := retries[snapshot.Address]
		kind, _, _, nextAt := statusRetryOrderAndKind(retry)
		if !tracked || now.Before(nextAt) {
			continue
		}
		kinds := effectiveStatusRetryKinds(retry)
		if snapshot.Connected && kinds&(statusRetryChannel|statusRetryMetadata|statusRetryRefresh) == 0 {
			continue
		}
		candidates = append(candidates, recoveryCandidate{
			station: station,
			address: snapshot.Address,
			retry:   retry,
			kind:    kind,
		})
	}
	m.stationsMutex.RUnlock()
	if len(candidates) == 0 {
		return 0
	}
	sort.Slice(candidates, func(i, j int) bool {
		left, right := candidates[i], candidates[j]
		leftFailures, leftLastAttempt, leftNextAt := statusRetryOrder(left.retry)
		rightFailures, rightLastAttempt, rightNextAt := statusRetryOrder(right.retry)
		if !leftNextAt.Equal(rightNextAt) {
			return leftNextAt.Before(rightNextAt)
		}
		if leftFailures != rightFailures {
			return leftFailures < rightFailures
		}
		if !leftLastAttempt.Equal(rightLastAttempt) {
			return leftLastAttempt.Before(rightLastAttempt)
		}
		return strings.ToLower(left.address) < strings.ToLower(right.address)
	})
	for _, candidate := range candidates {
		if err := m.beginRecoveryStationOperation(candidate.address); err != nil {
			if errors.Is(err, ErrShuttingDown) {
				return 0
			}
			continue
		}
		return m.recoverOneStation(candidate.station, candidate.address, candidate.kind)
	}
	return m.statusBusyRetry
}
func (m *Manager) recoverOneStation(
	station *bluetooth.BaseStation,
	address string,
	retryKind statusRetryKind,
) time.Duration {
	defer m.endRecoveryStationOperation(address)
	if err := m.ensureReady(); err != nil {
		m.observeBluetoothError(err)
		return m.initializationRetryDelay()
	}
	m.recoveryOperationMutex.Lock()
	recoveryContext := m.recoveryContext
	m.recoveryOperationMutex.Unlock()
	if recoveryContext == nil {
		recoveryContext = m.lifecycleContext
	}
	metadataTracked := effectiveStatusRetryKinds(m.statusRetrySnapshot(address))&statusRetryMetadata != 0
	// When the station is already connected, the status fetch takes the
	// "already good" fast path and performs no discovery, so any metadata
	// error it reports is stale cache rather than fresh evidence.
	stationConnected := m.bluetoothOps.stationConnected(station)
	metadataAttempted := retryKind == statusRetryMetadata ||
		(!stationConnected && metadataTracked)
	if retryKind == statusRetryMetadata {
		// The refresh phase gets its own budget so a slow capability discovery
		// cannot starve the subsequent status read into a deadline failure,
		// which would be misclassified as a connection failure.
		refreshContext, cancelRefresh := context.WithTimeout(recoveryContext, m.initialReadTimeoutDuration())
		refreshErr := runSafely("station metadata recovery", func() error {
			_, err := m.bluetoothOps.refreshCapabilities(refreshContext, station)
			return err
		})
		cancelRefresh()
		if refreshErr != nil {
			if m.shuttingDown.Load() && errors.Is(refreshErr, context.Canceled) {
				return 0
			}
			if errors.Is(refreshErr, context.Canceled) && m.lifecycleContext.Err() == nil {
				m.deferStatusRecovery(address, m.statusBusyRetry)
				return m.statusBusyRetry
			}
			m.observeBluetoothError(refreshErr)
			m.recordUnstructuredStationFailure(station, address, refreshErr)
			m.noteMetadataFailure(address)
			m.stopExhaustedAbsentRecovery(address, station)
			return 0
		}
	}
	readContext, cancelRead := context.WithTimeout(recoveryContext, m.initialReadTimeoutDuration())
	defer cancelRead()
	err := runSafely("station status recovery", func() error {
		return m.bluetoothOps.fetchInitialPowerState(readContext, station)
	})
	if m.shuttingDown.Load() && errors.Is(err, context.Canceled) {
		return 0
	}
	if errors.Is(err, context.Canceled) && m.lifecycleContext.Err() == nil {
		m.deferStatusRecovery(address, m.statusBusyRetry)
		return m.statusBusyRetry
	}
	// A refresh marker represents pending work, not a failure. Once an attempt
	// completes, consume it; any real error below will establish the appropriate
	// connection or channel retry schedule.
	m.clearStatusFailureKind(address, statusRetryRefresh)
	m.observeBluetoothError(err)
	var initialErr *bluetooth.InitialReadError
	if errors.As(err, &initialErr) {
		m.recordStructuredReadResult(station, address, initialErr.Power, initialErr.Channel)
		// The revival branch re-registers a metadata failure observed by a
		// fresh reconnect/discovery on a station whose metadata retries were
		// previously exhausted. Require a disconnected start so a stale
		// cached error from a connected "already good" fetch cannot relight
		// the retry loop indefinitely.
		if metadataAttempted || (initialErr.Metadata != nil && !metadataTracked && !stationConnected) {
			m.recordMetadataReadResult(address, initialErr.Metadata)
		}
		m.stopExhaustedAbsentRecovery(address, station)
		return 0
	}
	if err != nil {
		m.recordUnstructuredStationFailure(station, address, err)
		if metadataAttempted {
			m.noteMetadataFailure(address)
		}
		m.stopExhaustedAbsentRecovery(address, station)
		return 0
	}
	m.clearStatusFailureKind(
		address,
		statusRetryConnection|statusRetryChannel|statusRetryRefresh,
	)
	if metadataAttempted {
		m.clearStatusFailureKind(address, statusRetryMetadata)
	}
	return 0
}
func (m *Manager) statusRetrySnapshot(address string) statusRetry {
	m.statusRetryMutex.Lock()
	defer m.statusRetryMutex.Unlock()
	return m.statusRetries[address]
}
