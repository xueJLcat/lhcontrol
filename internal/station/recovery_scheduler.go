package station

import (
	"context"
	"errors"
	"lhcontrol/internal/bluetooth"
	"sort"
	"strings"
	"time"
)

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
		// A panic inside a recovery round (for example a driver panic in the
		// disconnect/invalidation paths, which are not otherwise wrapped) must
		// not kill the loop: it is started via sync.Once, so a dead loop would
		// silence all background recovery until the process restarts. Recover
		// and back off briefly instead of hot-looping.
		retryAfter := time.Duration(0)
		if roundErr := runSafely("status recovery round", func() error {
			retryAfter = m.runStatusRecoveryRound()
			return nil
		}); roundErr != nil {
			retryAfter = m.statusBusyRetry
		}
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
	eligible := make(map[string]struct{}, len(retries))
	m.stationsMutex.RLock()
	for _, station := range m.stations {
		if station == nil {
			continue
		}
		address := station.Snapshot().Address
		if _, tracked := retries[address]; tracked {
			eligible[address] = struct{}{}
		}
	}
	m.stationsMutex.RUnlock()
	now := time.Now()
	var earliest time.Time
	for address, retry := range retries {
		if _, eligibleAddress := eligible[address]; !eligibleAddress {
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
		// Connection retries are deliberately not filtered by Connected.
		// Deadline-only read failures record a connection retry without
		// disconnecting a possibly-healthy station, and a disconnect whose
		// cleanup stays pending can leave a stale connection flag behind;
		// recovery must still run so its read either clears the retry or
		// turns the stale connection into a real disconnect.
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
	// Set when the metadata refresh phase succeeds, so a later unstructured
	// status-read failure does not also count a metadata failure even though
	// this round's refresh already read fresh metadata.
	metadataRefreshSucceeded := false
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
			if errors.Is(refreshErr, context.DeadlineExceeded) && !bluetooth.RequiresReconnect(refreshErr) {
				// A recovery-budget deadline is not evidence the link is broken.
				// Retry with the normal failure backoff but do not disconnect a
				// possibly-healthy station, matching the bluetooth layer's rule
				// that a deadline needs no reconnect and the deliberate intent
				// above not to misclassify a deadline as a connection failure.
				m.noteStatusFailure(address)
			} else {
				m.recordUnstructuredStationFailure(station, address, refreshErr)
			}
			m.noteMetadataFailure(address)
			m.stopExhaustedAbsentRecovery(address, station)
			return 0
		}
		// The refresh completed, so this round already read fresh metadata.
		metadataRefreshSucceeded = true
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
		if errors.Is(err, context.DeadlineExceeded) && !bluetooth.RequiresReconnect(err) {
			// A read-budget deadline is not evidence the link is broken: a slow
			// but reachable station must not be disconnected, and the timeout
			// should back off (and eventually give up via the absent limit)
			// rather than being torn down as a connection failure.
			m.noteStatusFailure(address)
		} else {
			m.recordUnstructuredStationFailure(station, address, err)
		}
		if metadataAttempted {
			if metadataRefreshSucceeded {
				// The refresh already re-read metadata this round, so clear the
				// metadata kind instead of counting a failure: a follow-up
				// unstructured status-read error must neither double-count nor
				// leave the stale schedule behind (which would re-schedule
				// metadata with zero backoff in a tight recovery loop).
				m.clearStatusFailureKind(address, statusRetryMetadata)
			} else {
				m.noteMetadataFailure(address)
			}
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
