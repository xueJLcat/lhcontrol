package station

import (
	"context"
	"errors"
	"fmt"
	"log"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"lhcontrol/internal/bluetooth"
	"lhcontrol/internal/config"
)

var ErrOperationInProgress = errors.New("another Bluetooth operation is already in progress")
var ErrNotFound = errors.New("station not found")
var ErrInvalidArgument = errors.New("invalid argument")
var ErrUnsupported = errors.New("operation is not supported")
var ErrChannelConflict = errors.New("channel conflicts with another visible station")
var ErrScanRequired = errors.New("a recent successful scan is required")
var ErrShuttingDown = errors.New("application is shutting down")

const (
	statusFreshnessWindow          = 45 * time.Second
	channelScanFreshnessWindow     = 2 * time.Minute
	defaultInitialReadTimeout      = 30 * time.Second
	defaultStatusReadTimeout       = 20 * time.Second
	defaultInitialReadPhaseTimeout = 45 * time.Second
	defaultStatusRefreshTimeout    = 30 * time.Second
	metadataFreshnessWindow        = 24 * time.Hour
)

// StationInfo is a simplified representation of a BaseStation for the frontend.
type StationInfo struct {
	Name                string                   `json:"name"`
	OriginalName        string                   `json:"originalName"`
	Address             string                   `json:"address"`
	PowerState          int                      `json:"powerState"`
	PowerStateName      string                   `json:"powerStateName"`
	PowerStateConfirmed bool                     `json:"powerStateConfirmed"`
	RawPowerState       int                      `json:"rawPowerState"`
	Channel             int                      `json:"channel"`
	ChannelConflict     bool                     `json:"channelConflict"`
	IsPresent           bool                     `json:"isPresent"`
	SeenInLatestScan    bool                     `json:"seenInLatestScan"`
	ScanFresh           bool                     `json:"scanFresh"`
	MissedScans         int                      `json:"missedScans"`
	LastSeenAt          string                   `json:"lastSeenAt"`
	LastReadAt          string                   `json:"lastReadAt"`
	LastPowerReadAt     string                   `json:"lastPowerReadAt"`
	LastChannelReadAt   string                   `json:"lastChannelReadAt"`
	MetadataReadAt      string                   `json:"metadataReadAt"`
	LastError           string                   `json:"lastError"`
	StatusFresh         bool                     `json:"statusFresh"`
	PowerFresh          bool                     `json:"powerFresh"`
	ChannelFresh        bool                     `json:"channelFresh"`
	MetadataFresh       bool                     `json:"metadataFresh"`
	ConnectionState     string                   `json:"connectionState"`
	CapabilitiesKnown   bool                     `json:"capabilitiesKnown"`
	Capabilities        bluetooth.Capabilities   `json:"capabilities"`
	Metadata            bluetooth.DeviceMetadata `json:"metadata"`
}

type PowerActionResult struct {
	Station           StationInfo `json:"station"`
	CommandSent       bool        `json:"commandSent"`
	Confirmed         bool        `json:"confirmed"`
	ConfirmationError string      `json:"confirmationError"`
}

type BulkPowerStationResult struct {
	Address     string      `json:"address"`
	Name        string      `json:"name"`
	Skipped     bool        `json:"skipped"`
	Reason      string      `json:"reason"`
	CommandSent bool        `json:"commandSent"`
	Success     bool        `json:"success"`
	Confirmed   bool        `json:"confirmed"`
	Error       string      `json:"error"`
	Station     StationInfo `json:"station"`
}

type BulkPowerResult struct {
	Target  string                   `json:"target"`
	Results []BulkPowerStationResult `json:"results"`
}

type ChannelChangeResult struct {
	Address           string      `json:"address"`
	PreviousChannel   int         `json:"previousChannel"`
	Channel           int         `json:"channel"`
	CommandSent       bool        `json:"commandSent"`
	Confirmed         bool        `json:"confirmed"`
	ConfirmationError string      `json:"confirmationError"`
	Warnings          []string    `json:"warnings"`
	Station           StationInfo `json:"station"`
}

type ScanStatus struct {
	State       string   `json:"state"`
	StartedAt   string   `json:"startedAt"`
	CompletedAt string   `json:"completedAt"`
	Error       string   `json:"error"`
	Warnings    []string `json:"warnings"`
	Found       int      `json:"found"`
}

type ScanCallbacks struct {
	Started   func()
	Completed func([]StationInfo)
	Failed    func(error)
	Cancelled func()
}

type bluetoothOperations struct {
	scanForDurationContext func(context.Context, time.Duration) ([]bluetooth.DiscoveredStation, error)
	readPowerStateContext  func(context.Context, *bluetooth.BaseStation) error
	fetchInitialPowerState func(context.Context, *bluetooth.BaseStation) error
	ensureCapabilities     func(context.Context, *bluetooth.BaseStation) (bluetooth.Capabilities, error)
	refreshCapabilities    func(context.Context, *bluetooth.BaseStation) (bluetooth.Capabilities, error)
	setPowerState          func(context.Context, *bluetooth.BaseStation, bluetooth.PowerState) (bluetooth.PowerControlResult, error)
	identify               func(context.Context, *bluetooth.BaseStation) error
	setChannel             func(context.Context, *bluetooth.BaseStation, int) (bluetooth.ChannelWriteResult, error)
	disconnectStation      func(*bluetooth.BaseStation) error
	releaseStationForScan  func(*bluetooth.BaseStation) error
	stationConnected       func(*bluetooth.BaseStation) bool
}

type scanLifecycle struct {
	ctx         context.Context
	cancel      context.CancelFunc
	done        chan struct{}
	startedDone chan struct{}
}

type deviceOperationKind uint8

const (
	deviceOperationForeground deviceOperationKind = iota
	deviceOperationStatus
	deviceOperationRecovery
)

type activeDeviceOperation struct {
	kind   deviceOperationKind
	done   chan struct{}
	cancel context.CancelFunc
}

type deviceOperationBusyError struct {
	backgroundDone   <-chan struct{}
	cancelBackground context.CancelFunc
}

func (e *deviceOperationBusyError) Error() string {
	return ErrOperationInProgress.Error()
}

func (e *deviceOperationBusyError) Unwrap() error {
	return ErrOperationInProgress
}

type Manager struct {
	stations                map[string]*bluetooth.BaseStation
	stationsMutex           sync.RWMutex
	config                  *config.Config
	operationMutex          sync.RWMutex
	globalOperationMutex    sync.Mutex
	foregroundGlobalActive  bool
	foregroundSharedActive  int
	scanTransitionMutex     sync.Mutex
	statusOperationMutex    sync.Mutex
	statusLifecycleMutex    sync.Mutex
	statusOperationDone     chan struct{}
	cancelStatusOperation   context.CancelFunc
	channelOperationMutex   sync.Mutex
	deviceOperationMutex    sync.Mutex
	activeDeviceOperations  map[string]activeDeviceOperation
	deviceOperationSlots    chan struct{}
	recoveryOperationMutex  sync.Mutex
	recoveryOperationDone   chan struct{}
	recoveryContext         context.Context
	cancelRecovery          context.CancelFunc
	recoveryGeneration      uint64
	foregroundSlotMissHook  func()
	scanLifecycleStartHook  func()
	scanReadyHook           func()
	isScanning              atomic.Bool
	scanStatusMutex         sync.RWMutex
	scanStatus              ScanStatus
	scanLifecycleMutex      sync.Mutex
	scanLifecycle           *scanLifecycle
	initializeMutex         sync.Mutex
	initializeErr           error
	nextInitializeAt        time.Time
	initializeBluetooth     func() error
	asyncScanWg             sync.WaitGroup
	scanCallbackWg          sync.WaitGroup
	statusRetryMutex        sync.Mutex
	statusRetries           map[string]statusRetry
	statusRecoveryRunning   atomic.Bool
	statusRecoveryStart     sync.Once
	statusRecoveryWake      chan struct{}
	statusRecoveryWg        sync.WaitGroup
	statusRetryBase         time.Duration
	statusRetryMax          time.Duration
	statusBusyRetry         time.Duration
	initialReadTimeout      time.Duration
	initialReadPhaseTimeout time.Duration
	statusReadTimeout       time.Duration
	statusRefreshTimeout    time.Duration
	shuttingDown            atomic.Bool
	shutdownOnce            sync.Once
	shutdownCh              chan struct{}
	lifecycleContext        context.Context
	cancelLifecycle         context.CancelFunc
	lifecycleMutex          sync.Mutex
	lifecycleCond           *sync.Cond
	activeOperations        int
	bluetoothOps            bluetoothOperations
}

