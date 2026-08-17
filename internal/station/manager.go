package station

import (
	"context"
	"errors"
	"fmt"
	"lhcontrol/internal/bluetooth"
	"lhcontrol/internal/config"
	"log"
	"sync"
	"time"
)

func NewManager(cfg *config.Config) *Manager {
	lifecycleContext, cancelLifecycle := context.WithCancel(context.Background())
	manager := &Manager{
		stations:               make(map[string]*bluetooth.BaseStation),
		config:                 cfg,
		activeDeviceOperations: make(map[string]activeDeviceOperation),
		deviceOperationSlots:   make(chan struct{}, 2),
		statusRetries:          make(map[string]statusRetry),
		statusRecoveryWake:     make(chan struct{}, 1),
		statusBusyRetry:        250 * time.Millisecond,
		scanStatus:             ScanStatus{State: "idle", Warnings: []string{}},
		initializeBluetooth:    bluetooth.Initialize,
		shutdownCh:             make(chan struct{}),
		lifecycleContext:       lifecycleContext,
		cancelLifecycle:        cancelLifecycle,
		bluetoothOps: bluetoothOperations{
			scanForDurationContext: bluetooth.ScanForDurationContext,
			readPowerStateContext:  bluetooth.ReadPowerStateContext,
			fetchInitialPowerState: bluetooth.FetchInitialPowerStateContext,
			ensureCapabilities:     bluetooth.EnsureCapabilitiesContext,
			refreshCapabilities:    bluetooth.RefreshCapabilitiesContext,
			setPowerState:          bluetooth.SetPowerStateContext,
			identify:               bluetooth.IdentifyContext,
			setChannel:             bluetooth.SetChannelContext,
			disconnectStation:      bluetooth.DisconnectStation,
			releaseStationForScan:  bluetooth.ReleaseStationForScan,
			stationConnected: func(station *bluetooth.BaseStation) bool {
				return station.Snapshot().Connected
			},
		},
	}
	manager.lifecycleCond = sync.NewCond(&manager.lifecycleMutex)
	return manager
}

// stationPointers copies the fleet pointers under the read lock and releases
// it before the caller snapshots each station: Snapshot takes each station's
// own mutex, which an abandoned WinRT cleanup can hold for a long time, and
// holding the fleet lock meanwhile would stall every other fleet reader and
// writer behind that one wedged station. Station pointers are never removed
// from the map, so using them after the unlock is safe.
func (m *Manager) stationPointers() []*bluetooth.BaseStation {
	m.stationsMutex.RLock()
	defer m.stationsMutex.RUnlock()
	stationPtrs := make([]*bluetooth.BaseStation, 0, len(m.stations))
	for _, stationPtr := range m.stations {
		if stationPtr == nil {
			continue
		}
		stationPtrs = append(stationPtrs, stationPtr)
	}
	return stationPtrs
}

// newStationOperationContext gives one physical-device action a hard upper
// bound. The timeout follows the user-configured value; tests that construct
// a Manager directly can still pin it through the injectable field and fall
// back to the production default when it is left at zero.
func (m *Manager) newStationOperationContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = m.lifecycleContext
	}
	timeout := m.stationOperationTimeout
	if timeout <= 0 {
		timeout = m.config.StationOperationTimeout()
	}
	return context.WithTimeout(parent, timeout)
}
// adapterCleanupWaitLimit bounds how long error-cleanup paths block on the
// contextless disconnect/release calls. WinRT cleanup of an unresponsive
// device can stretch past the surrounding operation timeout, and the caller
// (which may already be past its deadline) must not be held indefinitely.
const adapterCleanupWaitLimit = 15 * time.Second

// runBoundedAdapterCleanup runs a contextless adapter cleanup call but gives
// up waiting once the limit expires. The cleanup itself keeps running: the
// bluetooth package serializes per-station work on the station lock, so any
// later operation on the same station queues behind the abandoned cleanup
// instead of racing it.
func (m *Manager) runBoundedAdapterCleanup(cleanup func() error) error {
	done := make(chan error, 1)
	go func() {
		done <- runSafely("adapter cleanup", cleanup)
	}()
	limit := m.adapterCleanupWait
	if limit <= 0 {
		limit = adapterCleanupWaitLimit
	}
	cleanupTimer := time.NewTimer(limit)
	defer cleanupTimer.Stop()
	select {
	case err := <-done:
		return err
	case <-cleanupTimer.C:
		log.Printf("Bluetooth adapter cleanup exceeded %s; cleanup continues in the background", limit)
		return fmt.Errorf("bluetooth adapter cleanup exceeded %s", limit)
	}
}

