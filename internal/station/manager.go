package station

import (
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
	statusFreshnessWindow      = 45 * time.Second
	channelScanFreshnessWindow = 2 * time.Minute
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
	Address         string   `json:"address"`
	PreviousChannel int      `json:"previousChannel"`
	Channel         int      `json:"channel"`
	Warnings        []string `json:"warnings"`
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
}

type bluetoothOperations struct {
	scanForDuration        func(time.Duration) ([]bluetooth.DiscoveredStation, error)
	readPowerState         func(*bluetooth.BaseStation) error
	fetchInitialPowerState func(*bluetooth.BaseStation) error
	ensureCapabilities     func(*bluetooth.BaseStation) (bluetooth.Capabilities, error)
	refreshCapabilities    func(*bluetooth.BaseStation) (bluetooth.Capabilities, error)
	setPowerState          func(*bluetooth.BaseStation, bluetooth.PowerState) (bluetooth.PowerControlResult, error)
	identify               func(*bluetooth.BaseStation) error
	setChannel             func(*bluetooth.BaseStation, int) (bluetooth.ChannelWriteResult, error)
	disconnectStation      func(*bluetooth.BaseStation)
	releaseStationForScan  func(*bluetooth.BaseStation)
}

type Manager struct {
	stations               map[string]*bluetooth.BaseStation
	stationsMutex          sync.RWMutex
	config                 *config.Config
	operationMutex         sync.RWMutex
	scanTransitionMutex    sync.Mutex
	statusOperationMutex   sync.Mutex
	channelOperationMutex  sync.Mutex
	deviceOperationMutex   sync.Mutex
	activeDeviceOperations map[string]struct{}
	deviceOperationSlots   chan struct{}
	isScanning             atomic.Bool
	scanStatusMutex        sync.RWMutex
	scanStatus             ScanStatus
	initializeMutex        sync.Mutex
	initializeErr          error
	nextInitializeAt       time.Time
	initializeBluetooth    func() error
	asyncScanWg            sync.WaitGroup
	statusRetryMutex       sync.Mutex
	statusRetries          map[string]statusRetry
	statusRecoveryRunning  atomic.Bool
	shuttingDown           atomic.Bool
	shutdownOnce           sync.Once
	shutdownCh             chan struct{}
	lifecycleMutex         sync.Mutex
	lifecycleCond          *sync.Cond
	activeOperations       int
	bluetoothOps           bluetoothOperations
}

type statusRetry struct {
	failures    int
	lastAttempt time.Time
	nextAt      time.Time
}

func NewManager(cfg *config.Config) *Manager {
	manager := &Manager{
		stations:               make(map[string]*bluetooth.BaseStation),
		config:                 cfg,
		activeDeviceOperations: make(map[string]struct{}),
		deviceOperationSlots:   make(chan struct{}, 2),
		statusRetries:          make(map[string]statusRetry),
		scanStatus:             ScanStatus{State: "idle", Warnings: []string{}},
		initializeBluetooth:    bluetooth.Initialize,
		shutdownCh:             make(chan struct{}),
		bluetoothOps: bluetoothOperations{
			scanForDuration:        bluetooth.ScanForDuration,
			readPowerState:         bluetooth.ReadPowerState,
			fetchInitialPowerState: bluetooth.FetchInitialPowerState,
			ensureCapabilities:     bluetooth.EnsureCapabilities,
			refreshCapabilities:    bluetooth.RefreshCapabilities,
			setPowerState:          bluetooth.SetPowerState,
			identify:               bluetooth.Identify,
			setChannel:             bluetooth.SetChannel,
			disconnectStation:      bluetooth.DisconnectStation,
			releaseStationForScan:  bluetooth.ReleaseStationForScan,
		},
	}
	manager.lifecycleCond = sync.NewCond(&manager.lifecycleMutex)
	return manager
}