type statusRetry struct {
	// The original fields are the connection retry schedule. Keeping them
	// separate from channel retry state prevents an optional channel failure
	// from accelerating or delaying connection recovery. Refresh pending work
	// has its own due time so it cannot reset an active connection backoff
	// or be delayed by one.
	failures            int
	lastAttempt         time.Time
	nextAt              time.Time
	channelFailures     int
	channelLastAttempt  time.Time
	channelNextAt       time.Time
	metadataFailures    int
	metadataLastAttempt time.Time
	metadataNextAt      time.Time
	refreshNextAt       time.Time
	kinds               statusRetryKind
}

type statusRetryKind uint8

const (
	statusRetryConnection statusRetryKind = 1 << iota
	statusRetryChannel
	statusRetryMetadata
	statusRetryRefresh
	statusAbsentRetryLimit = 5
	metadataRetryLimit     = 5
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

func (m *Manager) registerOperation() error {
	m.lifecycleMutex.Lock()
	defer m.lifecycleMutex.Unlock()
	if m.shuttingDown.Load() {
		return ErrShuttingDown
	}
	m.activeOperations++
	return nil
}

func (m *Manager) unregisterOperation() {
	m.lifecycleMutex.Lock()
	m.activeOperations--
	if m.activeOperations == 0 {
		m.lifecycleCond.Broadcast()
	}
	m.lifecycleMutex.Unlock()
}

func (m *Manager) beginOperation() error {
	if err := m.registerOperation(); err != nil {
		return err
	}
	if !m.operationMutex.TryLock() {
		m.unregisterOperation()
		return ErrOperationInProgress
	}
	return nil
}

func (m *Manager) endOperation() {
	m.operationMutex.Unlock()
	m.unregisterOperation()
}

// beginForegroundGlobalOperation waits for manager-owned background status
// and recovery work, while preserving immediate Busy responses for another
// foreground global, device, or configuration operation.
func (m *Manager) beginForegroundGlobalOperation() error {
	return m.beginForegroundGlobalOperationContext(context.Background())
}

func (m *Manager) beginForegroundGlobalOperationContext(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := m.beginOperation()
		if err == nil {
			if contextErr := ctx.Err(); contextErr != nil {
				m.endOperation()
				return contextErr
			}
			m.globalOperationMutex.Lock()
			m.foregroundGlobalActive = true
			m.globalOperationMutex.Unlock()
			return nil
		}
		if !errors.Is(err, ErrOperationInProgress) {
			return err
		}

		m.globalOperationMutex.Lock()
		foregroundActive := m.foregroundGlobalActive
		m.globalOperationMutex.Unlock()
		if foregroundActive {
			return err
		}

		m.recoveryOperationMutex.Lock()
		recoveryDone := m.recoveryOperationDone
		m.recoveryOperationMutex.Unlock()
		m.statusLifecycleMutex.Lock()
		statusDone := m.statusOperationDone
		m.statusLifecycleMutex.Unlock()

		var backgroundDone <-chan struct{}
		if recoveryDone != nil {
			backgroundDone = recoveryDone
		} else if statusDone != nil {
			backgroundDone = statusDone
		}
		if backgroundDone == nil {
			return err
		}
		m.cancelBackgroundReadsForForeground()
		select {
		case <-backgroundDone:
		case <-ctx.Done():
			return ctx.Err()
		case <-m.shutdownCh:
			return ErrShuttingDown
		}
	}
}

func (m *Manager) endForegroundGlobalOperation() {
	m.globalOperationMutex.Lock()
	m.foregroundGlobalActive = false
	m.globalOperationMutex.Unlock()
	m.endOperation()
}

func (m *Manager) beginStatusLifecycle(cancel context.CancelFunc) chan struct{} {
	done := make(chan struct{})
	m.statusLifecycleMutex.Lock()
	m.statusOperationDone = done
	m.cancelStatusOperation = cancel
	m.statusLifecycleMutex.Unlock()
	return done
}

func (m *Manager) endStatusLifecycle(done chan struct{}) {
	m.statusLifecycleMutex.Lock()
	if m.statusOperationDone == done {
		m.statusOperationDone = nil
		m.cancelStatusOperation = nil
		close(done)
	}
	m.statusLifecycleMutex.Unlock()
}

// beginSharedOperation participates in shutdown and excludes global operations
// without consuming a GATT slot. It is used for short configuration writes.
func (m *Manager) beginSharedOperation() error {
	if err := m.registerOperation(); err != nil {
		return err
	}
	if !m.operationMutex.TryRLock() {
		m.unregisterOperation()
		return ErrOperationInProgress
	}
	return nil
}

func (m *Manager) endSharedOperation() {
	m.operationMutex.RUnlock()
	m.unregisterOperation()
}

func (m *Manager) beginForegroundSharedOperation() error {
	m.scanTransitionMutex.Lock()
	defer m.scanTransitionMutex.Unlock()
	if m.isScanning.Load() {
		return ErrOperationInProgress
	}
	if err := m.beginSharedOperation(); err != nil {
		return err
	}
	m.globalOperationMutex.Lock()
	m.foregroundSharedActive++
	m.globalOperationMutex.Unlock()
	return nil
}

func (m *Manager) endForegroundSharedOperation() {
	m.globalOperationMutex.Lock()
	m.foregroundSharedActive--
	m.globalOperationMutex.Unlock()
	m.endSharedOperation()
}

// beginStationOperation rejects duplicate requests for one physical station
// and caps independent GATT work at two devices. It never waits while holding
// the global read lock, so a request flood cannot starve a scan.
func (m *Manager) beginStationOperation(address string) error {
	return m.beginStationOperationKind(address, deviceOperationForeground)
}

func (m *Manager) beginStationOperationKind(address string, kind deviceOperationKind) error {
	return m.beginStationOperationKindContext(address, kind, nil)
}