func (m *Manager) disconnectStationBounded(station *bluetooth.BaseStation) error {
	return m.runBoundedAdapterCleanup(func() error {
		return m.bluetoothOps.disconnectStation(station)
	})
}

func (m *Manager) releaseStationForScanBounded(station *bluetooth.BaseStation) error {
	return m.runBoundedAdapterCleanup(func() error {
		return m.bluetoothOps.releaseStationForScan(station)
	})
}

func (m *Manager) noteStatusFailure(address string) {
	m.noteStatusFailureKind(address, statusRetryConnection)
}
func (m *Manager) noteChannelFailure(address string) {
	m.noteStatusFailureKind(address, statusRetryChannel)
}
func (m *Manager) noteMetadataFailure(address string) {
	m.noteStatusFailureKind(address, statusRetryMetadata)
}
func (m *Manager) noteStatusRefreshPending(address string) {
	m.trackStatusRefreshPending(address)
	m.scheduleStatusRecovery()
}
func (m *Manager) trackStatusRefreshPending(address string) {
	m.statusRetryMutex.Lock()
	retry := m.statusRetries[address]
	retry.kinds |= statusRetryRefresh
	now := time.Now()
	if retry.refreshNextAt.IsZero() || retry.refreshNextAt.After(now) {
		retry.refreshNextAt = now
	}
	m.statusRetries[address] = retry
	m.statusRetryMutex.Unlock()
}
func (m *Manager) noteStatusFailureKind(address string, kind statusRetryKind) {
	m.statusRetryMutex.Lock()
	retry := m.statusRetries[address]
	retry.kinds |= kind
	now := time.Now()
	if kind&statusRetryConnection != 0 {
		retry.failures++
		retry.lastAttempt = now
		retry.nextAt = now.Add(m.statusRetryDelay(retry.failures))
	}
	if kind&statusRetryChannel != 0 {
		retry.channelFailures++
		retry.channelLastAttempt = now
		retry.channelNextAt = now.Add(m.statusRetryDelay(retry.channelFailures))
	}
	if kind&statusRetryMetadata != 0 {
		retry.metadataFailures++
		retry.metadataLastAttempt = now
		retry.metadataNextAt = now.Add(m.statusRetryDelay(retry.metadataFailures))
		if retry.metadataFailures >= metadataRetryLimit {
			retry.kinds &^= statusRetryMetadata
			retry.metadataFailures = 0
			retry.metadataLastAttempt = time.Time{}
			retry.metadataNextAt = time.Time{}
		}
	}
	if retry.kinds == 0 {
		delete(m.statusRetries, address)
	} else {
		m.statusRetries[address] = retry
	}
	m.statusRetryMutex.Unlock()
	m.scheduleStatusRecovery()
}
func (m *Manager) statusRetryDelay(failures int) time.Duration {
	base := m.statusRetryBaseDelay()
	maxDelay := m.statusRetryMaxDelay()
	delay := base
	for attempt := 1; attempt < failures && delay < maxDelay; attempt++ {
		delay *= 2
	}
	if delay > maxDelay {
		return maxDelay
	}
	return delay
}

// Tunable timing fields may be overridden directly in tests. Production runs
// leave them at zero and follow the user-configured values.
func (m *Manager) statusRetryBaseDelay() time.Duration {
	if m.statusRetryBase > 0 {
		return m.statusRetryBase
	}
	return m.config.RecoveryRetryBase()
}

func (m *Manager) statusRetryMaxDelay() time.Duration {
	if m.statusRetryMax > 0 {
		return m.statusRetryMax
	}
	return m.config.RecoveryRetryMax()
}

func (m *Manager) absentStationRetryLimit() int {
	return m.config.GetAbsentStationRetryLimit()
}

func (m *Manager) initialReadTimeoutDuration() time.Duration {
	if m.initialReadTimeout > 0 {
		return m.initialReadTimeout
	}
	return m.config.InitialReadTimeout()
}