func (m *Manager) noteStatusFailure(address string) {
	m.statusRetryMutex.Lock()
	retry := m.statusRetries[address]
	retry.failures++
	delay := 30 * time.Second
	for attempt := 1; attempt < retry.failures && delay < 5*time.Minute; attempt++ {
		delay *= 2
	}
	if delay > 5*time.Minute {
		delay = 5 * time.Minute
	}
	retry.lastAttempt = time.Now()
	retry.nextAt = retry.lastAttempt.Add(delay)
	m.statusRetries[address] = retry
	m.statusRetryMutex.Unlock()
}

func (m *Manager) clearStatusFailure(address string) {
	m.statusRetryMutex.Lock()
	delete(m.statusRetries, address)
	m.statusRetryMutex.Unlock()
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
	bluetooth.InvalidateAllConnections()
}

func (m *Manager) observeBluetoothError(err error) error {
	if err != nil {
		m.markBluetoothUnavailable(err)
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

// beginStationOperation rejects duplicate requests for one physical station
// and caps independent GATT work at two devices. It never waits while holding
// the global read lock, so a request flood cannot starve a scan.
func (m *Manager) beginStationOperation(address string) error {
	if err := m.registerOperation(); err != nil {
		return err
	}
	if !m.operationMutex.TryRLock() {
		m.unregisterOperation()
		return ErrOperationInProgress
	}
	select {
	case m.deviceOperationSlots <- struct{}{}:
	default:
		m.operationMutex.RUnlock()
		m.unregisterOperation()
		return ErrOperationInProgress
	}

	key := strings.ToLower(address)
	m.deviceOperationMutex.Lock()
	if _, exists := m.activeDeviceOperations[key]; exists {
		m.deviceOperationMutex.Unlock()
		<-m.deviceOperationSlots
		m.operationMutex.RUnlock()
		m.unregisterOperation()
		return ErrOperationInProgress
	}
	m.activeDeviceOperations[key] = struct{}{}
	m.deviceOperationMutex.Unlock()
	return nil
}

func (m *Manager) endStationOperation(address string) {
	key := strings.ToLower(address)
	m.deviceOperationMutex.Lock()
	delete(m.activeDeviceOperations, key)
	m.deviceOperationMutex.Unlock()
	<-m.deviceOperationSlots
	m.operationMutex.RUnlock()
	m.unregisterOperation()
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
	defer m.stationsMutex.RUnlock()

	snapshots := make([]bluetooth.BaseStationSnapshot, 0, len(m.stations))
	channelCounts := make(map[int]int)
	now := time.Now()
	for _, stationPtr := range m.stations {
		if stationPtr == nil {
			continue
		}
		snapshot := stationPtr.Snapshot()
		snapshots = append(snapshots, snapshot)
		if snapshot.Present &&
			snapshot.MissedScans == 0 &&
			isRecent(snapshot.LastSeenAt, now, channelScanFreshnessWindow) &&
			snapshot.Channel != bluetooth.ChannelUnknown &&
			isFresh(snapshot.LastChannelReadAt, now) {
			channelCounts[snapshot.Channel]++
		}
	}

	stationInfos := make([]StationInfo, 0, len(m.stations))
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
		scanFresh := snapshot.MissedScans == 0 &&
			isRecent(snapshot.LastSeenAt, now, channelScanFreshnessWindow)
		metadataFresh := !snapshot.MetadataReadAt.IsZero()
		stationInfos = append(stationInfos, StationInfo{
			Name:                name,
			OriginalName:        snapshot.Name,
			Address:             snapshot.Address,
			PowerState:          int(snapshot.PowerState),
			PowerStateName:      snapshot.PowerState.String(),
			PowerStateConfirmed: powerFresh && bluetooth.IsPowerStateConfirmed(snapshot.PowerState, snapshot.RawPowerState),
			RawPowerState:       snapshot.RawPowerState,
			Channel:             snapshot.Channel,
			ChannelConflict: snapshot.Present && scanFresh && channelFresh &&
				channelCounts[snapshot.Channel] > 1,
			IsPresent:         snapshot.Present,
			SeenInLatestScan:  snapshot.MissedScans == 0 && !snapshot.LastSeenAt.IsZero(),
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
	return !value.IsZero() && now.Sub(value) <= window
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

func (m *Manager) scanAndFetchStationsSafely() (stations []StationInfo, found int, returnErr error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			returnErr = fmt.Errorf("scan workflow panicked: %v\n%s", recovered, debug.Stack())
			stations = m.GetStationInfo()
			log.Printf("Recovered panic: %v", returnErr)
		}
	}()
	return m.scanAndFetchStations()
}

func (m *Manager) ScanAndFetchStations() ([]StationInfo, error) {
	if err := m.beginScan(ScanCallbacks{}); err != nil {
		return m.GetStationInfo(), err
	}
	stations, found, err := m.scanAndFetchStationsSafely()
	m.finishScan(stations, found, err, ScanCallbacks{})
	return stations, err
}

func (m *Manager) beginScan(callbacks ScanCallbacks) error {
	m.scanTransitionMutex.Lock()
	defer m.scanTransitionMutex.Unlock()
	if err := m.beginOperation(); err != nil {
		return err
	}
	if err := m.ensureReady(); err != nil {
		m.endOperation()
		return err
	}
	m.isScanning.Store(true)
	m.markScanStarted()
	if callbacks.Started != nil {
		if callbackErr := runSafely("scan started callback", func() error {
			callbacks.Started()
			return nil
		}); callbackErr != nil {
			log.Printf("Scan started callback failed: %v", callbackErr)
		}
	}
	return nil
}

func (m *Manager) finishScan(stations []StationInfo, found int, err error, callbacks ScanCallbacks) {
	m.scanTransitionMutex.Lock()
	defer m.scanTransitionMutex.Unlock()
	m.isScanning.Store(false)
	m.endOperation()
	m.markScanFinished(stations, found, err)
	if err != nil {
		if callbacks.Failed != nil {
			if callbackErr := runSafely("scan failure callback", func() error {
				callbacks.Failed(err)
				return nil
			}); callbackErr != nil {
				log.Printf("Scan failure callback failed: %v", callbackErr)
			}
		}
		return
	}
	if callbacks.Completed != nil {
		if callbackErr := runSafely("scan completion callback", func() error {
			callbacks.Completed(stations)
			return nil
		}); callbackErr != nil {
			log.Printf("Scan completion callback failed: %v", callbackErr)
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

func (m *Manager) scanAndFetchStations() ([]StationInfo, int, error) {
	scanDuration := 5 * time.Second

	// A Lighthouse commonly stops advertising while a GATT connection is
	// active. Release our cached connections before a fresh scan so previously
	// discovered stations can advertise again and participate in presence and
	// channel-conflict detection.
	m.stationsMutex.RLock()
	connectedStations := make([]*bluetooth.BaseStation, 0, len(m.stations))
	for _, stationPtr := range m.stations {
		if stationPtr != nil && stationPtr.Snapshot().Connected {
			connectedStations = append(connectedStations, stationPtr)
		}
	}
	m.stationsMutex.RUnlock()
	for _, stationPtr := range connectedStations {
		m.bluetoothOps.releaseStationForScan(stationPtr)
	}
	if len(connectedStations) > 0 {
		// TinyGo's Windows Disconnect is non-blocking. Give the OS a short
		// window to release the GATT session before starting advertisement scan.
		time.Sleep(250 * time.Millisecond)
	}

	discoveredValues, err := m.bluetoothOps.scanForDuration(scanDuration)
	if err != nil {
		m.observeBluetoothError(err)
		return m.GetStationInfo(), 0, fmt.Errorf("bluetooth scan failed: %w", err)
	}

	stationsToFetch := make([]*bluetooth.BaseStation, 0)
	scanTime := time.Now()
	m.stationsMutex.Lock()
	for _, stationPtr := range m.stations {
		if stationPtr != nil {
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

	if len(stationsToFetch) > 0 {
		var wg sync.WaitGroup
		type initialReadResult struct {
			address string
			station *bluetooth.BaseStation
			err     error
		}
		readResults := make([]initialReadResult, len(stationsToFetch))
		semaphore := make(chan struct{}, 2)
		for index, stationToFetch := range stationsToFetch {
			wg.Add(1)
			go func(resultIndex int, ptr *bluetooth.BaseStation) {
				defer wg.Done()
				semaphore <- struct{}{}
				defer func() { <-semaphore }()
				readResults[resultIndex] = initialReadResult{
					address: ptr.Snapshot().Address,
					station: ptr,
					err: runSafely("initial station read", func() error {
						return m.bluetoothOps.fetchInitialPowerState(ptr)
					}),
				}
			}(index, stationToFetch)
		}
		wg.Wait()
		sort.Slice(readResults, func(i, j int) bool {
			return strings.ToLower(readResults[i].address) < strings.ToLower(readResults[j].address)
		})
		readErrors := make([]error, 0)
		for _, result := range readResults {
			if result.err == nil {
				m.clearStatusFailure(result.address)
				continue
			}
			m.observeBluetoothError(result.err)
			var initialErr *bluetooth.InitialReadError
			if !errors.As(result.err, &initialErr) || initialErr.Power != nil {
				m.bluetoothOps.disconnectStation(result.station)
				m.noteStatusFailure(result.address)
			} else {
				m.clearStatusFailure(result.address)
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

func (m *Manager) IsScanning() bool {
	return m.isScanning.Load()
}

func (m *Manager) markScanStarted() {
	m.scanStatusMutex.Lock()
	m.scanStatus = ScanStatus{
		State:     "running",
		StartedAt: time.Now().Format(time.RFC3339Nano),
		Warnings:  []string{},
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
	if err != nil {
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

// StartScan reserves the Bluetooth adapter, emits Started synchronously, and
// completes the scan asynchronously. Completion callbacks run only after the
// scan state and operation lock have been released.
func (m *Manager) StartScan(callbacks ScanCallbacks) error {
	if err := m.beginScan(callbacks); err != nil {
		return err
	}
	m.asyncScanWg.Add(1)
	go func() {
		defer m.asyncScanWg.Done()
		stations, found, err := m.scanAndFetchStationsSafely()
		m.finishScan(stations, found, err, callbacks)
	}()
	return nil
}

func (m *Manager) CheckAllStationStatuses() ([]StationInfo, error) {
	if err := m.beginSharedOperation(); err != nil {
		return m.GetStationInfo(), err
	}
	defer m.endSharedOperation()
	if err := m.ensureReady(); err != nil {
		return m.GetStationInfo(), err
	}
	if !m.statusOperationMutex.TryLock() {
		return m.GetStationInfo(), nil
	}
	defer m.statusOperationMutex.Unlock()
	stationsToRead := make([]*bluetooth.BaseStation, 0)
	m.stationsMutex.RLock()
	for _, stationPtr := range m.stations {
		if stationPtr == nil {
			continue
		}
		snapshot := stationPtr.Snapshot()
		if !snapshot.Present {
			continue
		}
		if snapshot.Connected {
			stationsToRead = append(stationsToRead, stationPtr)
		}
	}
	m.stationsMutex.RUnlock()

	if len(stationsToRead) == 0 {
		m.scheduleStatusRecovery()
		return m.GetStationInfo(), nil
	}

	var statusErrors []error
	work := make(chan *bluetooth.BaseStation)
	workerCount := 2
	if len(stationsToRead) < workerCount {
		workerCount = len(stationsToRead)
	}
	var wg sync.WaitGroup
	var statusErrorsMutex sync.Mutex
	for worker := 0; worker < workerCount; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ptr := range work {
				address := ptr.Snapshot().Address
				if err := m.beginStationOperation(address); err != nil {
					continue
				}
				func() {
					defer m.endStationOperation(address)
					workerErr := runSafely("station status worker", func() error {
						return m.bluetoothOps.readPowerState(ptr)
					})
					if workerErr != nil {
						m.observeBluetoothError(workerErr)
						var readErr *bluetooth.StatusReadError
						if !errors.As(workerErr, &readErr) || readErr.Power != nil {
							m.bluetoothOps.disconnectStation(ptr)
							m.noteStatusFailure(address)
						} else {
							m.clearStatusFailure(address)
						}
						statusErrorsMutex.Lock()
						statusErrors = append(statusErrors, fmt.Errorf("%s: %w", ptr.Address.String(), workerErr))
						statusErrorsMutex.Unlock()
					} else {
						m.clearStatusFailure(address)
					}
				}()
			}
		}()
	}
	for _, station := range stationsToRead {
		work <- station
	}
	close(work)
	wg.Wait()
	m.scheduleStatusRecovery()

	statusInfos := m.GetStationInfo()
	if len(statusErrors) > 0 {
		return statusInfos, fmt.Errorf("one or more station status reads failed: %w", errors.Join(statusErrors...))
	}
	return statusInfos, nil
}

func (m *Manager) scheduleStatusRecovery() {
	if !m.statusRecoveryRunning.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer m.statusRecoveryRunning.Store(false)
		select {
		case <-m.shutdownCh:
			return
		default:
		}
		if m.shuttingDown.Load() || m.isScanning.Load() {
			return
		}
		now := time.Now()
		type recoveryCandidate struct {
			station *bluetooth.BaseStation
			address string
			retry   statusRetry
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
			if !snapshot.Present || snapshot.Connected {
				continue
			}
			retry, tracked := retries[snapshot.Address]
			if !tracked || !now.Before(retry.nextAt) {
				candidates = append(candidates, recoveryCandidate{
					station: station,
					address: snapshot.Address,
					retry:   retry,
				})
			}
		}
		m.stationsMutex.RUnlock()
		if len(candidates) == 0 {
			return
		}
		sort.Slice(candidates, func(i, j int) bool {
			left, right := candidates[i], candidates[j]
			if !left.retry.nextAt.Equal(right.retry.nextAt) {
				return left.retry.nextAt.Before(right.retry.nextAt)
			}
			if left.retry.failures != right.retry.failures {
				return left.retry.failures < right.retry.failures
			}
			if !left.retry.lastAttempt.Equal(right.retry.lastAttempt) {
				return left.retry.lastAttempt.Before(right.retry.lastAttempt)
			}
			return strings.ToLower(left.address) < strings.ToLower(right.address)
		})
		var wg sync.WaitGroup
		started := 0
		for _, candidate := range candidates {
			if started == 1 {
				break
			}
			if err := m.beginStationOperation(candidate.address); err != nil {
				continue
			}
			started++
			wg.Add(1)
			go func(candidate recoveryCandidate) {
				defer wg.Done()
				defer m.endStationOperation(candidate.address)
				err := runSafely("station status recovery", func() error {
					return m.bluetoothOps.fetchInitialPowerState(candidate.station)
				})
				m.observeBluetoothError(err)
				var initialErr *bluetooth.InitialReadError
				if errors.As(err, &initialErr) && initialErr.Power == nil {
					m.clearStatusFailure(candidate.address)
					return
				}
				if err != nil {
					m.bluetoothOps.disconnectStation(candidate.station)
					m.noteStatusFailure(candidate.address)
					return
				}
				m.clearStatusFailure(candidate.address)
			}(candidate)
		}
		wg.Wait()
	}()
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
func (m *Manager) SetStationPower(address, state string) (PowerActionResult, error) {
	target, err := bluetooth.ParsePowerTarget(state)
	if err != nil {
		return PowerActionResult{}, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	stationPtr, err := m.stationByAddress(address)
	if err != nil {
		return PowerActionResult{}, err
	}
	canonicalAddress := stationPtr.Snapshot().Address
	if err := m.beginStationOperation(canonicalAddress); err != nil {
		return PowerActionResult{}, err
	}
	defer m.endStationOperation(canonicalAddress)
	if err := m.ensureReady(); err != nil {
		return PowerActionResult{}, err
	}
	snapshot := stationPtr.Snapshot()
	capabilities := snapshot.Capabilities
	if !snapshot.CapabilitiesKnown || !capabilities.PowerWrite {
		err = runSafely("power capability refresh", func() error {
			var refreshErr error
			if snapshot.CapabilitiesKnown {
				capabilities, refreshErr = m.bluetoothOps.refreshCapabilities(stationPtr)
			} else {
				capabilities, refreshErr = m.bluetoothOps.ensureCapabilities(stationPtr)
			}
			return refreshErr
		})
		if err != nil {
			m.observeBluetoothError(err)
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
		controlResult, controlErr = m.bluetoothOps.setPowerState(stationPtr, target)
		return controlErr
	})
	if err != nil {
		m.observeBluetoothError(err)
		var confirmationErr *bluetooth.PowerConfirmationError
		if errors.As(err, &confirmationErr) {
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
		return PowerActionResult{}, err
	}
	info, err := m.stationInfoByAddress(address)
	if err != nil {
		return PowerActionResult{}, err
	}
	m.clearStatusFailure(canonicalAddress)
	return PowerActionResult{Station: info, CommandSent: true, Confirmed: controlResult.Confirmed}, nil
}

func (m *Manager) IdentifyStation(address string) error {
	stationPtr, err := m.stationByAddress(address)
	if err != nil {
		return err
	}
	canonicalAddress := stationPtr.Snapshot().Address
	if err := m.beginStationOperation(canonicalAddress); err != nil {
		return err
	}
	defer m.endStationOperation(canonicalAddress)
	if err := m.ensureReady(); err != nil {
		return err
	}
	capabilities := stationPtr.Snapshot().Capabilities
	if !capabilities.Identify {
		err = runSafely("identify capability refresh", func() error {
			var refreshErr error
			capabilities, refreshErr = m.bluetoothOps.refreshCapabilities(stationPtr)
			return refreshErr
		})
		if err != nil {
			m.observeBluetoothError(err)
			return err
		}
	}
	if !capabilities.Identify {
		return fmt.Errorf("%w: identify is unavailable", ErrUnsupported)
	}
	err = runSafely("identify operation", func() error {
		return m.bluetoothOps.identify(stationPtr)
	})
	return m.observeBluetoothError(err)
}

func (m *Manager) RefreshStationCapabilities(address string) (StationInfo, error) {
	stationPtr, err := m.stationByAddress(address)
	if err != nil {
		return StationInfo{}, err
	}
	canonicalAddress := stationPtr.Snapshot().Address
	if err := m.beginStationOperation(canonicalAddress); err != nil {
		return StationInfo{}, err
	}
	defer m.endStationOperation(canonicalAddress)
	if err := m.ensureReady(); err != nil {
		return StationInfo{}, err
	}
	if err := runSafely("capability refresh", func() error {
		_, refreshErr := m.bluetoothOps.refreshCapabilities(stationPtr)
		return refreshErr
	}); err != nil {
		m.observeBluetoothError(err)
		return StationInfo{}, err
	}
	if err := runSafely("capability refresh state read", func() error {
		return m.bluetoothOps.fetchInitialPowerState(stationPtr)
	}); err != nil {
		m.observeBluetoothError(err)
		var readErr *bluetooth.InitialReadError
		if !errors.As(err, &readErr) {
			m.bluetoothOps.disconnectStation(stationPtr)
			m.noteStatusFailure(canonicalAddress)
			return StationInfo{}, err
		}
		if readErr.Power != nil {
			m.bluetoothOps.disconnectStation(stationPtr)
			m.noteStatusFailure(canonicalAddress)
			return StationInfo{}, err
		}
		m.clearStatusFailure(canonicalAddress)
		return m.stationInfoByAddress(address)
	}
	m.clearStatusFailure(canonicalAddress)
	return m.stationInfoByAddress(address)
}

func (m *Manager) SetStationChannel(address string, channel int, allowUnknownConflictRisk bool) (ChannelChangeResult, error) {
	result := ChannelChangeResult{Address: address, Warnings: []string{}}
	if channel < 1 || channel > 16 {
		return result, fmt.Errorf("%w: channel must be between 1 and 16", ErrInvalidArgument)
	}
	stationPtr, err := m.stationByAddress(address)
	if err != nil {
		return result, err
	}
	canonicalAddress := stationPtr.Snapshot().Address
	if err := m.beginStationOperation(canonicalAddress); err != nil {
		return result, err
	}
	defer m.endStationOperation(canonicalAddress)
	if err := m.ensureReady(); err != nil {
		return result, err
	}
	if !m.channelOperationMutex.TryLock() {
		return result, ErrOperationInProgress
	}
	defer m.channelOperationMutex.Unlock()
	targetSnapshot := stationPtr.Snapshot()
	result.Address = targetSnapshot.Address
	if !targetSnapshot.Present || targetSnapshot.MissedScans > 0 {
		return result, fmt.Errorf("%w: station %s was not seen in the latest scan", ErrNotFound, address)
	}
	if !isRecent(targetSnapshot.LastSeenAt, time.Now(), channelScanFreshnessWindow) {
		return result, fmt.Errorf("%w before changing a channel", ErrScanRequired)
	}
	capabilities := targetSnapshot.Capabilities
	if !capabilities.ChannelRead || !capabilities.ChannelWrite {
		err = runSafely("channel capability refresh", func() error {
			var refreshErr error
			capabilities, refreshErr = m.bluetoothOps.refreshCapabilities(stationPtr)
			return refreshErr
		})
		if err != nil {
			m.observeBluetoothError(err)
			return result, err
		}
	}
	if !capabilities.ChannelRead || !capabilities.ChannelWrite {
		return result, fmt.Errorf("%w: safe channel changes require read and write support", ErrUnsupported)
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
		if snapshot.MissedScans > 0 ||
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
		writeResult, channelErr = m.bluetoothOps.setChannel(stationPtr, channel)
		return channelErr
	})
	result.PreviousChannel = writeResult.PreviousChannel
	result.Channel = writeResult.Channel
	if writeResult.WriteWarning != "" {
		result.Warnings = append(result.Warnings, writeResult.WriteWarning)
	}
	if err != nil {
		m.observeBluetoothError(err)
		return result, err
	}
	m.clearStatusFailure(canonicalAddress)
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

func (m *Manager) setAllStationsPowerDetailed(state string) (BulkPowerResult, error) {
	target, err := bluetooth.ParsePowerTarget(state)
	result := BulkPowerResult{Target: target.String(), Results: []BulkPowerStationResult{}}
	if err != nil {
		return result, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	if err := m.beginOperation(); err != nil {
		return result, err
	}
	defer m.endOperation()
	if err := m.ensureReady(); err != nil {
		return result, err
	}

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
		switch {
		case snapshot.PowerState == bluetooth.PowerStateBooting && isFresh(snapshot.LastPowerReadAt, selectionTime):
			stationResult.Skipped = true
			stationResult.Reason = "station is booting"
		case snapshot.CapabilitiesKnown && target == bluetooth.PowerStateStandby &&
			snapshot.Capabilities.PowerWrite &&
			!snapshot.Capabilities.Standby:
			stationResult.Skipped = true
			stationResult.Reason = "standby is not supported"
		case snapshot.PowerState == target &&
			isFresh(snapshot.LastPowerReadAt, selectionTime) &&
			bluetooth.IsPowerStateConfirmed(snapshot.PowerState, snapshot.RawPowerState):
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

	var wg sync.WaitGroup
	semaphore := make(chan struct{}, 2)

	for _, item := range work {
		wg.Add(1)
		go func(resultIndex int, s *bluetooth.BaseStation) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			stationResult := BulkPowerStationResult{
				Address: s.Address.String(),
			}
			workerErr := runSafely("bulk power worker", func() error {
				snapshot := s.Snapshot()
				stationResult.Address = snapshot.Address
				stationResult.Name = snapshot.Name
				capabilities := snapshot.Capabilities
				var err error
				if snapshot.CapabilitiesKnown && capabilities.PowerWrite {
					capabilities, err = m.bluetoothOps.ensureCapabilities(s)
				} else if snapshot.CapabilitiesKnown {
					capabilities, err = m.bluetoothOps.refreshCapabilities(s)
				} else {
					capabilities, err = m.bluetoothOps.ensureCapabilities(s)
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
				controlResult, err = m.bluetoothOps.setPowerState(s, target)
				stationResult.CommandSent = err == nil
				stationResult.Confirmed = controlResult.Confirmed
				if err == nil {
					stationResult.Success = true
				}
				return err
			})
			if workerErr != nil {
				m.observeBluetoothError(workerErr)
				var confirmationErr *bluetooth.PowerConfirmationError
				if errors.As(workerErr, &confirmationErr) {
					stationResult.CommandSent = true
					stationResult.Success = true
				}
				stationResult.Error = workerErr.Error()
			}
			if info, infoErr := m.stationInfoByAddress(stationResult.Address); infoErr == nil {
				stationResult.Station = info
				stationResult.Name = info.Name
			}
			if stationResult.Success {
				m.clearStatusFailure(stationResult.Address)
			}
			result.Results[resultIndex] = stationResult
		}(item.resultIndex, item.station)
	}

	wg.Wait()
	return result, nil
}

func (m *Manager) RenameStation(originalName string, newName string) error {
	if err := m.beginSharedOperation(); err != nil {
		return err
	}
	defer m.endSharedOperation()
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
	return m.config.SetRenamedStationForAddresses(originalName, newName, addresses)
}

func (m *Manager) RenameStationByAddress(address, newName string) error {
	if err := m.beginSharedOperation(); err != nil {
		return err
	}
	defer m.endSharedOperation()
	station, err := m.stationByAddress(address)
	if err != nil {
		return err
	}
	snapshot := station.Snapshot()
	return m.config.SetRenamedStationByAddress(snapshot.Address, snapshot.Name, newName)
}

func (m *Manager) SaveConfig() error {
	if err := m.beginSharedOperation(); err != nil {
		return err
	}
	defer m.endSharedOperation()
	return m.config.Save()
}

// BeginShutdown rejects new work and requests cancellation of an active scan
// without waiting. App shutdown uses it before draining the local HTTP server.
func (m *Manager) BeginShutdown() {
	m.shutdownOnce.Do(func() {
		m.lifecycleMutex.Lock()
		m.shuttingDown.Store(true)
		close(m.shutdownCh)
		m.lifecycleMutex.Unlock()
	})
	if m.isScanning.Load() {
		if err := bluetooth.CancelScan(); err != nil {
			log.Printf("Unable to stop active Bluetooth scan during shutdown: %v", err)
		}
	}
}

func (m *Manager) Shutdown() {
	m.BeginShutdown()
	m.lifecycleMutex.Lock()
	for m.activeOperations > 0 {
		m.lifecycleCond.Wait()
	}
	m.lifecycleMutex.Unlock()
	m.asyncScanWg.Wait()
	bluetooth.DisconnectAllStations()
}