func (m *Manager) beginStationOperationKindContext(address string, kind deviceOperationKind, cancel context.CancelFunc) error {
	if err := m.registerOperation(); err != nil {
		return err
	}
	if !m.operationMutex.TryRLock() {
		m.unregisterOperation()
		return ErrOperationInProgress
	}
	key := strings.ToLower(address)
	m.deviceOperationMutex.Lock()
	if active, exists := m.activeDeviceOperations[key]; exists {
		var backgroundDone <-chan struct{}
		if active.kind == deviceOperationStatus || active.kind == deviceOperationRecovery {
			backgroundDone = active.done
		}
		m.deviceOperationMutex.Unlock()
		m.operationMutex.RUnlock()
		m.unregisterOperation()
		return &deviceOperationBusyError{backgroundDone: backgroundDone, cancelBackground: active.cancel}
	}
	select {
	case m.deviceOperationSlots <- struct{}{}:
	default:
		m.deviceOperationMutex.Unlock()
		m.operationMutex.RUnlock()
		m.unregisterOperation()
		return ErrOperationInProgress
	}
	m.activeDeviceOperations[key] = activeDeviceOperation{
		kind:   kind,
		done:   make(chan struct{}),
		cancel: cancel,
	}
	m.deviceOperationMutex.Unlock()
	return nil
}

func (m *Manager) endStationOperation(address string) {
	key := strings.ToLower(address)
	m.deviceOperationMutex.Lock()
	active, exists := m.activeDeviceOperations[key]
	delete(m.activeDeviceOperations, key)
	if exists {
		close(active.done)
	}
	m.deviceOperationMutex.Unlock()
	<-m.deviceOperationSlots
	m.operationMutex.RUnlock()
	m.unregisterOperation()
}

func (m *Manager) beginRecoveryStationOperation(address string) error {
	m.recoveryOperationMutex.Lock()
	if m.recoveryOperationDone != nil {
		m.recoveryOperationMutex.Unlock()
		return ErrOperationInProgress
	}
	done := make(chan struct{})
	recoveryContext, cancelRecovery := context.WithCancel(m.lifecycleContext)
	m.recoveryOperationDone = done
	m.recoveryContext = recoveryContext
	m.cancelRecovery = cancelRecovery
	m.recoveryGeneration++
	m.recoveryOperationMutex.Unlock()
	if err := m.beginStationOperationKindContext(address, deviceOperationRecovery, cancelRecovery); err != nil {
		cancelRecovery()
		m.recoveryOperationMutex.Lock()
		if m.recoveryOperationDone == done {
			m.recoveryOperationDone = nil
			m.recoveryContext = nil
			m.cancelRecovery = nil
			m.recoveryGeneration++
			close(done)
		}
		m.recoveryOperationMutex.Unlock()
		return err
	}
	return nil
}

func (m *Manager) endRecoveryStationOperation(address string) {
	m.endStationOperation(address)
	m.recoveryOperationMutex.Lock()
	done := m.recoveryOperationDone
	cancelRecovery := m.cancelRecovery
	m.recoveryOperationDone = nil
	m.recoveryContext = nil
	m.cancelRecovery = nil
	if cancelRecovery != nil {
		cancelRecovery()
	}
	if done != nil {
		m.recoveryGeneration++
		close(done)
	}
	m.recoveryOperationMutex.Unlock()
}

// beginForegroundStationOperation waits only for an active background
// recovery. It still rejects conflicts with other foreground work immediately,
// while preventing a hidden recovery task from making a UI-permitted second
// device action fail with Busy.
func (m *Manager) beginForegroundStationOperation(address string) error {
	m.scanTransitionMutex.Lock()
	defer m.scanTransitionMutex.Unlock()
	if m.isScanning.Load() {
		return ErrOperationInProgress
	}
	for {
		m.recoveryOperationMutex.Lock()
		generationBefore := m.recoveryGeneration
		m.recoveryOperationMutex.Unlock()
		err := m.beginStationOperation(address)
		if !errors.Is(err, ErrOperationInProgress) {
			return err
		}
		var deviceBusy *deviceOperationBusyError
		if errors.As(err, &deviceBusy) && deviceBusy.backgroundDone != nil {
			if deviceBusy.cancelBackground != nil {
				deviceBusy.cancelBackground()
			} else {
				m.cancelRecoveryForForeground()
			}
			select {
			case <-deviceBusy.backgroundDone:
				continue
			case <-m.shutdownCh:
				return ErrShuttingDown
			}
		}
		if m.foregroundSlotMissHook != nil {
			m.foregroundSlotMissHook()
		}
		m.recoveryOperationMutex.Lock()
		done := m.recoveryOperationDone
		generationAfter := m.recoveryGeneration
		m.recoveryOperationMutex.Unlock()
		if generationAfter != generationBefore {
			continue
		}
		if done == nil {
			return err
		}
		m.cancelRecoveryForForeground()
		select {
		case <-done:
		case <-m.shutdownCh:
			return ErrShuttingDown
		}
	}
}

func (m *Manager) beginBulkGlobalOperation() error {
	m.scanTransitionMutex.Lock()
	defer m.scanTransitionMutex.Unlock()
	if m.isScanning.Load() {
		return ErrOperationInProgress
	}
	return m.beginForegroundGlobalOperation()
}

func (m *Manager) hasForegroundOperation() bool {
	m.globalOperationMutex.Lock()
	active := m.foregroundGlobalActive || m.foregroundSharedActive > 0
	m.globalOperationMutex.Unlock()
	if active {
		return true
	}
	m.deviceOperationMutex.Lock()
	defer m.deviceOperationMutex.Unlock()
	for _, operation := range m.activeDeviceOperations {
		if operation.kind == deviceOperationForeground {
			return true
		}
	}
	return false
}

func (m *Manager) cancelBackgroundReadsForForeground() {
	m.statusLifecycleMutex.Lock()
	cancelStatus := m.cancelStatusOperation
	m.statusLifecycleMutex.Unlock()
	if cancelStatus != nil {
		cancelStatus()
	}
	m.cancelRecoveryForForeground()
}

// cancelRecoveryForForeground makes a read-only recovery yield its GATT slot.
// The recovery scheduler will retry it without counting the cancellation as a
// station failure.
func (m *Manager) cancelRecoveryForForeground() {
	m.recoveryOperationMutex.Lock()
	cancel := m.cancelRecovery
	m.recoveryOperationMutex.Unlock()
	if cancel != nil {
		cancel()
	}
}

// IsBusy reports whether a scan, status read, or power command is active.
func (m *Manager) IsBusy() bool {
	if !m.operationMutex.TryLock() {
		return true
	}
	m.operationMutex.Unlock()
	return false
}