func (m *Manager) scanReadPhaseTimeoutDuration() time.Duration {
	if m.initialReadPhaseTimeout > 0 {
		return m.initialReadPhaseTimeout
	}
	return m.config.ScanReadPhaseTimeout()
}

func (m *Manager) statusReadTimeoutDuration() time.Duration {
	if m.statusReadTimeout > 0 {
		return m.statusReadTimeout
	}
	return m.config.StatusReadTimeout()
}

func (m *Manager) statusRefreshTimeoutDuration() time.Duration {
	if m.statusRefreshTimeout > 0 {
		return m.statusRefreshTimeout
	}
	return m.config.StatusRefreshTimeout()
}

func (m *Manager) channelScanFreshnessWindowDuration() time.Duration {
	return m.config.ChannelScanFreshness()
}

func (m *Manager) initializeRetryCooldown() time.Duration {
	return m.config.BluetoothInitRetry()
}

// ApplyRecoverySettings makes persisted recovery changes effective for work
// that is already queued. Without rebasing these deadlines, changing a retry
// delay only affected failures recorded after the setting changed, leaving an
// existing station or adapter retry asleep on the previous schedule.
func (m *Manager) ApplyRecoverySettings() {
	stationPtrs := m.stationPointers()
	absent := make(map[string]bool, len(stationPtrs))
	for _, station := range stationPtrs {
		snapshot := station.Snapshot()
		absent[snapshot.Address] = !snapshot.Present
	}

	retryLimit := m.absentStationRetryLimit()
	m.statusRetryMutex.Lock()
	for address, retry := range m.statusRetries {
		// Canonicalize historical entries whose zero kind represented a
		// connection retry before the schedules were split.
		retry.kinds = effectiveStatusRetryKinds(retry)
		if absent[address] {
			retry, _ = pruneExhaustedAbsentRetry(retry, retryLimit)
		}
		if retry.kinds == 0 {
			delete(m.statusRetries, address)
			continue
		}
		if retry.kinds&statusRetryConnection != 0 && retry.failures > 0 && !retry.lastAttempt.IsZero() {
			retry.nextAt = retry.lastAttempt.Add(m.statusRetryDelay(retry.failures))
		}
		if retry.kinds&statusRetryChannel != 0 {
			if retry.channelFailures > 0 && !retry.channelLastAttempt.IsZero() {
				retry.channelNextAt = retry.channelLastAttempt.Add(m.statusRetryDelay(retry.channelFailures))
			} else if retry.kinds&statusRetryConnection == 0 && retry.failures > 0 && !retry.lastAttempt.IsZero() {
				// Preserve the legacy channel-only representation used by old
				// in-memory entries until another operation migrates its fields.
				retry.nextAt = retry.lastAttempt.Add(m.statusRetryDelay(retry.failures))
			}
		}
		if retry.kinds&statusRetryMetadata != 0 && retry.metadataFailures > 0 && !retry.metadataLastAttempt.IsZero() {
			retry.metadataNextAt = retry.metadataLastAttempt.Add(m.statusRetryDelay(retry.metadataFailures))
		}
		m.statusRetries[address] = retry
	}
	hasStatusRetries := len(m.statusRetries) > 0
	m.statusRetryMutex.Unlock()

	m.initializeMutex.Lock()
	if m.initializeErr != nil && !m.initializeFailedAt.IsZero() {
		m.nextInitializeAt = m.initializeFailedAt.Add(m.initializeRetryCooldown())
	}
	m.initializeMutex.Unlock()

	if hasStatusRetries {
		m.scheduleStatusRecovery()
	} else {
		m.wakeStatusRecovery()
	}
}

