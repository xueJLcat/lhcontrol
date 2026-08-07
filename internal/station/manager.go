package station

import (
	"context"
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
		stations:                make(map[string]*bluetooth.BaseStation),
		config:                  cfg,
		activeDeviceOperations:  make(map[string]activeDeviceOperation),
		deviceOperationSlots:    make(chan struct{}, 2),
		statusRetries:           make(map[string]statusRetry),
		statusRecoveryWake:      make(chan struct{}, 1),
		statusRetryBase:         30 * time.Second,
		statusRetryMax:          5 * time.Minute,
		statusBusyRetry:         250 * time.Millisecond,
		initialReadTimeout:      defaultInitialReadTimeout,
		initialReadPhaseTimeout: defaultInitialReadPhaseTimeout,
		statusReadTimeout:       defaultStatusReadTimeout,
		statusRefreshTimeout:    defaultStatusRefreshTimeout,
		stationOperationTimeout: defaultStationOperationTimeout,
		scanStatus:              ScanStatus{State: "idle", Warnings: []string{}},
		initializeBluetooth:     bluetooth.Initialize,
		shutdownCh:              make(chan struct{}),
		lifecycleContext:        lifecycleContext,
		cancelLifecycle:         cancelLifecycle,
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

// newStationOperationContext gives one physical-device action a hard upper
// bound. Tests that construct a Manager directly still receive the production
// default when the injectable field is left at zero.
func (m *Manager) newStationOperationContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = m.lifecycleContext
	}
	timeout := m.stationOperationTimeout
	if timeout <= 0 {
		timeout = defaultStationOperationTimeout
	}
	return context.WithTimeout(parent, timeout)
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
	delay := m.statusRetryBase
	for attempt := 1; attempt < failures && delay < m.statusRetryMax; attempt++ {
		delay *= 2
	}
	if delay > m.statusRetryMax {
		return m.statusRetryMax
	}
	return delay
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
		kinds := effectiveStatusRetryKinds(retry)
		if kinds&statusRetryConnection != 0 && retry.failures >= statusAbsentRetryLimit {
			retry.kinds = kinds &^ statusRetryConnection
			retry.failures = 0
			retry.lastAttempt = time.Time{}
			retry.nextAt = time.Time{}
			changed = true
		}
		if kinds&statusRetryChannel != 0 && retry.channelFailures >= statusAbsentRetryLimit {
			retry.kinds &^= statusRetryChannel
			retry.channelFailures = 0
			retry.channelLastAttempt = time.Time{}
			retry.channelNextAt = time.Time{}
			changed = true
		}
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
	connectionNoted := false
	if powerErr == nil || bluetooth.IsUnsupportedCapabilityError(powerErr) {
		m.clearStatusFailureKind(address, statusRetryConnection)
	} else {
		_ = m.bluetoothOps.disconnectStation(station)
		m.noteStatusFailure(address)
		connectionNoted = true
	}
	if channelErr == nil || bluetooth.IsUnsupportedCapabilityError(channelErr) {
		m.clearStatusFailureKind(address, statusRetryChannel)
	} else {
		m.noteChannelFailure(address)
		if bluetooth.RequiresReconnect(channelErr) || bluetooth.IsAdapterUnavailable(channelErr) {
			if !connectionNoted {
				_ = m.bluetoothOps.disconnectStation(station)
				m.noteStatusFailure(address)
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
func (m *Manager) recordUnstructuredStationFailure(
	station *bluetooth.BaseStation,
	address string,
	err error,
) {
	if bluetooth.IsUnsupportedCapabilityError(err) && !bluetooth.RequiresReconnect(err) {
		m.clearStatusFailureKind(address, statusRetryConnection)
		return
	}
	_ = m.bluetoothOps.disconnectStation(station)
	m.noteStatusFailure(address)
}

// Initialize should be called at app startup
func (m *Manager) Initialize() error {
	m.initializeMutex.Lock()
	defer m.initializeMutex.Unlock()
	m.initializeErr = m.initializeBluetooth()
	if m.initializeErr != nil {
		m.nextInitializeAt = time.Now().Add(2 * time.Second)
	} else {
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
		m.nextInitializeAt = time.Now().Add(2 * time.Second)
		return fmt.Errorf("Bluetooth is unavailable; turn on Bluetooth or check the adapter, then retry: %w", m.initializeErr)
	}
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
	m.initializeErr = err
	m.nextInitializeAt = time.Now().Add(2 * time.Second)
	m.initializeMutex.Unlock()
	if cleanupErr := bluetooth.InvalidateAllConnections(); cleanupErr != nil {
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
		_ = m.bluetoothOps.disconnectStation(station)
		m.noteStatusFailure(address)
	}
	return err
}