// GetStationInfo returns the current state of the stations map.
func (m *Manager) GetStationInfo() []StationInfo {
	m.stationsMutex.RLock()
	snapshots := make([]bluetooth.BaseStationSnapshot, 0, len(m.stations))
	for _, stationPtr := range m.stations {
		if stationPtr == nil {
			continue
		}
		snapshots = append(snapshots, stationPtr.Snapshot())
	}
	m.stationsMutex.RUnlock()

	channelCounts := make(map[int]int)
	now := time.Now()
	for _, snapshot := range snapshots {
		if snapshot.Present &&
			snapshot.MissedScans == 0 &&
			!snapshot.PresenceUncertain &&
			isRecent(snapshot.LastSeenAt, now, channelScanFreshnessWindow) &&
			snapshot.Channel != bluetooth.ChannelUnknown &&
			isFresh(snapshot.LastChannelReadAt, now) {
			channelCounts[snapshot.Channel]++
		}
	}

	stationInfos := make([]StationInfo, 0, len(snapshots))
	for _, snapshot := range snapshots {
		name := snapshot.Name
		if renamedName, ok := m.config.GetStationDisplayName(snapshot.Address, snapshot.Name); ok {
			name = renamedName
		}
		connectionState := "disconnected"
		if snapshot.Connected {
			connectionState = "connected"
		}
		powerFresh := snapshot.RawPowerState != bluetooth.RawPowerStateUnknown && isFresh(snapshot.LastPowerReadAt, now)
		channelFresh := snapshot.Channel != bluetooth.ChannelUnknown && isFresh(snapshot.LastChannelReadAt, now)
		seenInLatestScan := snapshot.MissedScans == 0 &&
			!snapshot.PresenceUncertain &&
			!snapshot.LastSeenAt.IsZero()
		scanFresh := seenInLatestScan &&
			isRecent(snapshot.LastSeenAt, now, channelScanFreshnessWindow)
		metadataFresh := isRecent(snapshot.MetadataReadAt, now, metadataFreshnessWindow)
		stationInfos = append(stationInfos, StationInfo{
			Name:                name,
			OriginalName:        snapshot.Name,
			Address:             snapshot.Address,
			PowerState:          int(snapshot.PowerState),
			PowerStateName:      snapshot.PowerState.String(),
			PowerStateConfirmed: powerFresh && bluetooth.IsPowerStateVerified(snapshot.PowerState, snapshot.RawPowerState),
			RawPowerState:       snapshot.RawPowerState,
			Channel:             snapshot.Channel,
			ChannelConflict: snapshot.Present && scanFresh && channelFresh &&
				channelCounts[snapshot.Channel] > 1,
			IsPresent:         snapshot.Present,
			SeenInLatestScan:  seenInLatestScan,
			ScanFresh:         scanFresh,
			MissedScans:       snapshot.MissedScans,
			LastSeenAt:        formatTimestamp(snapshot.LastSeenAt),
			LastReadAt:        formatTimestamp(snapshot.LastReadAt),
			LastPowerReadAt:   formatTimestamp(snapshot.LastPowerReadAt),
			LastChannelReadAt: formatTimestamp(snapshot.LastChannelReadAt),
			MetadataReadAt:    formatTimestamp(snapshot.MetadataReadAt),
			LastError:         snapshot.LastError,
			StatusFresh:       powerFresh || channelFresh,
			PowerFresh:        powerFresh,
			ChannelFresh:      channelFresh,
			MetadataFresh:     metadataFresh,
			ConnectionState:   connectionState,
			CapabilitiesKnown: snapshot.CapabilitiesKnown,
			Capabilities:      snapshot.Capabilities,
			Metadata:          snapshot.Metadata,
		})
	}
	sort.Slice(stationInfos, func(i, j int) bool {
		return stationValuesLess(
			stationInfos[i].Channel, stationInfos[i].Name, stationInfos[i].Address,
			stationInfos[j].Channel, stationInfos[j].Name, stationInfos[j].Address,
		)
	})
	return stationInfos
}

func stationValuesLess(leftChannel int, leftName, leftAddress string, rightChannel int, rightName, rightAddress string) bool {
	if leftChannel <= bluetooth.ChannelUnknown {
		leftChannel = int(^uint(0) >> 1)
	}
	if rightChannel <= bluetooth.ChannelUnknown {
		rightChannel = int(^uint(0) >> 1)
	}
	if leftChannel != rightChannel {
		return leftChannel < rightChannel
	}
	if left, right := strings.ToLower(leftName), strings.ToLower(rightName); left != right {
		return left < right
	}
	return strings.ToLower(leftAddress) < strings.ToLower(rightAddress)
}

func isFresh(value, now time.Time) bool {
	return isRecent(value, now, statusFreshnessWindow)
}

func isRecent(value, now time.Time, window time.Duration) bool {
	age := now.Sub(value)
	return !value.IsZero() && age >= 0 && age <= window
}

func formatTimestamp(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Format(time.RFC3339Nano)
}

func runSafely(scope string, operation func() error) (returnErr error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			returnErr = fmt.Errorf("%s panicked: %v\n%s", scope, recovered, debug.Stack())
			log.Printf("Recovered panic: %v", returnErr)
		}
	}()
	return operation()
}

func (m *Manager) scanAndFetchStationsSafely(ctx context.Context) (stations []StationInfo, found int, returnErr error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			returnErr = fmt.Errorf("scan workflow panicked: %v\n%s", recovered, debug.Stack())
			stations = m.GetStationInfo()
			log.Printf("Recovered panic: %v", returnErr)
		}
	}()
	return m.scanAndFetchStations(ctx)
}

func (m *Manager) ScanAndFetchStations() ([]StationInfo, error) {
	if err := m.beginScan(ScanCallbacks{}); err != nil {
		return m.GetStationInfo(), err
	}
	ctx := m.currentScanContext()
	stations, found, err := m.scanAndFetchStationsSafely(ctx)
	m.scanLifecycleMutex.Lock()
	lifecycle := m.scanLifecycle
	m.scanLifecycleMutex.Unlock()
	if lifecycle != nil {
		close(lifecycle.startedDone)
	}
	m.finishScan(stations, found, err, ScanCallbacks{})()
	// finishScan releases the global operation, so a queued read-only recovery
	// may already have produced a newer snapshot than scanAndFetchStations.
	return m.GetStationInfo(), err
}