// ReviveAbsentStationRecovery gives known absent stations a fresh limited
// recovery budget after the configured retry limit is raised. Entries that
// still have connection or channel recovery keep their failures and backoff;
// only work that was previously exhausted (and therefore removed) is restarted
// immediately. Both limited kinds are restored because an exhausted entry no
// longer records whether its last failure was transport- or channel-specific.
func (m *Manager) ReviveAbsentStationRecovery() {
	stationPtrs := m.stationPointers()
	absentStations := make([]string, 0, len(stationPtrs))
	for _, station := range stationPtrs {
		snapshot := station.Snapshot()
		if !snapshot.Present {
			absentStations = append(absentStations, snapshot.Address)
		}
	}

	now := time.Now()
	changed := false
	m.statusRetryMutex.Lock()
	for _, address := range absentStations {
		retry, tracked := m.statusRetries[address]
		kinds := retry.kinds
		if tracked {
			kinds = effectiveStatusRetryKinds(retry)
			if kinds&(statusRetryConnection|statusRetryChannel) != 0 {
				continue
			}
		}
		retry.kinds = kinds | statusRetryConnection | statusRetryChannel
		retry.failures = 0
		retry.lastAttempt = time.Time{}
		retry.nextAt = now
		retry.channelFailures = 0
		retry.channelLastAttempt = time.Time{}
		retry.channelNextAt = now
		m.statusRetries[address] = retry
		changed = true
	}
	m.statusRetryMutex.Unlock()

	if changed {
		m.scheduleStatusRecovery()
	}
}

func (m *Manager) deferStatusRecovery(address string, delay time.Duration) {
	m.statusRetryMutex.Lock()
	retry, exists := m.statusRetries[address]
	if exists {
		deadline := time.Now().Add(delay)
		kinds := effectiveStatusRetryKinds(retry)
		if kinds&statusRetryConnection != 0 &&
			(retry.nextAt.IsZero() || retry.nextAt.Before(deadline)) {
			retry.nextAt = deadline
		}
		if kinds&statusRetryChannel != 0 &&
			(retry.channelNextAt.IsZero() || retry.channelNextAt.Before(deadline)) {
			retry.channelNextAt = deadline
		}
		if kinds&statusRetryMetadata != 0 &&
			(retry.metadataNextAt.IsZero() || retry.metadataNextAt.Before(deadline)) {
			retry.metadataNextAt = deadline
		}
		if kinds&statusRetryRefresh != 0 &&
			(retry.refreshNextAt.IsZero() || retry.refreshNextAt.Before(deadline)) {
			retry.refreshNextAt = deadline
		}
		m.statusRetries[address] = retry
	}
	m.statusRetryMutex.Unlock()
	m.wakeStatusRecovery()
}
func (m *Manager) clearStatusFailure(address string) {
	m.statusRetryMutex.Lock()
	delete(m.statusRetries, address)
	m.statusRetryMutex.Unlock()
	m.wakeStatusRecovery()
}
func (m *Manager) clearStatusFailureKind(address string, kind statusRetryKind) {
	m.statusRetryMutex.Lock()
	retry, exists := m.statusRetries[address]
	if exists {
		// Entries created before the schedules were split (and a few focused
		// tests) store a channel-only schedule in the connection fields.
		if retry.kinds&statusRetryChannel != 0 && retry.channelNextAt.IsZero() {
			retry.channelFailures = retry.failures
			retry.channelLastAttempt = retry.lastAttempt
			retry.channelNextAt = retry.nextAt
		}
		retry.kinds &^= kind
		if kind&statusRetryConnection != 0 {
			retry.failures = 0
			retry.lastAttempt = time.Time{}
			retry.nextAt = time.Time{}
		}
		if kind&statusRetryChannel != 0 {
			retry.channelFailures = 0
			retry.channelLastAttempt = time.Time{}
			retry.channelNextAt = time.Time{}
		}
		if kind&statusRetryMetadata != 0 {
			retry.metadataFailures = 0
			retry.metadataLastAttempt = time.Time{}
			retry.metadataNextAt = time.Time{}
		}
		if kind&statusRetryRefresh != 0 {
			retry.refreshNextAt = time.Time{}
		}
		if retry.kinds == 0 {
			delete(m.statusRetries, address)
		} else {
			m.statusRetries[address] = retry
		}
	}
	m.statusRetryMutex.Unlock()
	m.wakeStatusRecovery()
}
func effectiveStatusRetryKinds(retry statusRetry) statusRetryKind {
	// Retry entries created by older tests/configuration helpers without a
	// kind represent the historical connection recovery behavior.
	if retry.kinds == 0 {
		return statusRetryConnection
	}
	return retry.kinds
}
func statusRetrySchedule(retry statusRetry, kind statusRetryKind) (int, time.Time, time.Time) {
	if kind == statusRetryChannel && !retry.channelNextAt.IsZero() {
		return retry.channelFailures, retry.channelLastAttempt, retry.channelNextAt
	}
	if kind == statusRetryMetadata && !retry.metadataNextAt.IsZero() {
		return retry.metadataFailures, retry.metadataLastAttempt, retry.metadataNextAt
	}
	if kind == statusRetryRefresh && !retry.refreshNextAt.IsZero() {
		return 0, time.Time{}, retry.refreshNextAt
	}
	return retry.failures, retry.lastAttempt, retry.nextAt
}
func statusRetryOrderAndKind(retry statusRetry) (statusRetryKind, int, time.Time, time.Time) {
	kinds := effectiveStatusRetryKinds(retry)
	var selectedKind statusRetryKind
	var selectedFailures int
	var selectedLastAttempt time.Time
	var selectedNextAt time.Time
	for _, kind := range []statusRetryKind{
		statusRetryConnection,
		statusRetryChannel,
		statusRetryMetadata,
		statusRetryRefresh,
	} {
		if kinds&kind == 0 {
			continue
		}
		failures, lastAttempt, nextAt := statusRetrySchedule(retry, kind)
		if selectedNextAt.IsZero() || nextAt.Before(selectedNextAt) {
			selectedKind = kind
			selectedFailures = failures
			selectedLastAttempt = lastAttempt
			selectedNextAt = nextAt
		}
		if nextAt.IsZero() {
			// A zero schedule is due immediately; no later kind can be due
			// sooner, so it must win rather than being overwritten by a kind
			// scheduled in the future.
			break
		}
	}
	return selectedKind, selectedFailures, selectedLastAttempt, selectedNextAt
}
func statusRetryOrder(retry statusRetry) (int, time.Time, time.Time) {
	_, failures, lastAttempt, nextAt := statusRetryOrderAndKind(retry)
	return failures, lastAttempt, nextAt
}
func (m *Manager) stopExhaustedAbsentRecovery(address string, station *bluetooth.BaseStation) {
	if station == nil || station.Snapshot().Present {
		return
	}
	m.statusRetryMutex.Lock()
	retry, tracked := m.statusRetries[address]
	changed := false
	if tracked {
		retry, changed = pruneExhaustedAbsentRetry(retry, m.absentStationRetryLimit())
		if retry.kinds == 0 {
			delete(m.statusRetries, address)
		} else {
			m.statusRetries[address] = retry
		}
	}
	m.statusRetryMutex.Unlock()
	if changed {
		m.wakeStatusRecovery()
	}
}

func pruneExhaustedAbsentRetry(retry statusRetry, retryLimit int) (statusRetry, bool) {
	kinds := effectiveStatusRetryKinds(retry)
	changed := false
	if kinds&statusRetryConnection != 0 && retry.failures >= retryLimit {
		kinds &^= statusRetryConnection
		retry.failures = 0
		retry.lastAttempt = time.Time{}
		retry.nextAt = time.Time{}
		changed = true
	}
	if kinds&statusRetryChannel != 0 && retry.channelFailures >= retryLimit {
		kinds &^= statusRetryChannel
		retry.channelFailures = 0
		retry.channelLastAttempt = time.Time{}
		retry.channelNextAt = time.Time{}
		changed = true
	}
	retry.kinds = kinds
	return retry, changed
}

// recordStructuredReadResult tracks power/connection recovery independently
// from optional channel recovery. A firmware that rejects power reads can
// still have a transient channel failure that needs another attempt.
// A dead connection normally fails both reads in one operation, so the
// connection failure is recorded at most once per call; counting it twice
// would double the exponential backoff and abandon absent stations early.
func (m *Manager) recordStructuredReadResult(
	station *bluetooth.BaseStation,
	address string,
	powerErr error,
	channelErr error,
) {
	m.recordObservedReadResult(station, address, true, powerErr, true, channelErr)
}