func (m *Manager) beginScan(callbacks ScanCallbacks) error {
	lifecycle, err := m.reserveScan()
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

func (m *Manager) reserveScan() (*scanLifecycle, error) {
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
	ctx, cancel := context.WithCancel(context.Background())
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
		m.markScanFinished(m.GetStationInfo(), 0, bluetooth.ErrScanCancelled)
	} else {
		m.markScanFinished(m.GetStationInfo(), 0, err)
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

func (m *Manager) finishScan(stations []StationInfo, found int, err error, callbacks ScanCallbacks) func() {
	m.scanTransitionMutex.Lock()
	m.isScanning.Store(false)
	m.endForegroundGlobalOperation()
	m.markScanFinished(stations, found, err)

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
	scanDuration := 5 * time.Second
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
		phaseContext, cancelPhase := context.WithTimeout(ctx, m.initialReadPhaseTimeout)
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
				readContext, cancelRead := context.WithTimeout(phaseContext, m.initialReadTimeout)
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

func (m *Manager) markScanFinished(stations []StationInfo, found int, err error) {
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
	lifecycle, err := m.reserveScan()
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
		stations, found, err := m.scanAndFetchStationsSafely(ctx)
		deliverTerminal := m.finishScan(stations, found, err, callbacks)
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

func (m *Manager) CheckAllStationStatuses() ([]StationInfo, error) {
	if !m.statusOperationMutex.TryLock() {
		return m.GetStationInfo(), fmt.Errorf("status refresh already in progress: %w", ErrOperationInProgress)
	}
	defer m.statusOperationMutex.Unlock()
	refreshContext, cancelRefresh := context.WithTimeout(m.lifecycleContext, m.statusRefreshTimeout)
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
				readContext, cancelRead := context.WithTimeout(refreshContext, m.statusReadTimeout)
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
		refreshContext, cancelRefresh := context.WithTimeout(recoveryContext, m.initialReadTimeout)
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
	readContext, cancelRead := context.WithTimeout(recoveryContext, m.initialReadTimeout)
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

func (m *Manager) PowerOnStation(address string) error {
	_, err := m.SetStationPower(address, "on")
	return err
}

func (m *Manager) PowerOffStation(address string) error {
	_, err := m.SetStationPower(address, "sleep")
	return err
}

func (m *Manager) stationByAddress(address string) (*bluetooth.BaseStation, error) {
	m.stationsMutex.RLock()
	stationPtr, ok := m.stations[address]
	if !ok {
		for stationAddress, candidate := range m.stations {
			if strings.EqualFold(stationAddress, address) {
				stationPtr, ok = candidate, true
				break
			}
		}
	}
	m.stationsMutex.RUnlock()
	if !ok || stationPtr == nil {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, address)
	}
	return stationPtr, nil
}

func (m *Manager) stationInfoByAddress(address string) (StationInfo, error) {
	for _, info := range m.GetStationInfo() {
		if strings.EqualFold(info.Address, address) {
			return info, nil
		}
	}
	return StationInfo{}, fmt.Errorf("%w: %s", ErrNotFound, address)
}

// SetStationPower sets one of the three stable target states. Confirmed is
// false when the firmware supports writing but does not expose power reads.
func (m *Manager) cachedPowerOutcome(stationPtr *bluetooth.BaseStation, target bluetooth.PowerState) (PowerActionResult, error, bool) {
	snapshot := stationPtr.Snapshot()
	switch classifyCachedPower(snapshot, target, time.Now()) {
	case cachedPowerBooting:
		return PowerActionResult{}, fmt.Errorf("station is booting; retry after transition: %w", ErrOperationInProgress), true
	case cachedPowerActionable:
		return PowerActionResult{}, nil, false
	}
	info, err := m.stationInfoByAddress(snapshot.Address)
	if err != nil {
		return PowerActionResult{}, err, true
	}
	return PowerActionResult{Station: info, Confirmed: true}, nil, true
}

type cachedPowerDisposition uint8

const (
	cachedPowerActionable cachedPowerDisposition = iota
	cachedPowerBooting
	cachedPowerAtTarget
)

func classifyCachedPower(
	snapshot bluetooth.BaseStationSnapshot,
	target bluetooth.PowerState,
	now time.Time,
) cachedPowerDisposition {
	if !isFresh(snapshot.LastPowerReadAt, now) {
		return cachedPowerActionable
	}
	if snapshot.PowerState == bluetooth.PowerStateBooting {
		return cachedPowerBooting
	}
	if snapshot.PowerState == target &&
		bluetooth.IsPowerStateVerified(snapshot.PowerState, snapshot.RawPowerState) {
		return cachedPowerAtTarget
	}
	return cachedPowerActionable
}

func (m *Manager) SetStationPower(address, state string) (PowerActionResult, error) {
	target, err := bluetooth.ParsePowerTarget(state)
	if err != nil {
		return PowerActionResult{}, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	stationPtr, err := m.stationByAddress(address)
	if err != nil {
		return PowerActionResult{}, err
	}
	if m.shuttingDown.Load() {
		return PowerActionResult{}, ErrShuttingDown
	}
	canonicalAddress := stationPtr.Snapshot().Address
	if result, outcomeErr, handled := m.cachedPowerOutcome(stationPtr, target); handled {
		return result, outcomeErr
	}
	if err := m.beginForegroundStationOperation(canonicalAddress); err != nil {
		return PowerActionResult{}, err
	}
	defer m.endStationOperation(canonicalAddress)
	if result, outcomeErr, handled := m.cachedPowerOutcome(stationPtr, target); handled {
		return result, outcomeErr
	}
	snapshot := stationPtr.Snapshot()
	if err := m.ensureReady(); err != nil {
		return PowerActionResult{}, err
	}
	capabilities := snapshot.Capabilities
	if !snapshot.CapabilitiesKnown ||
		!capabilities.PowerWrite ||
		(target == bluetooth.PowerStateStandby && !capabilities.Standby) {
		err = runSafely("power capability refresh", func() error {
			discoveryContext, cancelDiscovery := context.WithTimeout(m.lifecycleContext, m.initialReadTimeout)
			defer cancelDiscovery()
			var refreshErr error
			if snapshot.CapabilitiesKnown {
				capabilities, refreshErr = m.bluetoothOps.refreshCapabilities(discoveryContext, stationPtr)
			} else {
				capabilities, refreshErr = m.bluetoothOps.ensureCapabilities(discoveryContext, stationPtr)
			}
			return refreshErr
		})
		if err != nil {
			if m.shuttingDown.Load() && errors.Is(err, context.Canceled) {
				return PowerActionResult{}, ErrShuttingDown
			}
			m.observeStationBluetoothError(stationPtr, canonicalAddress, err)
			return PowerActionResult{}, err
		}
	}
	if !capabilities.PowerWrite {
		return PowerActionResult{}, fmt.Errorf("%w: power write is unavailable", ErrUnsupported)
	}
	if target == bluetooth.PowerStateStandby && !capabilities.Standby {
		return PowerActionResult{}, fmt.Errorf("%w: standby is unavailable", ErrUnsupported)
	}
	var controlResult bluetooth.PowerControlResult
	err = runSafely("power operation", func() error {
		var controlErr error
		controlResult, controlErr = m.bluetoothOps.setPowerState(m.lifecycleContext, stationPtr, target)
		return controlErr
	})
	if err != nil {
		m.observeStationBluetoothError(stationPtr, canonicalAddress, err)
		var confirmationErr *bluetooth.PowerConfirmationError
		if errors.As(err, &confirmationErr) {
			if !bluetooth.RequiresReconnect(err) && !bluetooth.IsAdapterUnavailable(err) {
				m.clearStatusFailureKind(canonicalAddress, statusRetryConnection)
			}
			info, infoErr := m.stationInfoByAddress(address)
			if infoErr == nil {
				return PowerActionResult{
					Station:           info,
					CommandSent:       true,
					Confirmed:         false,
					ConfirmationError: err.Error(),
				}, err
			}
		}
		if m.shuttingDown.Load() && errors.Is(err, context.Canceled) {
			return PowerActionResult{}, ErrShuttingDown
		}
		if bluetooth.IsUnsupportedCapabilityError(err) {
			return PowerActionResult{}, fmt.Errorf("%w: %v", ErrUnsupported, err)
		}
		return PowerActionResult{}, err
	}
	info, err := m.stationInfoByAddress(address)
	if err != nil {
		return PowerActionResult{}, err
	}
	m.clearStatusFailureKind(canonicalAddress, statusRetryConnection)
	return PowerActionResult{Station: info, CommandSent: true, Confirmed: controlResult.Confirmed}, nil
}

func (m *Manager) IdentifyStation(address string) error {
	stationPtr, err := m.stationByAddress(address)
	if err != nil {
		return err
	}
	canonicalAddress := stationPtr.Snapshot().Address
	if err := m.beginForegroundStationOperation(canonicalAddress); err != nil {
		return err
	}
	defer m.endStationOperation(canonicalAddress)
	if err := m.ensureReady(); err != nil {
		return err
	}
	capabilities := stationPtr.Snapshot().Capabilities
	if !capabilities.Identify {
		err = runSafely("identify capability refresh", func() error {
			discoveryContext, cancelDiscovery := context.WithTimeout(m.lifecycleContext, m.initialReadTimeout)
			defer cancelDiscovery()
			var refreshErr error
			capabilities, refreshErr = m.bluetoothOps.refreshCapabilities(discoveryContext, stationPtr)
			return refreshErr
		})
		if err != nil {
			if m.shuttingDown.Load() && errors.Is(err, context.Canceled) {
				return ErrShuttingDown
			}
			m.observeStationBluetoothError(stationPtr, canonicalAddress, err)
			return err
		}
	}
	if !capabilities.Identify {
		return fmt.Errorf("%w: identify is unavailable", ErrUnsupported)
	}
	err = runSafely("identify operation", func() error {
		return m.bluetoothOps.identify(m.lifecycleContext, stationPtr)
	})
	if bluetooth.IsUnsupportedCapabilityError(err) {
		return fmt.Errorf("%w: %v", ErrUnsupported, err)
	}
	if err != nil {
		if m.shuttingDown.Load() && errors.Is(err, context.Canceled) {
			return ErrShuttingDown
		}
		return m.observeStationBluetoothError(stationPtr, canonicalAddress, err)
	}
	m.clearStatusFailureKind(canonicalAddress, statusRetryConnection)
	return nil
}

func (m *Manager) RefreshStationCapabilities(address string) (StationInfo, error) {
	stationPtr, err := m.stationByAddress(address)
	if err != nil {
		return StationInfo{}, err
	}
	canonicalAddress := stationPtr.Snapshot().Address
	if err := m.beginForegroundStationOperation(canonicalAddress); err != nil {
		return StationInfo{}, err
	}
	defer m.endStationOperation(canonicalAddress)
	if err := m.ensureReady(); err != nil {
		return StationInfo{}, err
	}
	if err := runSafely("capability refresh", func() error {
		discoveryContext, cancelDiscovery := context.WithTimeout(m.lifecycleContext, m.initialReadTimeout)
		defer cancelDiscovery()
		_, refreshErr := m.bluetoothOps.refreshCapabilities(discoveryContext, stationPtr)
		return refreshErr
	}); err != nil {
		if m.shuttingDown.Load() && errors.Is(err, context.Canceled) {
			return StationInfo{}, ErrShuttingDown
		}
		m.observeStationBluetoothError(stationPtr, canonicalAddress, err)
		return StationInfo{}, err
	}
	if err := runSafely("capability refresh state read", func() error {
		readContext, cancelRead := context.WithTimeout(m.lifecycleContext, m.initialReadTimeout)
		defer cancelRead()
		return m.bluetoothOps.fetchInitialPowerState(readContext, stationPtr)
	}); err != nil {
		if m.shuttingDown.Load() && errors.Is(err, context.Canceled) {
			return StationInfo{}, ErrShuttingDown
		}
		m.observeBluetoothError(err)
		var readErr *bluetooth.InitialReadError
		if !errors.As(err, &readErr) {
			m.observeStationBluetoothError(stationPtr, canonicalAddress, err)
			return StationInfo{}, err
		}
		m.recordStructuredReadResult(stationPtr, canonicalAddress, readErr.Power, readErr.Channel)
		m.recordMetadataReadResult(canonicalAddress, readErr.Metadata)
		// Capability discovery succeeded. Keep the refreshed station visible and
		// expose any unavailable state values through freshness and LastError
		// instead of turning a structured partial read into a total refresh
		// failure.
		return m.stationInfoByAddress(address)
	}
	m.clearStatusFailure(canonicalAddress)
	return m.stationInfoByAddress(address)
}

func (m *Manager) SetStationChannel(
	address string,
	channel int,
	allowUnknownConflictRisk bool,
) (result ChannelChangeResult, returnErr error) {
	result = ChannelChangeResult{Address: address, Warnings: []string{}}
	if channel < 1 || channel > 16 {
		return result, fmt.Errorf("%w: channel must be between 1 and 16", ErrInvalidArgument)
	}
	stationPtr, err := m.stationByAddress(address)
	if err != nil {
		return result, err
	}
	if m.shuttingDown.Load() {
		return result, ErrShuttingDown
	}
	canonicalAddress := stationPtr.Snapshot().Address
	defer func() {
		if info, err := m.stationInfoByAddress(canonicalAddress); err == nil {
			result.Station = info
		}
	}()
	initialSnapshot := stationPtr.Snapshot()
	if initialSnapshot.Channel == channel && isFresh(initialSnapshot.LastChannelReadAt, time.Now()) {
		return ChannelChangeResult{
			Address: initialSnapshot.Address, PreviousChannel: channel, Channel: channel, Confirmed: true, Warnings: []string{},
		}, nil
	}
	if err := m.beginForegroundStationOperation(canonicalAddress); err != nil {
		return result, err
	}
	defer m.endStationOperation(canonicalAddress)
	targetSnapshot := stationPtr.Snapshot()
	result.Address = targetSnapshot.Address
	if targetSnapshot.Channel == channel &&
		isFresh(targetSnapshot.LastChannelReadAt, time.Now()) {
		result.PreviousChannel = channel
		result.Channel = channel
		result.Confirmed = true
		return result, nil
	}
	if targetSnapshot.PowerState == bluetooth.PowerStateBooting &&
		isFresh(targetSnapshot.LastPowerReadAt, time.Now()) {
		return result, fmt.Errorf(
			"station is booting; retry channel change after transition: %w",
			ErrOperationInProgress,
		)
	}
	if err := m.ensureReady(); err != nil {
		return result, err
	}
	if !m.channelOperationMutex.TryLock() {
		return result, ErrOperationInProgress
	}
	defer m.channelOperationMutex.Unlock()
	targetSnapshot = stationPtr.Snapshot()
	result.Address = targetSnapshot.Address
	if !targetSnapshot.Present || targetSnapshot.MissedScans > 0 || targetSnapshot.PresenceUncertain {
		return result, fmt.Errorf("%w: station %s was not seen in the latest scan", ErrNotFound, address)
	}
	if !isRecent(targetSnapshot.LastSeenAt, time.Now(), channelScanFreshnessWindow) {
		return result, fmt.Errorf("%w before changing a channel", ErrScanRequired)
	}
	capabilities := targetSnapshot.Capabilities
	if !capabilities.ChannelRead || !capabilities.ChannelWrite {
		err = runSafely("channel capability refresh", func() error {
			discoveryContext, cancelDiscovery := context.WithTimeout(m.lifecycleContext, m.initialReadTimeout)
			defer cancelDiscovery()
			var refreshErr error
			capabilities, refreshErr = m.bluetoothOps.refreshCapabilities(discoveryContext, stationPtr)
			return refreshErr
		})
		if err != nil {
			if m.shuttingDown.Load() && errors.Is(err, context.Canceled) {
				return result, ErrShuttingDown
			}
			m.observeStationBluetoothError(stationPtr, canonicalAddress, err)
			return result, err
		}
	}
	if !capabilities.ChannelRead || !capabilities.ChannelWrite {
		return result, fmt.Errorf("%w: safe channel changes require read and write support", ErrUnsupported)
	}
	targetSnapshot = stationPtr.Snapshot()
	result.Address = targetSnapshot.Address
	if !targetSnapshot.Present || targetSnapshot.MissedScans > 0 || targetSnapshot.PresenceUncertain {
		return result, fmt.Errorf("%w: station %s was not seen in the latest scan", ErrNotFound, address)
	}
	if !isRecent(targetSnapshot.LastSeenAt, time.Now(), channelScanFreshnessWindow) {
		return result, fmt.Errorf("%w before changing a channel", ErrScanRequired)
	}
	if targetSnapshot.Channel == channel && isFresh(targetSnapshot.LastChannelReadAt, time.Now()) {
		result.PreviousChannel = channel
		result.Channel = channel
		result.Confirmed = true
		return result, nil
	}

	hasUnknown := false
	conflictCheckTime := time.Now()
	m.stationsMutex.RLock()
	for _, other := range m.stations {
		if other == nil || other == stationPtr {
			continue
		}
		snapshot := other.Snapshot()
		if !snapshot.Present {
			continue
		}
		if snapshot.MissedScans > 0 || snapshot.PresenceUncertain ||
			!isRecent(snapshot.LastSeenAt, conflictCheckTime, channelScanFreshnessWindow) ||
			!isFresh(snapshot.LastChannelReadAt, conflictCheckTime) {
			hasUnknown = true
			continue
		}
		if snapshot.Channel == bluetooth.ChannelUnknown {
			hasUnknown = true
			continue
		}
		if snapshot.Channel == channel {
			m.stationsMutex.RUnlock()
			return result, fmt.Errorf("%w: channel %d is used by %s (%s)", ErrChannelConflict, channel, snapshot.Name, snapshot.Address)
		}
	}
	m.stationsMutex.RUnlock()
	if hasUnknown {
		result.Warnings = append(result.Warnings, "One or more visible stations have an unknown channel; conflicts cannot be fully verified.")
		if !allowUnknownConflictRisk {
			return result, fmt.Errorf("%w: one or more visible stations have an unknown channel", ErrScanRequired)
		}
	}

	var writeResult bluetooth.ChannelWriteResult
	err = runSafely("channel operation", func() error {
		var channelErr error
		writeResult, channelErr = m.bluetoothOps.setChannel(m.lifecycleContext, stationPtr, channel)
		return channelErr
	})
	result.PreviousChannel = writeResult.PreviousChannel
	result.Channel = writeResult.Channel
	result.CommandSent = writeResult.CommandSent
	result.Confirmed = err == nil && writeResult.Channel == channel
	if writeResult.WriteWarning != "" {
		result.Warnings = append(result.Warnings, writeResult.WriteWarning)
	}
	if err != nil {
		if m.shuttingDown.Load() && errors.Is(err, context.Canceled) && !writeResult.CommandSent {
			return result, ErrShuttingDown
		}
		if writeResult.CommandSent {
			result.ConfirmationError = err.Error()
		}
		m.observeStationBluetoothError(stationPtr, canonicalAddress, err)
		if writeResult.CommandSent {
			result.Warnings = append(result.Warnings, "The channel command was sent, but its result could not be confirmed.")
		}
		if bluetooth.IsUnsupportedCapabilityError(err) && !writeResult.CommandSent {
			return result, fmt.Errorf("%w: %v", ErrUnsupported, err)
		}
		return result, err
	}
	m.clearStatusFailureKind(canonicalAddress, statusRetryConnection|statusRetryChannel)
	return result, nil
}

func (m *Manager) PowerOnAllStations() error {
	return m.setAllStationsPower("on")
}

func (m *Manager) PowerOffAllStations() error {
	return m.setAllStationsPower("sleep")
}

// SetAllStationsPower applies one stable target to known, writable stations.
// Stations already at the target and stations currently booting are skipped.
func (m *Manager) SetAllStationsPower(state string) error {
	return m.setAllStationsPower(state)
}

func (m *Manager) setAllStationsPower(state string) error {
	result, err := m.setAllStationsPowerDetailed(state)
	if err != nil {
		return err
	}
	var operationErrors []error
	for _, stationResult := range result.Results {
		if !stationResult.Success && !stationResult.Skipped {
			operationErrors = append(operationErrors, fmt.Errorf("%s: %s", stationResult.Address, stationResult.Error))
		}
	}
	if len(operationErrors) > 0 {
		return fmt.Errorf("failed to set one or more stations to %s: %w", result.Target, errors.Join(operationErrors...))
	}
	return nil
}

// SetAllStationsPowerDetailed returns one result per known station.
// Per-device failures are data, not a top-level error, so Wails callers retain
// successful results when only part of a batch fails.
func (m *Manager) SetAllStationsPowerDetailed(state string) (BulkPowerResult, error) {
	return m.setAllStationsPowerDetailed(state)
}

func (m *Manager) cachedBulkPowerResult(target bluetooth.PowerState) (BulkPowerResult, bool) {
	result := BulkPowerResult{Target: target.String(), Results: []BulkPowerStationResult{}}
	for _, info := range m.GetStationInfo() {
		item := BulkPowerStationResult{Address: info.Address, Name: info.Name, Station: info}
		switch classifyCachedPowerInfo(info, target) {
		case cachedPowerBooting:
			item.Skipped = true
			item.Reason = "station is booting"
		case cachedPowerAtTarget:
			item.Skipped = true
			item.Success = true
			item.Confirmed = true
			item.Reason = "already at target state"
		default:
			return BulkPowerResult{}, false
		}
		result.Results = append(result.Results, item)
	}
	return result, true
}

func classifyCachedPowerInfo(info StationInfo, target bluetooth.PowerState) cachedPowerDisposition {
	if !info.PowerFresh {
		return cachedPowerActionable
	}
	if bluetooth.PowerState(info.PowerState) == bluetooth.PowerStateBooting {
		return cachedPowerBooting
	}
	if info.PowerStateConfirmed && bluetooth.PowerState(info.PowerState) == target {
		return cachedPowerAtTarget
	}
	return cachedPowerActionable
}

func (m *Manager) setAllStationsPowerDetailed(state string) (BulkPowerResult, error) {
	target, err := bluetooth.ParsePowerTarget(state)
	result := BulkPowerResult{Target: target.String(), Results: []BulkPowerStationResult{}}
	if err != nil {
		return result, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	if m.shuttingDown.Load() {
		return result, ErrShuttingDown
	}
	if cached, complete := m.cachedBulkPowerResult(target); complete {
		return cached, nil
	}
	if err := m.beginBulkGlobalOperation(); err != nil {
		return result, err
	}
	defer m.endForegroundGlobalOperation()

	type bulkPowerWork struct {
		station     *bluetooth.BaseStation
		resultIndex int
	}
	type bulkPowerCandidate struct {
		station  *bluetooth.BaseStation
		snapshot bluetooth.BaseStationSnapshot
		name     string
	}
	selectionTime := time.Now()
	m.stationsMutex.RLock()
	candidates := make([]bulkPowerCandidate, 0, len(m.stations))
	for _, stationPtr := range m.stations {
		if stationPtr == nil {
			continue
		}
		snapshot := stationPtr.Snapshot()
		name := snapshot.Name
		if renamedName, ok := m.config.GetStationDisplayName(snapshot.Address, snapshot.Name); ok {
			name = renamedName
		}
		candidates = append(candidates, bulkPowerCandidate{station: stationPtr, snapshot: snapshot, name: name})
	}
	m.stationsMutex.RUnlock()
	sort.Slice(candidates, func(i, j int) bool {
		left, right := candidates[i], candidates[j]
		return stationValuesLess(
			left.snapshot.Channel, left.name, left.snapshot.Address,
			right.snapshot.Channel, right.name, right.snapshot.Address,
		)
	})

	work := make([]bulkPowerWork, 0, len(candidates))
	for _, candidate := range candidates {
		stationPtr := candidate.station
		snapshot := candidate.snapshot
		name := candidate.name
		stationResult := BulkPowerStationResult{Address: snapshot.Address, Name: name}
		switch classifyCachedPower(snapshot, target, selectionTime) {
		case cachedPowerBooting:
			stationResult.Skipped = true
			stationResult.Reason = "station is booting"
		case cachedPowerAtTarget:
			stationResult.Skipped = true
			stationResult.Success = true
			stationResult.Confirmed = true
			stationResult.Reason = "already at target state"
		}
		result.Results = append(result.Results, stationResult)
		resultIndex := len(result.Results) - 1
		if !stationResult.Skipped {
			work = append(work, bulkPowerWork{station: stationPtr, resultIndex: resultIndex})
		}
	}

	infoByAddress := make(map[string]StationInfo, len(result.Results))
	for _, info := range m.GetStationInfo() {
		infoByAddress[info.Address] = info
	}
	for index := range result.Results {
		if info, ok := infoByAddress[result.Results[index].Address]; ok {
			result.Results[index].Station = info
			result.Results[index].Name = info.Name
		}
	}
	if len(work) == 0 {
		return result, nil
	}
	if err := m.ensureReady(); err != nil {
		return result, err
	}

	var wg sync.WaitGroup
	semaphore := make(chan struct{}, 2)

	for _, item := range work {
		wg.Add(1)
		go func(resultIndex int, s *bluetooth.BaseStation) {
			defer wg.Done()
			select {
			case semaphore <- struct{}{}:
			case <-m.shutdownCh:
				result.Results[resultIndex].Skipped = true
				result.Results[resultIndex].Reason = "application is shutting down"
				return
			}
			defer func() { <-semaphore }()
			if m.shuttingDown.Load() {
				result.Results[resultIndex].Skipped = true
				result.Results[resultIndex].Reason = "application is shutting down"
				return
			}
			stationResult := BulkPowerStationResult{
				Address: s.Address.String(),
			}
			cachedSkip := false
			workerErr := runSafely("bulk power worker", func() error {
				snapshot := s.Snapshot()
				stationResult.Address = snapshot.Address
				stationResult.Name = snapshot.Name
				switch classifyCachedPower(snapshot, target, time.Now()) {
				case cachedPowerBooting:
					cachedSkip = true
					stationResult.Skipped = true
					stationResult.Reason = "station is booting"
					return nil
				case cachedPowerAtTarget:
					cachedSkip = true
					stationResult.Skipped = true
					stationResult.Success = true
					stationResult.Confirmed = true
					stationResult.Reason = "already at target state"
					return nil
				}
				capabilities := snapshot.Capabilities
				var err error
				discoveryContext, cancelDiscovery := context.WithTimeout(m.lifecycleContext, m.initialReadTimeout)
				defer cancelDiscovery()
				if snapshot.CapabilitiesKnown &&
					(!capabilities.PowerWrite ||
						(target == bluetooth.PowerStateStandby && !capabilities.Standby)) {
					capabilities, err = m.bluetoothOps.refreshCapabilities(discoveryContext, s)
				} else if !snapshot.CapabilitiesKnown {
					capabilities, err = m.bluetoothOps.ensureCapabilities(discoveryContext, s)
				}
				if err == nil && !capabilities.PowerWrite {
					stationResult.Skipped = true
					stationResult.Reason = "power control is not supported"
					return nil
				}
				if err == nil && target == bluetooth.PowerStateStandby && !capabilities.Standby {
					stationResult.Skipped = true
					stationResult.Reason = "standby is not supported"
					return nil
				}
				if err != nil {
					return err
				}
				var controlResult bluetooth.PowerControlResult
				controlResult, err = m.bluetoothOps.setPowerState(m.lifecycleContext, s, target)
				stationResult.CommandSent = err == nil
				stationResult.Confirmed = controlResult.Confirmed
				if err == nil {
					stationResult.Success = true
				}
				return err
			})
			if workerErr != nil {
				var confirmationErr *bluetooth.PowerConfirmationError
				if errors.As(workerErr, &confirmationErr) {
					// A possibly-sent command keeps its command-sent outcome even
					// when shutdown cancelled the confirmation read.
					m.observeStationBluetoothError(s, stationResult.Address, workerErr)
					stationResult.CommandSent = true
					stationResult.Success = true
					stationResult.Confirmed = false
					stationResult.Error = workerErr.Error()
				} else if m.shuttingDown.Load() && errors.Is(workerErr, context.Canceled) {
					stationResult.Skipped = true
					stationResult.Reason = "application is shutting down"
					stationResult.CommandSent = false
					stationResult.Error = ""
					if info, infoErr := m.stationInfoByAddress(stationResult.Address); infoErr == nil {
						stationResult.Station = info
						stationResult.Name = info.Name
					}
					result.Results[resultIndex] = stationResult
					return
				} else {
					m.observeStationBluetoothError(s, stationResult.Address, workerErr)
					if bluetooth.IsUnsupportedCapabilityError(workerErr) {
						stationResult.Skipped = true
						stationResult.Reason = workerErr.Error()
					}
					if !stationResult.Skipped {
						stationResult.Error = workerErr.Error()
					}
				}
			}
			if info, infoErr := m.stationInfoByAddress(stationResult.Address); infoErr == nil {
				stationResult.Station = info
				stationResult.Name = info.Name
			}
			communicationSucceeded := !cachedSkip && (workerErr == nil || stationResult.CommandSent)
			if communicationSucceeded && !bluetooth.RequiresReconnect(workerErr) &&
				!bluetooth.IsAdapterUnavailable(workerErr) {
				m.clearStatusFailureKind(stationResult.Address, statusRetryConnection)
			}
			result.Results[resultIndex] = stationResult
		}(item.resultIndex, item.station)
	}

	wg.Wait()
	return result, nil
}

func (m *Manager) RenameStation(originalName string, newName string) error {
	if err := m.beginForegroundSharedOperation(); err != nil {
		return err
	}
	defer m.endForegroundSharedOperation()
	addresses := make([]string, 0)
	m.stationsMutex.RLock()
	for _, stationPtr := range m.stations {
		if stationPtr == nil {
			continue
		}
		snapshot := stationPtr.Snapshot()
		if snapshot.Name == originalName {
			addresses = append(addresses, snapshot.Address)
		}
	}
	m.stationsMutex.RUnlock()
	if len(addresses) == 0 {
		return fmt.Errorf("%w: no station has original name %q", ErrNotFound, originalName)
	}
	return m.config.SetRenamedStationForAddresses(originalName, newName, addresses)
}

func (m *Manager) RenameStationByAddress(address, newName string) error {
	if err := m.beginForegroundSharedOperation(); err != nil {
		return err
	}
	defer m.endForegroundSharedOperation()
	station, err := m.stationByAddress(address)
	if err != nil {
		return err
	}
	snapshot := station.Snapshot()
	return m.config.SetRenamedStationByAddress(snapshot.Address, snapshot.Name, newName)
}

func (m *Manager) SaveConfig() error {
	if err := m.beginForegroundSharedOperation(); err != nil {
		return err
	}
	defer m.endForegroundSharedOperation()
	return m.config.Save()
}

// BeginShutdown rejects new work and requests cancellation of an active scan
// without waiting. App shutdown uses it before draining the local HTTP server.
func (m *Manager) BeginShutdown() {
	m.shutdownOnce.Do(func() {
		m.lifecycleMutex.Lock()
		m.shuttingDown.Store(true)
		m.cancelLifecycle()
		close(m.shutdownCh)
		m.lifecycleMutex.Unlock()
	})
	m.scanLifecycleMutex.Lock()
	lifecycle := m.scanLifecycle
	m.scanLifecycleMutex.Unlock()
	if lifecycle != nil {
		lifecycle.cancel()
	}
}

func (m *Manager) Shutdown() {
	m.BeginShutdown()
	if err := m.StopScan(); err != nil {
		log.Printf("Bluetooth scan cancellation was incomplete: %v", err)
	}
	m.statusRecoveryWg.Wait()
	m.lifecycleMutex.Lock()
	for m.activeOperations > 0 {
		m.lifecycleCond.Wait()
	}
	m.lifecycleMutex.Unlock()
	m.asyncScanWg.Wait()
	// scanCallbackWg is intentionally not awaited: shutdown may itself be
	// invoked from a scan callback, so waiting for callbacks would
	// self-deadlock. Event emissions are guarded by the caller's shutdown
	// flag instead.
	if err := bluetooth.DisconnectAllStations(); err != nil {
		log.Printf("Bluetooth shutdown cleanup was incomplete: %v", err)
	}
}