func (m *Manager) recordObservedReadResult(
	station *bluetooth.BaseStation,
	address string,
	powerObserved bool,
	powerErr error,
	channelObserved bool,
	channelErr error,
) {
	connectionFailureNoted := false
	connectionDisconnected := false
	if powerObserved {
		if powerErr == nil || bluetooth.IsUnsupportedCapabilityError(powerErr) || bluetooth.IsDeviceValueError(powerErr) {
			// Malformed device data (like an unsupported capability) cannot be
			// repaired by reconnecting; discarding the session here would start
			// a disconnect/reconnect cycle that fails the same way on every poll.
			m.clearStatusFailureKind(address, statusRetryConnection)
		} else if ((errors.Is(powerErr, context.DeadlineExceeded) || errors.Is(powerErr, context.Canceled)) &&
			!bluetooth.RequiresReconnect(powerErr)) || bluetooth.IsProtocolRejection(powerErr) {
			// A power read stopped by its own budget or by a cancellation is
			// not evidence the link is broken, matching the bare-deadline rule
			// used by scan initial reads, recovery, and status refreshes.
			// Structured reads surface the deadline inside
			// InitialReadError/StatusReadError, so the same
			// backoff-without-disconnect handling applies here: a slow but
			// reachable station must not pay a disconnect/reconnect cycle.
			// RequiresReconnect stays true for errors joined with a genuine
			// transport failure, so mixed errors still take the disconnect
			// branch below. Protocol-level rejections (authentication,
			// encryption, resources, value shape) are peer decisions about
			// the request with a healthy link; reconnecting could never
			// change them, so they share the backoff-without-disconnect path.
			m.noteStatusFailure(address)
			connectionFailureNoted = true
		} else {
			_ = m.disconnectStationBounded(station)
			m.noteStatusFailure(address)
			connectionFailureNoted = true
			connectionDisconnected = true
		}
	}
	if channelObserved {
		if channelErr == nil || bluetooth.IsUnsupportedCapabilityError(channelErr) || bluetooth.IsDeviceValueError(channelErr) {
			m.clearStatusFailureKind(address, statusRetryChannel)
		} else if (errors.Is(channelErr, context.Canceled) || errors.Is(channelErr, context.DeadlineExceeded)) &&
			!bluetooth.RequiresReconnect(channelErr) {
			// A budget deadline or cancellation that interrupted the channel
			// read is not evidence of a channel fault, matching the deadline
			// rule used by status refreshes and recovery. Schedule a plain
			// re-read instead of counting a failure with backoff.
			// RequiresReconnect stays true for errors joined with a genuine
			// transport failure, so mixed errors take the failure branch
			// below, matching the power branch above.
			m.noteStatusRefreshPending(address)
		} else {
			m.noteChannelFailure(address)
			if bluetooth.RequiresReconnect(channelErr) || bluetooth.IsAdapterUnavailable(channelErr) {
				// A power-read deadline already counted one connection
				// failure for this call without disconnecting; the channel
				// error's genuine transport failure still earns the
				// disconnect but must not double the failure count.
				if !connectionDisconnected {
					_ = m.disconnectStationBounded(station)
				}
				if !connectionFailureNoted {
					m.noteStatusFailure(address)
				}
			}
		}
	}
}
func (m *Manager) recordMetadataReadResult(address string, metadataErr error) {
	if metadataErr == nil || bluetooth.IsUnsupportedCapabilityError(metadataErr) {
		m.clearStatusFailureKind(address, statusRetryMetadata)
		return
	}
	m.noteMetadataFailure(address)
}

// reconcileMetadataReadResult observes a discovery side effect without
// mistaking an old cached metadata error for a new failure. Successful-read
// timestamps are unsuitable here because they remain zero on partial reads;
// the Bluetooth layer's monotonic revision changes for every completed
// metadata reconciliation instead.
func (m *Manager) reconcileMetadataReadResult(
	address string,
	previousRevision uint64,
	after bluetooth.BaseStationSnapshot,
) {
	if after.MetadataReadRevision == previousRevision {
		return
	}
	m.recordMetadataReadResult(address, after.MetadataReadError)
}

func (m *Manager) recordUnstructuredStationFailure(
	station *bluetooth.BaseStation,
	address string,
	err error,
) {
	if bluetooth.IsUnsupportedCapabilityError(err) && !bluetooth.RequiresReconnect(err) {
		m.clearStatusFailureKind(address, statusRetryConnection)
		return
	}
	if bluetooth.IsDeviceValueError(err) {
		// A value formatting violation is device data, not a broken link.
		m.clearStatusFailureKind(address, statusRetryConnection)
		return
	}
	if bluetooth.IsProtocolRejection(err) {
		// A security-policy or resource rejection keeps the healthy link;
		// only the usual failure accounting with backoff applies.
		m.noteStatusFailure(address)
		return
	}
	_ = m.disconnectStationBounded(station)
	m.noteStatusFailure(address)
}

// Initialize should be called at app startup
func (m *Manager) Initialize() error {
	m.initializeMutex.Lock()
	defer m.initializeMutex.Unlock()
	if m.shuttingDown.Load() {
		// Consistent with ensureReady: do not re-initialize the adapter while
		// it is being torn down.
		return ErrShuttingDown
	}
	m.initializeErr = m.initializeBluetooth()
	if m.initializeErr != nil {
		m.initializeFailedAt = time.Now()
		m.nextInitializeAt = m.initializeFailedAt.Add(m.initializeRetryCooldown())
	} else {
		m.initializeFailedAt = time.Time{}
		m.nextInitializeAt = time.Time{}
	}
	return m.initializeErr
}
func (m *Manager) ensureReady() error {
	if m.shuttingDown.Load() {
		return ErrShuttingDown
	}
	m.initializeMutex.Lock()
	defer m.initializeMutex.Unlock()
	if m.initializeErr == nil {
		if m.shuttingDown.Load() {
			return ErrShuttingDown
		}
		return nil
	}
	if time.Now().Before(m.nextInitializeAt) {
		return fmt.Errorf("Bluetooth is unavailable; turn on Bluetooth or check the adapter, then retry: %w", m.initializeErr)
	}
	m.initializeErr = m.initializeBluetooth()
	if m.initializeErr != nil {
		m.initializeFailedAt = time.Now()
		m.nextInitializeAt = m.initializeFailedAt.Add(m.initializeRetryCooldown())
		return fmt.Errorf("Bluetooth is unavailable; turn on Bluetooth or check the adapter, then retry: %w", m.initializeErr)
	}
	m.initializeFailedAt = time.Time{}
	m.nextInitializeAt = time.Time{}
	if m.shuttingDown.Load() {
		return ErrShuttingDown
	}
	return nil
}
func (m *Manager) markBluetoothUnavailable(err error) {
	if !bluetooth.IsAdapterUnavailable(err) {
		return
	}
	m.initializeMutex.Lock()
	// Debounce: an adapter loss marks every operation that observes it (a
	// scan release loop reports one per failing station). While the cooldown
	// from the first mark is still pending, the adapter state cannot change
	// — ensureReady refuses re-initialization until nextInitializeAt — so the
	// fleet-wide cleanup already issued for that loss must not be re-issued
	// for every subsequent observer (each copy pays the same bounded wait).
	alreadyMarked := bluetooth.IsAdapterUnavailable(m.initializeErr) && time.Now().Before(m.nextInitializeAt)
	m.initializeErr = err
	m.initializeFailedAt = time.Now()
	m.nextInitializeAt = m.initializeFailedAt.Add(m.initializeRetryCooldown())
	m.initializeMutex.Unlock()
	if alreadyMarked {
		m.wakeStatusRecovery()
		return
	}
	// Run the contextless fleet cleanup with the same bounded wait as every
	// other adapter cleanup: adapter loss is exactly when WinRT disconnect
	// calls are most likely to stall, and this path is invoked synchronously
	// from scan, refresh, recovery, and bulk workers that must not hang
	// indefinitely behind a wedged adapter.
	if cleanupErr := m.runBoundedAdapterCleanup(bluetooth.InvalidateAllConnections); cleanupErr != nil {
		log.Printf("Bluetooth cleanup after adapter loss is pending: %v", cleanupErr)
	}
	m.wakeStatusRecovery()
}
func (m *Manager) observeBluetoothError(err error) error {
	if err != nil {
		m.markBluetoothUnavailable(err)
	}
	return err
}
func (m *Manager) observeStationBluetoothError(station *bluetooth.BaseStation, address string, err error) error {
	m.observeBluetoothError(err)
	if err != nil && bluetooth.IsUnsupportedCapabilityError(err) && !bluetooth.RequiresReconnect(err) {
		m.clearStatusFailureKind(address, statusRetryConnection)
		return err
	}
	if err != nil && (bluetooth.RequiresReconnect(err) || bluetooth.IsAdapterUnavailable(err)) {
		_ = m.disconnectStationBounded(station)
		m.noteStatusFailure(address)
	}
	return err
}
