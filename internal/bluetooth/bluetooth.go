package bluetooth

import (
	"context"
	"errors"
	"fmt"
	"log"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"tinygo.org/x/bluetooth"
)

var (
	adapter bluetooth.BLEAdapter = bluetooth.DefaultAdapter

	// UUIDs
	powerControlServiceUUIDString        = "00001523-1212-efde-1523-785feabcd124"
	modeCharacteristicUUIDString         = "00001524-1212-efde-1523-785feabcd124"
	powerControlCharacteristicUUIDString = "00001525-1212-efde-1523-785feabcd124"
	identifyCharacteristicUUIDString     = "00008421-1212-efde-1523-785feabcd124"
	deviceInformationServiceUUIDString   = "0000180a-0000-1000-8000-00805f9b34fb"
	manufacturerCharacteristicUUIDString = "00002a29-0000-1000-8000-00805f9b34fb"
	modelCharacteristicUUIDString        = "00002a24-0000-1000-8000-00805f9b34fb"
	serialCharacteristicUUIDString       = "00002a25-0000-1000-8000-00805f9b34fb"
	hardwareCharacteristicUUIDString     = "00002a27-0000-1000-8000-00805f9b34fb"
	firmwareCharacteristicUUIDString     = "00002a26-0000-1000-8000-00805f9b34fb"
	powerControlServiceUUID              bluetooth.UUID
	modeCharacteristicUUID               bluetooth.UUID
	powerControlCharacteristicUUID       bluetooth.UUID
	identifyCharacteristicUUID           bluetooth.UUID
	deviceInformationServiceUUID         bluetooth.UUID
	manufacturerCharacteristicUUID       bluetooth.UUID
	modelCharacteristicUUID              bluetooth.UUID
	serialCharacteristicUUID             bluetooth.UUID
	hardwareCharacteristicUUID           bluetooth.UUID
	firmwareCharacteristicUUID           bluetooth.UUID

	// Track connected stations for cleanup
	connectedStations      []*BaseStation
	pendingCleanupStations []*BaseStation
	connectedStationsMutex sync.Mutex
	uuidInitOnce           sync.Once
	uuidInitErr            error
	activeScanMutex        sync.Mutex
	activeScan             *scanSession
)

const (
	characteristicPropertyRead                 = uint32(0x02)
	characteristicPropertyWriteWithoutResponse = uint32(0x04)
	characteristicPropertyWrite                = uint32(0x08)
	characteristicPropertyNotify               = uint32(0x10)
	characteristicPropertyIndicate             = uint32(0x20)
)

type characteristicIO interface {
	Read([]byte) (int, error)
	Write([]byte) (int, error)
	WriteWithoutResponse([]byte) (int, error)
	Properties() uint32
}

type contextAdapter interface {
	ConnectContext(context.Context, bluetooth.Address, bluetooth.ConnectionParams) (bluetooth.Device, error)
}

type contextDevice interface {
	DiscoverServicesContext(context.Context, []bluetooth.UUID) ([]bluetooth.DeviceService, error)
}

type contextService interface {
	DiscoverCharacteristicsContext(context.Context, []bluetooth.UUID) ([]bluetooth.DeviceCharacteristic, error)
}

type contextCharacteristic interface {
	ReadContext(context.Context, []byte) (int, error)
}

func normalizeContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func connectContext(ctx context.Context, address bluetooth.Address) (bluetooth.Device, error) {
	ctx = normalizeContext(ctx)
	if err := ctx.Err(); err != nil {
		return bluetooth.Device{}, err
	}
	if contextual, ok := adapter.(contextAdapter); ok {
		return contextual.ConnectContext(ctx, address, bluetooth.ConnectionParams{})
	}
	return adapter.Connect(address, bluetooth.ConnectionParams{})
}

func discoverServicesContext(ctx context.Context, device bluetooth.GAPDevice) ([]bluetooth.DeviceService, error) {
	ctx = normalizeContext(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if contextual, ok := device.(contextDevice); ok {
		return contextual.DiscoverServicesContext(ctx, nil)
	}
	return device.DiscoverServices(nil)
}

func discoverCharacteristicsContext(ctx context.Context, service bluetooth.DeviceService) ([]bluetooth.DeviceCharacteristic, error) {
	ctx = normalizeContext(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if contextual, ok := any(service).(contextService); ok {
		return contextual.DiscoverCharacteristicsContext(ctx, nil)
	}
	return service.DiscoverCharacteristics(nil)
}

func readCharacteristicContext(ctx context.Context, characteristic characteristicIO, data []byte) (int, error) {
	ctx = normalizeContext(ctx)
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if contextual, ok := characteristic.(contextCharacteristic); ok {
		return contextual.ReadContext(ctx, data)
	}
	return characteristic.Read(data)
}

// BaseStation represents a discovered SteamVR Base Station.
type BaseStation struct {
	Name              string
	Address           bluetooth.Address
	PowerState        PowerState
	RawPowerState     int
	Channel           int
	Present           bool
	Capabilities      Capabilities
	CapabilitiesKnown bool
	Metadata          DeviceMetadata
	// Fields for storing handles and state
	device                 bluetooth.GAPDevice
	pendingCleanup         bluetooth.GAPDevice
	characteristic         characteristicIO
	modeCharacteristic     characteristicIO
	identifyCharacteristic characteristicIO
	isConnected            bool
	// Add Mutex for thread-safe access
	mutex             sync.RWMutex
	LastStateUpdate   time.Time // Track when state was last read
	LastSeenAt        time.Time
	LastReadAt        time.Time
	LastPowerReadAt   time.Time
	LastChannelReadAt time.Time
	MetadataReadAt    time.Time
	LastError         string
	MissedScans       int
	bootingSince      time.Time
	presenceUncertain bool
	connectionError   string
	powerError        string
	channelError      string
	metadataError     string
	metadataReadError error
	operationError    string
}

// PossiblySentError reports that a write failed after the transport may have
// accepted the command. Such commands must be confirmed, never replayed.
type PossiblySentError struct {
	Err error
}

func (e *PossiblySentError) Error() string {
	return fmt.Sprintf("command may have been sent: %v", e.Err)
}

func (e *PossiblySentError) Unwrap() error {
	return e.Err
}

func (e *PossiblySentError) PossiblySent() bool {
	return true
}

// IsPossiblySent accepts the application marker and compatible transport
// markers if tinygo-bluetooth adds its own typed error.
func IsPossiblySent(err error) bool {
	possiblySent, classified := possiblySentClassification(err)
	return classified && possiblySent
}

func possiblySentClassification(err error) (possiblySent, classified bool) {
	var marker interface{ PossiblySent() bool }
	if errors.As(err, &marker) {
		return marker.PossiblySent(), true
	}
	var alternate interface{ MayHaveBeenSent() bool }
	if errors.As(err, &alternate) {
		return alternate.MayHaveBeenSent(), true
	}
	return false, false
}

// DiscoveredStation contains only immutable scan data. Keeping the mutex-bearing
// BaseStation out of scan results avoids copying a sync.RWMutex.
type DiscoveredStation struct {
	Name    string
	Address bluetooth.Address
}

var ErrScanCancelled = errors.New("Bluetooth scan cancelled")

type scanStopReason uint32

const (
	scanStopNone scanStopReason = iota
	scanStopDuration
	scanStopCancelled
)

type scanSession struct {
	mutex       sync.Mutex
	doneOnce    sync.Once
	reason      atomic.Uint32
	started     bool
	finished    bool
	stopStarted bool
	stopDone    chan struct{}
	stopErr     error
}

func newScanSession() *scanSession {
	return &scanSession{stopDone: make(chan struct{})}
}

func (s *scanSession) requestStop(reason scanStopReason) error {
	s.requestStopAsync(reason)
	s.mutex.Lock()
	// A platform watcher has not started yet, so there is no StopScan call to
	// await. markStarted will issue the recorded cancellation when it arrives.
	pendingStart := !s.started && !s.finished
	s.mutex.Unlock()
	if pendingStart {
		return nil
	}
	<-s.stopDone
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.stopErr
}

func (s *scanSession) requestStopAsync(reason scanStopReason) {
	s.mutex.Lock()
	currentReason := scanStopReason(s.reason.Load())
	if currentReason == scanStopNone || reason == scanStopCancelled {
		s.reason.Store(uint32(reason))
	}
	shouldStop := s.started && !s.finished
	s.mutex.Unlock()
	if shouldStop {
		s.issueStop()
	}
}

func (s *scanSession) issueStop() {
	s.mutex.Lock()
	if s.stopStarted || s.finished || !s.started {
		s.mutex.Unlock()
		return
	}
	s.stopStarted = true
	s.mutex.Unlock()
	go func() {
		err := stopScanSafely()
		s.mutex.Lock()
		s.stopErr = err
		s.mutex.Unlock()
		s.doneOnce.Do(func() { close(s.stopDone) })
	}()
}

func (s *scanSession) markStarted() {
	s.mutex.Lock()
	s.started = true
	pendingStop := s.reason.Load() != uint32(scanStopNone) && !s.finished
	s.mutex.Unlock()
	if pendingStop {
		s.issueStop()
	}
}

func (s *scanSession) markFinished() {
	s.mutex.Lock()
	s.finished = true
	stopStarted := s.stopStarted
	s.mutex.Unlock()
	if !stopStarted {
		s.doneOnce.Do(func() { close(s.stopDone) })
	}
}

func (s *scanSession) waitForIssuedStop() {
	s.mutex.Lock()
	stopStarted := s.stopStarted
	s.mutex.Unlock()
	if stopStarted {
		<-s.stopDone
	}
}

func (s *scanSession) stopReason() scanStopReason {
	return scanStopReason(s.reason.Load())
}

// IsConnected returns the current connection status safely.
func (bs *BaseStation) IsConnected() bool {
	bs.mutex.Lock()
	defer bs.mutex.Unlock()
	if !bs.isConnected || bs.device == nil {
		return false
	}
	connected, err := bs.device.Connected()
	if err == nil && connected {
		return true
	}
	if err != nil {
		bs.setConnectionErrorInternal(err)
	}
	_ = disconnectInternal(bs)
	return false
}

// setPowerStateInternal updates the power state and timestamp safely.
// Assumes caller holds the write lock (bs.mutex.Lock()).
func (bs *BaseStation) setPowerStateInternal(state PowerState, raw int) {
	if state == PowerStateBooting {
		if bs.PowerState != PowerStateBooting || bs.bootingSince.IsZero() {
			bs.bootingSince = time.Now()
		}
	} else {
		bs.bootingSince = time.Time{}
	}
	bs.PowerState = state
	bs.RawPowerState = raw
	bs.LastStateUpdate = time.Now()
}

func (bs *BaseStation) updateLastReadInternal(readAt time.Time) {
	if readAt.After(bs.LastReadAt) {
		bs.LastReadAt = readAt
	}
}

func (bs *BaseStation) refreshLastErrorInternal() {
	parts := make([]string, 0, 5)
	for _, item := range []struct {
		label string
		value string
	}{
		{label: "connection", value: bs.connectionError},
		{label: "power", value: bs.powerError},
		{label: "channel", value: bs.channelError},
		{label: "metadata", value: bs.metadataError},
		{label: "operation", value: bs.operationError},
	} {
		if item.value != "" {
			parts = append(parts, fmt.Sprintf("%s: %s", item.label, item.value))
		}
	}
	bs.LastError = strings.Join(parts, "; ")
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (bs *BaseStation) setConnectionErrorInternal(err error) {
	bs.connectionError = errorText(err)
	bs.refreshLastErrorInternal()
}

func (bs *BaseStation) setPowerErrorInternal(err error) {
	bs.powerError = errorText(err)
	bs.refreshLastErrorInternal()
}

func (bs *BaseStation) setChannelErrorInternal(err error) {
	bs.channelError = errorText(err)
	bs.refreshLastErrorInternal()
}

func (bs *BaseStation) setMetadataErrorInternal(err error) {
	bs.metadataError = errorText(err)
	bs.metadataReadError = err
	bs.refreshLastErrorInternal()
}

func (bs *BaseStation) setOperationErrorInternal(err error) {
	bs.operationError = errorText(err)
	bs.refreshLastErrorInternal()
}

// GetPowerState reads the power state safely.
func (bs *BaseStation) GetPowerState() int {
	bs.mutex.RLock()
	defer bs.mutex.RUnlock()
	return int(bs.PowerState)
}

// GetChannel returns the cached optical channel (1-16), or 0 when unknown.
func (bs *BaseStation) GetChannel() int {
	bs.mutex.RLock()
	defer bs.mutex.RUnlock()
	return bs.Channel
}

type BaseStationSnapshot struct {
	Name              string
	Address           string
	PowerState        PowerState
	RawPowerState     int
	Channel           int
	Present           bool
	Capabilities      Capabilities
	CapabilitiesKnown bool
	Metadata          DeviceMetadata
	LastStateUpdate   time.Time
	LastSeenAt        time.Time
	LastReadAt        time.Time
	LastPowerReadAt   time.Time
	LastChannelReadAt time.Time
	MetadataReadAt    time.Time
	LastError         string
	MissedScans       int
	Connected         bool
	PresenceUncertain bool
}

func (bs *BaseStation) Snapshot() BaseStationSnapshot {
	bs.mutex.RLock()
	defer bs.mutex.RUnlock()
	return BaseStationSnapshot{
		Name:              bs.Name,
		Address:           bs.Address.String(),
		PowerState:        bs.PowerState,
		RawPowerState:     bs.RawPowerState,
		Channel:           bs.Channel,
		Present:           bs.Present,
		Capabilities:      bs.Capabilities,
		CapabilitiesKnown: bs.CapabilitiesKnown,
		Metadata:          bs.Metadata,
		LastStateUpdate:   bs.LastStateUpdate,
		LastSeenAt:        bs.LastSeenAt,
		LastReadAt:        bs.LastReadAt,
		LastPowerReadAt:   bs.LastPowerReadAt,
		LastChannelReadAt: bs.LastChannelReadAt,
		MetadataReadAt:    bs.MetadataReadAt,
		LastError:         bs.LastError,
		MissedScans:       bs.MissedScans,
		Connected:         bs.isConnected && bs.device != nil,
		PresenceUncertain: bs.presenceUncertain,
	}
}

func (bs *BaseStation) SetPresent(present bool) {
	bs.mutex.Lock()
	bs.Present = present
	bs.mutex.Unlock()
}

func (bs *BaseStation) MarkSeen(now time.Time) {
	bs.mutex.Lock()
	bs.Present = true
	bs.MissedScans = 0
	bs.LastSeenAt = now
	bs.presenceUncertain = false
	bs.mutex.Unlock()
}

// MarkMissed requires two consecutive successful scans to mark a station
// absent. A single Windows BLE scan can miss a station while its GATT session
// is still being released.
func (bs *BaseStation) MarkMissed() {
	bs.mutex.Lock()
	bs.presenceUncertain = false
	bs.MissedScans++
	if bs.MissedScans >= 2 {
		bs.Present = false
	}
	bs.mutex.Unlock()
}

// MarkPresenceUncertain records that a completed scan could not reliably
// determine whether this station advertised. It does not count as a miss and
// therefore cannot move a known station closer to the absence threshold.
func (bs *BaseStation) MarkPresenceUncertain() {
	bs.mutex.Lock()
	bs.presenceUncertain = true
	bs.mutex.Unlock()
}

func (bs *BaseStation) UpdateName(name string) {
	bs.mutex.Lock()
	bs.Name = name
	bs.mutex.Unlock()
}

// Initialize sets up the Bluetooth adapter and parses UUIDs.
func Initialize() error {
	if err := adapter.Enable(); err != nil {
		return fmt.Errorf("could not enable Bluetooth adapter: %w", err)
	}
	adapter.SetConnectHandler(handleAdapterConnectionChange)

	uuidInitOnce.Do(func() {
		parsedUUIDs := []struct {
			name   string
			value  string
			target *bluetooth.UUID
		}{
			{"power control service", powerControlServiceUUIDString, &powerControlServiceUUID},
			{"mode characteristic", modeCharacteristicUUIDString, &modeCharacteristicUUID},
			{"power control characteristic", powerControlCharacteristicUUIDString, &powerControlCharacteristicUUID},
			{"identify characteristic", identifyCharacteristicUUIDString, &identifyCharacteristicUUID},
			{"device information service", deviceInformationServiceUUIDString, &deviceInformationServiceUUID},
			{"manufacturer characteristic", manufacturerCharacteristicUUIDString, &manufacturerCharacteristicUUID},
			{"model characteristic", modelCharacteristicUUIDString, &modelCharacteristicUUID},
			{"serial characteristic", serialCharacteristicUUIDString, &serialCharacteristicUUID},
			{"hardware characteristic", hardwareCharacteristicUUIDString, &hardwareCharacteristicUUID},
			{"firmware characteristic", firmwareCharacteristicUUIDString, &firmwareCharacteristicUUID},
		}
		for _, item := range parsedUUIDs {
			*item.target, uuidInitErr = bluetooth.ParseUUID(item.value)
			if uuidInitErr != nil {
				uuidInitErr = fmt.Errorf("could not parse %s UUID: %w", item.name, uuidInitErr)
				return
			}
		}
	})
	if uuidInitErr != nil {
		return uuidInitErr
	}
	return nil
}

func handleAdapterConnectionChange(device bluetooth.Device, connected bool) {
	if connected {
		return
	}
	connectedStationsMutex.Lock()
	stations := append([]*BaseStation(nil), connectedStations...)
	connectedStationsMutex.Unlock()
	for _, station := range stations {
		if station.Address != device.Address {
			continue
		}
		go invalidateDisconnectedDevice(station, device)
	}
}

func invalidateDisconnectedDevice(station *BaseStation, disconnected bluetooth.Device) {
	station.mutex.Lock()
	current, ok := station.device.(bluetooth.Device)
	if !ok || current != disconnected {
		station.mutex.Unlock()
		return
	}
	station.isConnected = false
	station.pendingCleanup = station.device
	station.device = nil
	station.characteristic = nil
	station.modeCharacteristic = nil
	station.identifyCharacteristic = nil
	station.LastPowerReadAt = time.Time{}
	station.LastChannelReadAt = time.Time{}
	station.setConnectionErrorInternal(errors.New("Bluetooth device disconnected"))
	station.mutex.Unlock()

	connectedStationsMutex.Lock()
	remaining := connectedStations[:0]
	for _, tracked := range connectedStations {
		if tracked != station {
			remaining = append(remaining, tracked)
		}
	}
	connectedStations = remaining
	found := false
	for _, tracked := range pendingCleanupStations {
		if tracked == station {
			found = true
			break
		}
	}
	if !found {
		pendingCleanupStations = append(pendingCleanupStations, station)
	}
	connectedStationsMutex.Unlock()
}

func IsAdapterUnavailable(err error) bool {
	return errors.Is(err, bluetooth.ErrRadioNotAvailable) ||
		errors.Is(err, bluetooth.ErrDisabledByPolicy)
}

// DeviceTransportError identifies an operation that could not reliably use
// the current connection or cached GATT database.
type DeviceTransportError struct {
	Operation string
	Err       error
}

func (e *DeviceTransportError) Error() string {
	return fmt.Sprintf("%s: %v", e.Operation, e.Err)
}

func (e *DeviceTransportError) Unwrap() error {
	return e.Err
}

func transportError(operation string, err error) error {
	if err == nil {
		return nil
	}
	return &DeviceTransportError{Operation: operation, Err: err}
}

// UnsupportedCapabilityError reports that the device explicitly rejected an
// operation as unsupported. It is intentionally distinct from a transport
// failure so callers do not discard an otherwise healthy GATT connection.
type UnsupportedCapabilityError struct {
	Capability string
	Err        error
}

func (e *UnsupportedCapabilityError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("%s is not supported", e.Capability)
	}
	return fmt.Sprintf("%s is not supported: %v", e.Capability, e.Err)
}

func (e *UnsupportedCapabilityError) Unwrap() error {
	return e.Err
}

func unsupportedCapability(capability string, err error) error {
	return &UnsupportedCapabilityError{Capability: capability, Err: err}
}

// IsUnsupportedCapabilityError reports both a classified application
// capability error and a raw ATT response that explicitly rejects an
// operation.
func IsUnsupportedCapabilityError(err error) bool {
	var capabilityErr *UnsupportedCapabilityError
	return errors.As(err, &capabilityErr) || IsCapabilityUnsupported(err)
}

// IsCapabilityUnsupported reports ATT responses that describe an unsupported
// operation rather than a broken connection.
func IsCapabilityUnsupported(err error) bool {
	var protocolErr bluetooth.AttributeProtocolError
	if !errors.As(err, &protocolErr) {
		return false
	}
	switch protocolErr {
	case bluetooth.ErrAttReadNotPermitted,
		bluetooth.ErrAttWriteNotPermitted,
		bluetooth.ErrAttRequestNotSupported:
		return true
	default:
		return false
	}
}

// RequiresReconnect reports failures for which cached WinRT service and
// characteristic handles must not be reused by the next device operation.
func RequiresReconnect(err error) bool {
	if err == nil {
		return false
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, nested := range joined.Unwrap() {
			if RequiresReconnect(nested) {
				return true
			}
		}
		return false
	}
	if protocolErr, ok := err.(bluetooth.AttributeProtocolError); ok {
		return !IsCapabilityUnsupported(protocolErr)
	}
	if capabilityErr, ok := err.(*UnsupportedCapabilityError); ok {
		return RequiresReconnect(capabilityErr.Err)
	}
	if transportErr, ok := err.(*DeviceTransportError); ok {
		if RequiresReconnect(transportErr.Err) {
			return true
		}
		if IsUnsupportedCapabilityError(transportErr.Err) {
			return false
		}
		return true
	}
	if errors.Is(err, bluetooth.ErrGATTUnreachable) ||
		errors.Is(err, bluetooth.ErrGATTProtocol) ||
		errors.Is(err, bluetooth.ErrGATTAccessDenied) ||
		errors.Is(err, bluetooth.ErrGATTCommunication) {
		return true
	}
	return RequiresReconnect(errors.Unwrap(err))
}

func IsGATTCommunicationFailure(err error) bool {
	return RequiresReconnect(err)
}

// ScanForDuration performs a blocking BLE scan for the specified duration
// and returns a list of discovered base stations.
// Uses time.AfterFunc to stop the scan.
func ScanForDuration(duration time.Duration) ([]DiscoveredStation, error) {
	return ScanForDurationContext(context.Background(), duration)
}

// ScanForDurationContext performs a blocking BLE scan using the same guarded
// platform scan session as ScanForDuration and stops it when ctx is cancelled.
func ScanForDurationContext(ctx context.Context, duration time.Duration) ([]DiscoveredStation, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		return nil, ErrScanCancelled
	}
	// log.Printf("[BT] ScanForDuration: Starting scan for %v...", duration)
	localStations := make(map[string]DiscoveredStation)
	var localMutex sync.Mutex
	session := newScanSession()
	activeScanMutex.Lock()
	if activeScan != nil {
		activeScanMutex.Unlock()
		return nil, errors.New("Bluetooth scan is already active")
	}
	activeScan = session
	activeScanMutex.Unlock()
	contextWatcherDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			session.requestStopAsync(scanStopCancelled)
		case <-contextWatcherDone:
		}
	}()
	defer func() {
		close(contextWatcherDone)
		activeScanMutex.Lock()
		if activeScan == session {
			activeScan = nil
		}
		activeScanMutex.Unlock()
	}()

	scanCallback := func(adapter *bluetooth.Adapter, result bluetooth.ScanResult) {
		localName := result.LocalName()
		isNamedLighthouse := strings.HasPrefix(localName, "LHB-")
		hasControlService := result.AdvertisementPayload.HasServiceUUID(powerControlServiceUUID)
		if !isNamedLighthouse && !hasControlService {
			return
		}
		addressString := result.Address.String()
		if addressString == "" || addressString == "00:00:00:00:00:00" {
			return
		}
		localMutex.Lock()
		previous, found := localStations[addressString]
		if !found {
			// log.Printf("[BT] Scan: Discovered %s (%s)", result.LocalName(), result.Address.String())
		}
		if localName == "" && found {
			localName = previous.Name
		}
		localStations[addressString] = DiscoveredStation{
			Name:    localName,
			Address: result.Address,
		}
		localMutex.Unlock()
	}

	var stopTimer *time.Timer
	scanStarted := func() {
		session.markStarted()
		stopTimer = time.AfterFunc(duration, func() {
			log.Printf("[BT] ScanForDuration: Duration %v elapsed. Calling StopScan...", duration)
			err := session.requestStop(scanStopDuration)
			if err != nil {
				log.Printf("[BT] ScanForDuration: adapter.StopScan() error: %v", err)
			}
		})
	}

	// Start the blocking scan directly
	log.Println("[BT] ScanForDuration: Calling adapter.Scan()...")
	scanErr := scanSafely(scanCallback, scanStarted)
	session.markFinished()
	session.waitForIssuedStop()
	timerStopped := stopTimer == nil || stopTimer.Stop()
	if stopTimer != nil && !timerStopped {
		// The timer callback has started. Wait for it so a late StopScan cannot
		// accidentally stop a subsequent scan.
		<-session.stopDone
	}

	if scanErr != nil {
		log.Printf("[BT] ScanForDuration (AfterFunc): adapter.Scan() finished with error: %v", scanErr)
	} else {
		log.Println("[BT] ScanForDuration (AfterFunc): adapter.Scan() finished gracefully (likely due to StopScan timer).)")
	}

	// Collect results
	localMutex.Lock()
	results := make([]DiscoveredStation, 0, len(localStations))
	for _, station := range localStations {
		results = append(results, station)
	}
	localMutex.Unlock()

	log.Printf("[BT] ScanForDuration (AfterFunc): Finished. Found %d stations.", len(results))

	reason := session.stopReason()
	if scanErr != nil {
		if err := scanCompletionError(scanErr); err != nil {
			return nil, err
		}
	}
	// A watcher that failed to stop or timed out after a cancellation
	// request must be reported as a failure so callers (HTTP, Wails,
	// status) agree the scan did not complete cleanly.
	if reason == scanStopCancelled || ctx.Err() != nil {
		if session.stopErr != nil {
			return nil, fmt.Errorf("failed to stop Bluetooth scan after cancellation: %w", session.stopErr)
		}
		return nil, ErrScanCancelled
	}
	if reason != scanStopDuration {
		return nil, errors.New("scan stopped before the requested duration completed")
	}
	if session.stopErr != nil {
		return nil, fmt.Errorf("failed to stop Bluetooth scan safely: %w", session.stopErr)
	}
	if err := scanCompletionError(scanErr); err != nil {
		return nil, err
	}
	return results, nil
}

func scanSafely(callback func(*bluetooth.Adapter, bluetooth.ScanResult), started func()) (returnErr error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			returnErr = fmt.Errorf("Bluetooth scan panicked: %v\n%s", recovered, debug.Stack())
		}
	}()
	if startAware, ok := adapter.(interface {
		ScanWithStart(func(*bluetooth.Adapter, bluetooth.ScanResult), func()) error
	}); ok {
		return startAware.ScanWithStart(callback, started)
	}
	started()
	return adapter.Scan(callback)
}

func stopScanSafely() (returnErr error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			returnErr = fmt.Errorf("Bluetooth StopScan panicked: %v\n%s", recovered, debug.Stack())
		}
	}()
	return adapter.StopScan()
}

// CancelScan requests cancellation of an active platform scan. It is used
// during application shutdown and retains the same panic boundary as the
// duration timer.
func CancelScan() error {
	activeScanMutex.Lock()
	session := activeScan
	activeScanMutex.Unlock()
	if session == nil {
		return nil
	}
	return session.requestStop(scanStopCancelled)
}

// RequestScanCancellation records shutdown cancellation and starts StopScan
// when possible without waiting for the WinRT watcher to start or stop.
func RequestScanCancellation() {
	activeScanMutex.Lock()
	session := activeScan
	activeScanMutex.Unlock()
	if session != nil {
		session.requestStopAsync(scanStopCancelled)
	}
}

func scanCompletionError(scanErr error) error {
	if scanErr != nil {
		if IsAdapterUnavailable(scanErr) {
			return fmt.Errorf("Bluetooth is unavailable; turn on Bluetooth or check the adapter, then retry: %w", scanErr)
		}
		return fmt.Errorf("scan failed before the requested duration completed: %w", scanErr)
	}
	return nil
}

// readPowerStateInternal performs the actual read and update.
// Assumes caller holds the write lock (station.mutex.Lock()).
func readPowerStateInternal(station *BaseStation) error {
	return readPowerStateInternalContext(context.Background(), station)
}

func readPowerStateInternalContext(ctx context.Context, station *BaseStation) error {
	if station.characteristic == nil {
		return transportError("read power characteristic", fmt.Errorf("power characteristic is nil for %s", station.Name))
	}

	log.Printf("Bluetooth: Reading power state for %s (%s)", station.Name, station.Address)
	buf := make([]byte, 1)
	n, err := readCharacteristicContext(ctx, station.characteristic, buf)
	if err != nil {
		station.LastPowerReadAt = time.Time{}
		if IsCapabilityUnsupported(err) {
			station.Capabilities.PowerRead = false
			station.setPowerStateInternal(PowerStateUnknown, RawPowerStateUnknown)
			return unsupportedCapability("power read", err)
		}
		return transportError("read power characteristic", fmt.Errorf("%s: %w", station.Name, err))
	}
	if n != 1 {
		station.LastPowerReadAt = time.Time{}
		return transportError("read power characteristic", fmt.Errorf("unexpected bytes read (%d) for %s", n, station.Name))
	}

	rawState := int(buf[0])
	newState := decodePowerStateWithHistory(buf[0], station.PowerState, station.bootingSince, time.Now())

	if station.PowerState != newState { // Check before logging
		log.Printf("Bluetooth: Power state for %s changed from %d to %d", station.Name, station.PowerState, newState)
	}
	station.setPowerStateInternal(newState, rawState)
	station.LastPowerReadAt = time.Now()
	station.updateLastReadInternal(station.LastPowerReadAt)

	return nil
}

const bootingFallbackAfter = 8 * time.Second

// Some Lighthouse 2.0 firmware reports 0x01 both while starting and
// occasionally while already awake. Keep the transitional interpretation
// initially, but do not let a station remain permanently stuck in Booting.
func decodePowerStateWithHistory(raw byte, previous PowerState, bootingSince, now time.Time) PowerState {
	state := DecodePowerState(raw)
	if raw != 0x01 {
		return state
	}
	if previous == PowerStateOn {
		return PowerStateOn
	}
	if previous == PowerStateBooting && !bootingSince.IsZero() && now.Sub(bootingSince) >= bootingFallbackAfter {
		return PowerStateOn
	}
	return state
}

// readChannelInternal reads the Lighthouse 2.0 optical channel.
// Assumes caller holds the write lock (station.mutex.Lock()).
func readChannelInternal(station *BaseStation) error {
	return readChannelInternalContext(context.Background(), station)
}

func readChannelInternalContext(ctx context.Context, station *BaseStation) error {
	if station.modeCharacteristic == nil {
		station.LastChannelReadAt = time.Time{}
		return transportError("read channel characteristic", fmt.Errorf("mode characteristic is nil for %s", station.Name))
	}

	// Read one extra byte so an overlong value is rejected instead of being
	// silently truncated to a seemingly valid four-byte integer.
	buf := make([]byte, 5)
	n, err := readCharacteristicContext(ctx, station.modeCharacteristic, buf)
	if err != nil {
		station.LastChannelReadAt = time.Time{}
		if IsCapabilityUnsupported(err) {
			station.Capabilities.ChannelRead = false
			station.Channel = ChannelUnknown
			return unsupportedCapability("channel read", err)
		}
		return transportError("read channel characteristic", fmt.Errorf("%s: %w", station.Name, err))
	}
	if n < 1 || n > 4 {
		station.LastChannelReadAt = time.Time{}
		return transportError("read channel characteristic", fmt.Errorf("unexpected bytes read (%d) for %s", n, station.Name))
	}

	channel, err := DecodeChannel(buf[:n])
	if err != nil {
		station.LastChannelReadAt = time.Time{}
		return fmt.Errorf("invalid channel for %s: %w", station.Name, err)
	}
	station.Channel = channel
	station.LastChannelReadAt = time.Now()
	station.updateLastReadInternal(station.LastChannelReadAt)
	return nil
}

// ReadPowerState attempts to read the current power state for an already connected station.
func ReadPowerState(station *BaseStation) error {
	return ReadPowerStateContext(context.Background(), station)
}

// ReadPowerStateContext reads the current state of an already connected
// station and allows read-only GATT work to be cancelled.
func ReadPowerStateContext(ctx context.Context, station *BaseStation) error {
	if station == nil {
		return fmt.Errorf("station is nil")
	}
	ctx = normalizeContext(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}

	station.mutex.Lock() // Lock for the duration
	defer station.mutex.Unlock()

	if !station.isConnected || station.device == nil {
		return fmt.Errorf("station %s is not connected", station.Name)
	}
	var powerReadErr error
	var channelReadErr error
	if station.Capabilities.PowerRead {
		if station.characteristic == nil {
			powerReadErr = fmt.Errorf("power characteristic is unavailable for %s", station.Name)
			station.LastPowerReadAt = time.Time{}
		} else {
			powerReadErr = readPowerStateInternalContext(ctx, station)
		}
	} else {
		station.setPowerStateInternal(PowerStateUnknown, RawPowerStateUnknown)
	}
	if err := ctx.Err(); err != nil {
		if powerReadErr != nil {
			return &StatusReadError{Power: errors.Join(powerReadErr, err)}
		}
		return &StatusReadError{Channel: err}
	}
	if station.Capabilities.ChannelRead {
		channelReadErr = readChannelInternalContext(ctx, station)
	} else {
		station.Channel = ChannelUnknown
		station.LastChannelReadAt = time.Time{}
	}
	if err := ctx.Err(); err != nil {
		if channelReadErr == nil {
			channelReadErr = err
		} else {
			channelReadErr = errors.Join(channelReadErr, err)
		}
	}
	if powerReadErr != nil || channelReadErr != nil {
		readErr := &StatusReadError{Power: powerReadErr, Channel: channelReadErr}
		station.setPowerErrorInternal(powerReadErr)
		station.setChannelErrorInternal(channelReadErr)
		return readErr
	}
	station.setConnectionErrorInternal(nil)
	station.setPowerErrorInternal(nil)
	station.setChannelErrorInternal(nil)
	return nil
}

// InvalidateAllConnections synchronously invalidates every cached connection.
// Failed WinRT cleanup remains owned by its BaseStation so a later operation
// or shutdown can retry it before creating a replacement GATT session.
func InvalidateAllConnections() error {
	connectedStationsMutex.Lock()
	stations := append([]*BaseStation(nil), connectedStations...)
	for _, pending := range pendingCleanupStations {
		found := false
		for _, station := range stations {
			if station == pending {
				found = true
				break
			}
		}
		if !found {
			stations = append(stations, pending)
		}
	}
	connectedStationsMutex.Unlock()
	var cleanupErrors []error
	for _, station := range stations {
		if err := DisconnectStation(station); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("%s: %w", station.Snapshot().Address, err))
		}
	}
	return errors.Join(cleanupErrors...)
}

func hasRead(properties uint32) bool {
	return properties&characteristicPropertyRead != 0
}

func hasWrite(properties uint32) bool {
	return properties&(characteristicPropertyWrite|characteristicPropertyWriteWithoutResponse) != 0
}

func hasNotify(properties uint32) bool {
	return properties&(characteristicPropertyNotify|characteristicPropertyIndicate) != 0
}

func readMetadataValue(characteristic characteristicIO) (string, error) {
	return readMetadataValueContext(context.Background(), characteristic)
}

func readMetadataValueContext(ctx context.Context, characteristic characteristicIO) (string, error) {
	buf := make([]byte, 256)
	n, err := readCharacteristicContext(ctx, characteristic, buf)
	if err != nil {
		return "", err
	}
	if n < 0 || n > len(buf) {
		return "", fmt.Errorf("metadata value is too large: %d bytes", n)
	}
	return strings.Trim(string(buf[:n]), "\x00\r\n\t "), nil
}

func mergeMetadata(previous, discovered DeviceMetadata) DeviceMetadata {
	if discovered.Manufacturer == "" {
		discovered.Manufacturer = previous.Manufacturer
	}
	if discovered.Model == "" {
		discovered.Model = previous.Model
	}
	if discovered.SerialNumber == "" {
		discovered.SerialNumber = previous.SerialNumber
	}
	if discovered.HardwareRevision == "" {
		discovered.HardwareRevision = previous.HardwareRevision
	}
	if discovered.FirmwareRevision == "" {
		discovered.FirmwareRevision = previous.FirmwareRevision
	}
	return discovered
}

func reconcileMetadata(
	previous DeviceMetadata,
	discovered DeviceMetadata,
	serviceFound bool,
	recognized int,
	successful int,
	hadFailure bool,
	now time.Time,
) (DeviceMetadata, time.Time) {
	if serviceFound && recognized > 0 && successful == recognized && !hadFailure {
		// A complete discovery is authoritative. Replacing the snapshot also
		// removes fields that are no longer advertised by the current firmware.
		return discovered, now
	}
	// Retain useful cached values after an optional partial failure, but do not
	// present the mixed-age snapshot as freshly read.
	return mergeMetadata(previous, discovered), time.Time{}
}

// connectAndDiscoverInternal handles connection and discovery.
// Assumes caller holds the write lock (station.mutex.Lock()).
func connectAndDiscoverInternal(station *BaseStation) error {
	return connectAndDiscoverInternalContext(context.Background(), station)
}

func connectAndDiscoverInternalContext(ctx context.Context, station *BaseStation) error {
	ctx = normalizeContext(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := cleanupPendingInternal(station); err != nil {
		return transportError("finish previous connection cleanup", err)
	}
	if station.isConnected && station.device != nil && station.characteristic != nil {
		connected, err := station.device.Connected()
		if err == nil && connected {
			station.setConnectionErrorInternal(nil)
			return nil // Already good
		}
		if err := disconnectInternal(station); err != nil {
			return transportError("disconnect stale station connection", err)
		}
	}

	if !station.isConnected || station.device == nil {
		log.Printf("Bluetooth: Internal connect attempt for %s...", station.Name)
		device, err := connectContext(ctx, station.Address)
		if err != nil {
			station.isConnected = false
			station.device = nil
			station.characteristic = nil
			station.modeCharacteristic = nil
			station.identifyCharacteristic = nil
			station.LastPowerReadAt = time.Time{}
			station.LastChannelReadAt = time.Time{}
			if ctx.Err() == nil {
				station.setConnectionErrorInternal(err)
			}
			return transportError("connect station", err)
		}
		station.device = device
		station.isConnected = true
		log.Printf("Bluetooth: Internal connect successful for %s.", station.Name)
		connectedStationsMutex.Lock()
		found := false
		for _, cs := range connectedStations {
			if cs.Address == station.Address {
				found = true
				break
			}
		}
		if !found {
			connectedStations = append(connectedStations, station)
		}
		connectedStationsMutex.Unlock()
	}

	if station.characteristic == nil {
		log.Printf("Bluetooth: Internal discovery attempt for %s...", station.Name)

		var err error

		const maxRetries = 3
		for i := 0; i < maxRetries; i++ {
			if contextErr := ctx.Err(); contextErr != nil {
				return contextErr
			}
			if i > 0 {
				log.Printf("Bluetooth: Retrying discovery for %s (attempt %d/%d)...", station.Name, i+1, maxRetries)
				if cleanupErr := disconnectInternal(station); cleanupErr != nil {
					return transportError("cleanup before discovery retry", cleanupErr)
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(500 * time.Millisecond):
				}
				device, connectErr := connectContext(ctx, station.Address)
				if connectErr != nil {
					if errors.Is(connectErr, context.Canceled) || errors.Is(connectErr, context.DeadlineExceeded) {
						return connectErr
					}
					err = transportError("retry station connection", connectErr)
					continue
				}
				station.device = device
				station.isConnected = true
				connectedStationsMutex.Lock()
				found := false
				for _, connected := range connectedStations {
					if connected == station {
						found = true
						break
					}
				}
				if !found {
					connectedStations = append(connectedStations, station)
				}
				connectedStationsMutex.Unlock()
			}

			services, discoverErr := discoverServicesContext(ctx, station.device)
			if errors.Is(discoverErr, context.Canceled) || errors.Is(discoverErr, context.DeadlineExceeded) {
				return discoverErr
			}
			err = transportError("discover GATT services", discoverErr)
			if err != nil {
				continue
			}

			var powerCharacteristic characteristicIO
			var modeCharacteristic characteristicIO
			var identifyCharacteristic characteristicIO
			capabilities := Capabilities{}
			metadata := DeviceMetadata{}
			metadataServiceFound := false
			metadataRecognized := 0
			metadataSuccessful := 0
			metadataFailure := false
			metadataErrors := make([]error, 0)
			controlServiceFound := false

			for serviceIndex := range services {
				service := services[serviceIndex]
				serviceUUID := service.UUID()
				if serviceUUID != powerControlServiceUUID && serviceUUID != deviceInformationServiceUUID {
					continue
				}
				if serviceUUID == deviceInformationServiceUUID {
					metadataServiceFound = true
				}
				chars, characteristicErr := discoverCharacteristicsContext(ctx, service)
				if characteristicErr != nil {
					if errors.Is(characteristicErr, context.Canceled) || errors.Is(characteristicErr, context.DeadlineExceeded) {
						return characteristicErr
					}
					if serviceUUID == powerControlServiceUUID {
						err = transportError("discover control characteristics", characteristicErr)
						break
					}
					log.Printf("Bluetooth: Optional device information discovery failed for %s: %v", station.Name, characteristicErr)
					metadataFailure = true
					metadataErrors = append(metadataErrors, fmt.Errorf("discover device information characteristics: %w", characteristicErr))
					continue
				}

				if serviceUUID == powerControlServiceUUID {
					controlServiceFound = true
					for characteristicIndex := range chars {
						current := &chars[characteristicIndex]
						properties := current.Properties()
						switch current.UUID() {
						case powerControlCharacteristicUUID:
							powerCharacteristic = current
							capabilities.PowerRead = hasRead(properties)
							capabilities.PowerWrite = hasWrite(properties)
							capabilities.PowerNotify = hasNotify(properties)
							capabilities.Standby = capabilities.PowerWrite
						case modeCharacteristicUUID:
							modeCharacteristic = current
							capabilities.ChannelRead = hasRead(properties)
							capabilities.ChannelWrite = hasWrite(properties)
							capabilities.ChannelNotify = hasNotify(properties)
						case identifyCharacteristicUUID:
							identifyCharacteristic = current
							capabilities.Identify = hasWrite(properties)
						}
					}
					continue
				}

				capabilities.DeviceInformation = true
				for characteristicIndex := range chars {
					current := &chars[characteristicIndex]
					switch current.UUID() {
					case manufacturerCharacteristicUUID, modelCharacteristicUUID,
						serialCharacteristicUUID, hardwareCharacteristicUUID,
						firmwareCharacteristicUUID:
					default:
						continue
					}
					metadataRecognized++
					value, readErr := readMetadataValueContext(ctx, current)
					if readErr != nil {
						if errors.Is(readErr, context.Canceled) || errors.Is(readErr, context.DeadlineExceeded) {
							return readErr
						}
						log.Printf("Bluetooth: Optional metadata read failed for %s (%s): %v", station.Name, current.UUID(), readErr)
						metadataFailure = true
						metadataErrors = append(metadataErrors, fmt.Errorf("read %s: %w", current.UUID(), readErr))
						continue
					}
					metadataSuccessful++
					switch current.UUID() {
					case manufacturerCharacteristicUUID:
						metadata.Manufacturer = value
					case modelCharacteristicUUID:
						metadata.Model = value
					case serialCharacteristicUUID:
						metadata.SerialNumber = value
					case hardwareCharacteristicUUID:
						metadata.HardwareRevision = value
					case firmwareCharacteristicUUID:
						metadata.FirmwareRevision = value
					}
				}
			}

			if err != nil {
				continue
			}
			if !controlServiceFound || powerCharacteristic == nil {
				err = unsupportedCapability("Lighthouse power control service", nil)
				continue
			}
			station.characteristic = powerCharacteristic
			station.modeCharacteristic = modeCharacteristic
			station.identifyCharacteristic = identifyCharacteristic
			station.Capabilities = capabilities
			station.CapabilitiesKnown = true
			if !capabilities.ChannelRead {
				// Do not retain a channel obtained from an older discovery when
				// the current firmware/session no longer exposes a readable Mode
				// characteristic. A stale value must not participate in safety
				// checks.
				station.Channel = ChannelUnknown
				station.LastChannelReadAt = time.Time{}
			}
			station.Metadata, station.MetadataReadAt = reconcileMetadata(
				station.Metadata,
				metadata,
				metadataServiceFound,
				metadataRecognized,
				metadataSuccessful,
				metadataFailure,
				time.Now(),
			)
			if metadataFailure {
				station.setMetadataErrorInternal(errors.Join(metadataErrors...))
			} else {
				station.setMetadataErrorInternal(nil)
			}
			station.setConnectionErrorInternal(nil)
			err = nil
			break
		}

		if err != nil {
			permanentlyUnsupported := IsUnsupportedCapabilityError(err)
			station.CapabilitiesKnown = permanentlyUnsupported
			if permanentlyUnsupported {
				station.Capabilities = Capabilities{}
			}
			cleanupErr := disconnectInternal(station)
			station.setConnectionErrorInternal(err)
			if permanentlyUnsupported {
				if cleanupErr != nil {
					return errors.Join(err, transportError("cleanup unsupported station connection", cleanupErr))
				}
				return err
			}
			if cleanupErr != nil {
				err = errors.Join(err, fmt.Errorf("cleanup failed: %w", cleanupErr))
			}
			return transportError(
				"discover station capabilities",
				fmt.Errorf("%s after %d retries: %w", station.Name, maxRetries, err),
			)
		}

		log.Printf("Bluetooth: Internal discovery successful for %s.", station.Name)
	}
	return nil
}

// InitialReadError reports non-fatal failures after connection and characteristic
// discovery succeeded. Callers can keep the station visible while showing the
// affected values as unknown.
type InitialReadError struct {
	Power    error
	Channel  error
	Metadata error
}

// StatusReadError identifies which independently readable station values
// failed. Callers can preserve a healthy power connection when only the
// optional channel read failed.
type StatusReadError struct {
	Power   error
	Channel error
}

func (e *StatusReadError) Error() string {
	return fmt.Sprintf("station status read was incomplete: %v", errors.Join(e.Power, e.Channel))
}

func (e *StatusReadError) Unwrap() error {
	return errors.Join(e.Power, e.Channel)
}

func (e *InitialReadError) Error() string {
	return fmt.Sprintf("initial station read was incomplete: %v", errors.Join(e.Power, e.Channel, e.Metadata))
}

func (e *InitialReadError) Unwrap() error {
	return errors.Join(e.Power, e.Channel, e.Metadata)
}

// EnsureCapabilities connects and discovers characteristics before a caller
// decides whether an operation is supported. This avoids rejecting a station
// forever because an earlier transient discovery left cached capabilities empty.
func EnsureCapabilities(station *BaseStation) (Capabilities, error) {
	return EnsureCapabilitiesContext(context.Background(), station)
}

// EnsureCapabilitiesContext is the cancellable form of EnsureCapabilities.
// It is safe to cancel while the operation is still connecting or discovering.
func EnsureCapabilitiesContext(ctx context.Context, station *BaseStation) (Capabilities, error) {
	if station == nil {
		return Capabilities{}, fmt.Errorf("station is nil")
	}
	ctx = normalizeContext(ctx)
	station.mutex.Lock()
	defer station.mutex.Unlock()
	if err := connectAndDiscoverInternalContext(ctx, station); err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return Capabilities{}, finishCancelledCapabilityDiscovery(station, contextErr)
		}
		return Capabilities{}, err
	}
	return station.Capabilities, nil
}

// RefreshCapabilities forces service and characteristic discovery on the
// current connection. It is used when a required optional characteristic was
// missing from an earlier, possibly incomplete discovery result.
func RefreshCapabilities(station *BaseStation) (Capabilities, error) {
	return RefreshCapabilitiesContext(context.Background(), station)
}

// RefreshCapabilitiesContext is the cancellable form of RefreshCapabilities.
// Cancellation only applies before a caller starts a subsequent write.
func RefreshCapabilitiesContext(ctx context.Context, station *BaseStation) (Capabilities, error) {
	if station == nil {
		return Capabilities{}, fmt.Errorf("station is nil")
	}
	ctx = normalizeContext(ctx)
	station.mutex.Lock()
	defer station.mutex.Unlock()
	if err := disconnectInternal(station); err != nil {
		return Capabilities{}, transportError("cleanup before capability refresh", err)
	}
	station.CapabilitiesKnown = false
	if err := connectAndDiscoverInternalContext(ctx, station); err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return Capabilities{}, finishCancelledCapabilityDiscovery(station, contextErr)
		}
		return Capabilities{}, err
	}
	return station.Capabilities, nil
}

func finishCancelledCapabilityDiscovery(station *BaseStation, contextErr error) error {
	if cleanupErr := disconnectInternal(station); cleanupErr != nil {
		return errors.Join(contextErr, transportError("cleanup cancelled capability discovery", cleanupErr))
	}
	return contextErr
}

// FetchInitialPowerState attempts to connect (if necessary) and read the initial power state.
func FetchInitialPowerState(station *BaseStation) error {
	return FetchInitialPowerStateContext(context.Background(), station)
}

func finishCancelledInitialRead(station *BaseStation, contextErr error) error {
	if cleanupErr := disconnectInternal(station); cleanupErr != nil {
		return errors.Join(contextErr, transportError("cleanup cancelled initial read", cleanupErr))
	}
	return contextErr
}

// FetchInitialPowerStateContext performs the full initial GATT read using one
// context so a stopped scan can cancel connection, discovery, and reads.
func FetchInitialPowerStateContext(ctx context.Context, station *BaseStation) error {
	if station == nil {
		return fmt.Errorf("station is nil")
	}
	ctx = normalizeContext(ctx)

	station.mutex.Lock() // Lock for the whole operation
	defer station.mutex.Unlock()

	err := connectAndDiscoverInternalContext(ctx, station)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return finishCancelledInitialRead(station, contextErr)
		}
		station.setConnectionErrorInternal(err)
		log.Printf("Bluetooth: Failed to connect/discover in FetchInitialPowerState for %s: %v", station.Name, err)
		return err
	}

	var powerReadErr error
	var channelReadErr error
	metadataReadErr := station.metadataReadError
	if station.Capabilities.PowerRead {
		log.Printf("Bluetooth: FetchInitialPowerState proceeding to read state for %s.", station.Name)
		if powerReadErr = readPowerStateInternalContext(ctx, station); powerReadErr != nil {
			log.Printf("Bluetooth: Failed to read state in FetchInitialPowerState for %s: %v", station.Name, powerReadErr)
		}
	} else {
		station.setPowerStateInternal(PowerStateUnknown, RawPowerStateUnknown)
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return finishCancelledInitialRead(station, contextErr)
	}
	if station.Capabilities.ChannelRead {
		channelReadErr = readChannelInternalContext(ctx, station)
		if channelReadErr != nil {
			// Channel support is optional so power control remains usable on
			// firmware that does not expose a readable mode characteristic.
			log.Printf("Bluetooth: Failed to read channel in FetchInitialPowerState for %s: %v", station.Name, channelReadErr)
		}
	} else {
		station.Channel = ChannelUnknown
		station.LastChannelReadAt = time.Time{}
	}

	if contextErr := ctx.Err(); contextErr != nil {
		return finishCancelledInitialRead(station, contextErr)
	}
	if powerReadErr != nil || channelReadErr != nil || metadataReadErr != nil {
		readErr := &InitialReadError{
			Power:    powerReadErr,
			Channel:  channelReadErr,
			Metadata: metadataReadErr,
		}
		station.setPowerErrorInternal(powerReadErr)
		station.setChannelErrorInternal(channelReadErr)
		return readErr
	}
	station.setConnectionErrorInternal(nil)
	station.setPowerErrorInternal(nil)
	station.setChannelErrorInternal(nil)
	log.Printf("Bluetooth: FetchInitialPowerState successful for %s. State: %d, channel: %d", station.Name, station.PowerState, station.Channel)
	return nil
}

// writeCharacteristicValueInternal writes one byte using the modes advertised
// by the characteristic. A transport failure from WriteWithoutResponse must
// not be retried as a response write because the first command may have
// reached the device.
// Assumes caller holds station.mutex.
func writeCharacteristicValueInternal(characteristic characteristicIO, value byte) error {
	if characteristic == nil {
		return transportError("write characteristic", fmt.Errorf("characteristic is unavailable"))
	}
	properties := bluetooth.CharacteristicPermissions(characteristic.Properties())
	var n int
	var err error
	switch {
	case properties.WriteWithoutResponse():
		n, err = characteristic.WriteWithoutResponse([]byte{value})
		if err != nil && properties.Write() && IsCapabilityUnsupported(err) {
			n, err = characteristic.Write([]byte{value})
		} else if err != nil && !isDefiniteWriteRejection(err) {
			possiblySent, classified := possiblySentClassification(err)
			if !classified {
				err = &PossiblySentError{Err: err}
			} else if !possiblySent {
				// A transport-provided definite classification preserves retry safety.
			}
		}
	case properties.Write():
		n, err = characteristic.Write([]byte{value})
	default:
		return unsupportedCapability("characteristic write", nil)
	}
	if err != nil {
		return transportError("write characteristic", err)
	}
	if n != 1 {
		shortWriteErr := fmt.Errorf("wrote %d bytes instead of 1", n)
		if properties.WriteWithoutResponse() {
			return transportError("write characteristic", &PossiblySentError{Err: shortWriteErr})
		}
		return transportError("write characteristic", shortWriteErr)
	}
	return nil
}

func isDefiniteWriteRejection(err error) bool {
	var protocolErr bluetooth.AttributeProtocolError
	return errors.As(err, &protocolErr)
}

func writePowerValueInternal(station *BaseStation, value byte) error {
	if station.characteristic == nil {
		return fmt.Errorf("power characteristic is unavailable")
	}
	return writeCharacteristicValueInternal(station.characteristic, value)
}

// confirmPowerStateInternal polls briefly because Lighthouse state transitions
// are not always visible immediately after a successful GATT write.
// Assumes caller holds station.mutex.
func confirmPowerStateInternal(station *BaseStation, expectedState PowerState) error {
	attempts := 15
	if expectedState == PowerStateOn {
		attempts = 51
	}
	var lastErr error
	consecutiveReadErrors := 0
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			time.Sleep(200 * time.Millisecond)
		}
		if err := readPowerStateInternal(station); err != nil {
			lastErr = err
			if IsUnsupportedCapabilityError(err) {
				return err
			}
			consecutiveReadErrors++
			if consecutiveReadErrors >= 2 && attempt < attempts-1 {
				_ = disconnectInternal(station)
				time.Sleep(250 * time.Millisecond)
				if reconnectErr := connectAndDiscoverInternal(station); reconnectErr != nil {
					lastErr = errors.Join(lastErr, fmt.Errorf("confirmation reconnect failed: %w", reconnectErr))
					break
				}
				consecutiveReadErrors = 0
			}
			continue
		}
		consecutiveReadErrors = 0
		if IsPowerStateConfirmed(expectedState, station.RawPowerState) {
			return nil
		}
		lastErr = fmt.Errorf(
			"reported %s with raw 0x%02X, expected a confirmed %s state",
			station.PowerState,
			byte(station.RawPowerState),
			expectedState,
		)
	}
	return lastErr
}

func IsPowerStateConfirmed(expectedState PowerState, raw int) bool {
	switch expectedState {
	case PowerStateSleep:
		return raw == 0x00
	case PowerStateStandby:
		return raw == 0x02
	case PowerStateOn:
		return raw == 0x09 || raw == 0x0B
	default:
		return false
	}
}

type PowerControlResult struct {
	State     PowerState
	Confirmed bool
}

// PowerConfirmationError means the write completed, but the target stable
// state could not be confirmed by readback.
type PowerConfirmationError struct {
	Target PowerState
	Actual PowerState
	Raw    int
	Err    error
}

func (e *PowerConfirmationError) Error() string {
	raw := "unavailable"
	if e.Raw >= 0 {
		raw = fmt.Sprintf("0x%02X", byte(e.Raw))
	}
	return fmt.Sprintf(
		"%s command sent but state confirmation failed (actual %s, raw %s): %v",
		e.Target,
		e.Actual,
		raw,
		e.Err,
	)
}

func (e *PowerConfirmationError) Unwrap() error {
	return e.Err
}

// SetPowerState writes a stable target state and confirms it when the firmware
// exposes a readable power characteristic.
func SetPowerState(station *BaseStation, target PowerState) (PowerControlResult, error) {
	if station == nil {
		return PowerControlResult{}, fmt.Errorf("station is nil")
	}
	if target != PowerStateOn && target != PowerStateStandby && target != PowerStateSleep {
		return PowerControlResult{}, fmt.Errorf("invalid stable target state %s", target)
	}
	station.mutex.Lock()
	defer station.mutex.Unlock()

	const maxRetries = 2
	var err error
	var ambiguousWrite error
	ambiguousSleepPrepare := false
	command := byte(0x00)
	switch target {
	case PowerStateOn:
		command = 0x01
	case PowerStateStandby:
		command = 0x02
	case PowerStateSleep:
		command = 0x00
	}

	for i := 0; i < maxRetries; i++ {
		sleepFinalAttempted := false
		if err = connectAndDiscoverInternal(station); err != nil {
			log.Printf("Bluetooth: connect/discover failed during power attempt %d/%d for %s: %v", i+1, maxRetries, station.Name, err)
			if i == maxRetries-1 {
				return PowerControlResult{}, fmt.Errorf("failed to connect/discover before power command: %w", err)
			}
			_ = disconnectInternal(station)
			time.Sleep(500 * time.Millisecond)
			continue
		}
		if !station.Capabilities.PowerWrite {
			return PowerControlResult{}, unsupportedCapability("power control", nil)
		}

		log.Printf("Bluetooth: Sending %s command to %s", target, station.Name)
		if target == PowerStateSleep {
			// Some Lighthouse 2.0 firmware expects wake/prepare then sleep.
			err = writePowerValueInternal(station, 0x01)
			if err == nil {
				time.Sleep(50 * time.Millisecond)
				sleepFinalAttempted = true
				err = writePowerValueInternal(station, command)
			}
		} else {
			err = writePowerValueInternal(station, command)
		}
		if err == nil {
			break
		}
		if IsPossiblySent(err) {
			ambiguousWrite = err
			ambiguousSleepPrepare = target == PowerStateSleep && !sleepFinalAttempted
			if ambiguousSleepPrepare {
				// The final sleep command was not attempted, so observing the old
				// sleeping state cannot confirm completion of the sequence.
				_ = readPowerStateInternal(station)
			}
			break
		}
		var protocolErr bluetooth.AttributeProtocolError
		if target == PowerStateStandby &&
			errors.As(err, &protocolErr) &&
			protocolErr == bluetooth.ErrAttValueNotAllowed {
			station.Capabilities.Standby = false
			station.setOperationErrorInternal(err)
			return PowerControlResult{}, unsupportedCapability("standby", err)
		}
		if IsCapabilityUnsupported(err) {
			station.Capabilities.PowerWrite = false
			station.Capabilities.Standby = false
			station.setOperationErrorInternal(err)
			return PowerControlResult{}, unsupportedCapability("power control", err)
		}
		log.Printf("Bluetooth: Write %s failed for %s: %v. Retrying...", target, station.Name, err)
		_ = disconnectInternal(station)
		if i < maxRetries-1 {
			time.Sleep(500 * time.Millisecond)
		}
	}

	if err != nil {
		if ambiguousWrite != nil {
			if station.Capabilities.PowerRead && !ambiguousSleepPrepare {
				if confirmationErr := confirmPowerStateInternal(station, target); confirmationErr == nil {
					station.setPowerErrorInternal(nil)
					station.setOperationErrorInternal(nil)
					return PowerControlResult{State: target, Confirmed: true}, nil
				} else {
					err = errors.Join(ambiguousWrite, confirmationErr)
				}
			} else if !station.Capabilities.PowerRead {
				err = errors.Join(ambiguousWrite, unsupportedCapability("power confirmation read", nil))
			} else {
				err = errors.Join(ambiguousWrite, fmt.Errorf("sleep prepare write was ambiguous before the final sleep command"))
			}
			station.setPowerErrorInternal(err)
			station.setOperationErrorInternal(nil)
			return PowerControlResult{State: station.PowerState, Confirmed: false}, &PowerConfirmationError{
				Target: target,
				Actual: station.PowerState,
				Raw:    station.RawPowerState,
				Err:    fmt.Errorf("possibly-sent command could not be confirmed for %s: %w", station.Name, err),
			}
		}
		station.setOperationErrorInternal(err)
		return PowerControlResult{}, fmt.Errorf("failed to write %s command after %d retries: %w", target, maxRetries, err)
	}

	if !station.Capabilities.PowerRead {
		station.setPowerStateInternal(PowerStateUnknown, RawPowerStateUnknown)
		station.setPowerErrorInternal(nil)
		station.setOperationErrorInternal(nil)
		return PowerControlResult{State: target, Confirmed: false}, nil
	}
	if err = confirmPowerStateInternal(station, target); err != nil {
		station.setPowerErrorInternal(err)
		station.setOperationErrorInternal(nil)
		return PowerControlResult{State: station.PowerState, Confirmed: false}, &PowerConfirmationError{
			Target: target,
			Actual: station.PowerState,
			Raw:    station.RawPowerState,
			Err:    fmt.Errorf("state confirmation failed for %s: %w", station.Name, err),
		}
	}
	station.LastReadAt = time.Now()
	station.setPowerErrorInternal(nil)
	station.setOperationErrorInternal(nil)
	return PowerControlResult{State: target, Confirmed: true}, nil
}

func PowerOn(station *BaseStation) error {
	_, err := SetPowerState(station, PowerStateOn)
	return err
}

func PowerOff(station *BaseStation) error {
	_, err := SetPowerState(station, PowerStateSleep)
	return err
}

func Identify(station *BaseStation) error {
	if station == nil {
		return fmt.Errorf("station is nil")
	}
	station.mutex.Lock()
	defer station.mutex.Unlock()
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if err := connectAndDiscoverInternal(station); err != nil {
			lastErr = err
		} else if !station.Capabilities.Identify || station.identifyCharacteristic == nil {
			return unsupportedCapability("identify", nil)
		} else if err := writeCharacteristicValueInternal(station.identifyCharacteristic, 0x01); err != nil {
			if IsCapabilityUnsupported(err) {
				station.Capabilities.Identify = false
				station.setOperationErrorInternal(err)
				return unsupportedCapability("identify", err)
			}
			lastErr = err
			if IsPossiblySent(err) {
				station.setOperationErrorInternal(err)
				return fmt.Errorf("identify command for %s may have been sent and will not be retried: %w", station.Name, err)
			}
		} else {
			station.setOperationErrorInternal(nil)
			return nil
		}
		if attempt == 0 {
			_ = disconnectInternal(station)
			time.Sleep(250 * time.Millisecond)
		}
	}
	_ = disconnectInternal(station)
	station.setOperationErrorInternal(lastErr)
	return fmt.Errorf("failed to identify %s after retry: %w", station.Name, lastErr)
}

type ChannelWriteResult struct {
	PreviousChannel int
	Channel         int
	WriteWarning    string
	CommandSent     bool
}

func SetChannel(station *BaseStation, channel int) (ChannelWriteResult, error) {
	result := ChannelWriteResult{PreviousChannel: ChannelUnknown, Channel: ChannelUnknown}
	if station == nil {
		return result, fmt.Errorf("station is nil")
	}
	if channel < 1 || channel > 16 {
		return result, fmt.Errorf("channel %d is outside the supported range 1-16", channel)
	}

	station.mutex.Lock()
	defer station.mutex.Unlock()
	if err := connectAndDiscoverInternal(station); err != nil {
		return result, err
	}
	if !station.Capabilities.ChannelWrite || !station.Capabilities.ChannelRead || station.modeCharacteristic == nil {
		return result, unsupportedCapability("safe channel control", nil)
	}
	if err := readChannelInternal(station); err != nil {
		station.setChannelErrorInternal(err)
		station.setOperationErrorInternal(nil)
		if RequiresReconnect(err) {
			_ = disconnectInternal(station)
		}
		return result, fmt.Errorf("failed to read the existing channel for %s: %w", station.Name, err)
	}
	result.PreviousChannel = station.Channel
	if station.Channel == channel {
		result.Channel = station.Channel
		station.LastReadAt = time.Now()
		station.setChannelErrorInternal(nil)
		station.setOperationErrorInternal(nil)
		return result, nil
	}

	if writeErr := writeCharacteristicValueInternal(station.modeCharacteristic, byte(channel)); writeErr != nil {
		if IsCapabilityUnsupported(writeErr) {
			station.Capabilities.ChannelWrite = false
			station.setOperationErrorInternal(writeErr)
			return result, unsupportedCapability("channel write", writeErr)
		}
		// Once the transport reports an ambiguous write, a failed readback
		// cannot turn it back into a definitely-unsent command.
		result.CommandSent = IsPossiblySent(writeErr)
		if readErr := readChannelInternal(station); readErr == nil {
			result.Channel = station.Channel
			if station.Channel == channel {
				result.CommandSent = true
				result.WriteWarning = fmt.Sprintf("the write call reported an error, but channel %d was confirmed by readback: %v", channel, writeErr)
				station.LastReadAt = time.Now()
				station.setChannelErrorInternal(nil)
				station.setOperationErrorInternal(nil)
				return result, nil
			}
			writeErr = fmt.Errorf(
				"write reported %v, but readback reported channel %d instead of %d",
				writeErr,
				station.Channel,
				channel,
			)
		} else {
			writeErr = errors.Join(writeErr, fmt.Errorf("final channel read failed: %w", readErr))
			_ = disconnectInternal(station)
		}
		station.setOperationErrorInternal(writeErr)
		return result, fmt.Errorf("failed to write channel %d for %s: %w", channel, station.Name, writeErr)
	}
	result.CommandSent = true
	var confirmationErr error
	consecutiveReadErrors := 0
	for attempt := 0; attempt < 5; attempt++ {
		time.Sleep(250 * time.Millisecond)
		if err := readChannelInternal(station); err != nil {
			confirmationErr = err
			if IsUnsupportedCapabilityError(err) {
				break
			}
			consecutiveReadErrors++
			if consecutiveReadErrors >= 2 && attempt < 4 {
				_ = disconnectInternal(station)
				time.Sleep(250 * time.Millisecond)
				if reconnectErr := connectAndDiscoverInternal(station); reconnectErr != nil {
					confirmationErr = errors.Join(confirmationErr, fmt.Errorf("channel confirmation reconnect failed: %w", reconnectErr))
					break
				}
				consecutiveReadErrors = 0
			}
			continue
		}
		consecutiveReadErrors = 0
		result.Channel = station.Channel
		if station.Channel == channel {
			station.LastReadAt = time.Now()
			station.setChannelErrorInternal(nil)
			station.setOperationErrorInternal(nil)
			return result, nil
		}
		confirmationErr = fmt.Errorf("reported channel %d, expected %d", station.Channel, channel)
	}
	if err := readChannelInternal(station); err == nil {
		result.Channel = station.Channel
		if station.Channel == channel {
			result.WriteWarning = fmt.Sprintf("channel %d was confirmed by the final readback", channel)
			station.LastReadAt = time.Now()
			station.setChannelErrorInternal(nil)
			station.setOperationErrorInternal(nil)
			return result, nil
		}
		confirmationErr = fmt.Errorf("reported channel %d, expected %d", station.Channel, channel)
	} else {
		confirmationErr = errors.Join(confirmationErr, err)
		if RequiresReconnect(err) {
			_ = disconnectInternal(station)
		}
	}
	if confirmationErr == nil {
		confirmationErr = fmt.Errorf("no channel confirmation was received")
	}
	station.setChannelErrorInternal(confirmationErr)
	station.setOperationErrorInternal(nil)
	return result, fmt.Errorf("channel %d was written but could not be confirmed for %s: %w", channel, station.Name, confirmationErr)
}

// disconnectInternal performs disconnection without locking (must be called within locked context).
// Also removes station from the global tracking list.
func disconnectInternal(s *BaseStation) error {
	if cleanupErr := cleanupPendingInternal(s); cleanupErr != nil {
		return cleanupErr
	}
	var disconnectErr error
	if s.device != nil {
		log.Printf("Bluetooth: Disconnecting internal for %s", s.Name)
		disconnectErr = s.device.Disconnect()
		if bluetooth.IsDisconnectCleanupComplete(disconnectErr) {
			log.Printf("Bluetooth: Disconnect cleanup warning for %s: %v", s.Name, disconnectErr)
			disconnectErr = nil
		} else if disconnectErr != nil {
			s.pendingCleanup = s.device
		}
	}
	s.isConnected = false
	s.device = nil
	s.characteristic = nil
	s.modeCharacteristic = nil
	s.identifyCharacteristic = nil
	s.LastPowerReadAt = time.Time{}
	s.LastChannelReadAt = time.Time{}

	connectedStationsMutex.Lock()
	newConnectedStations := make([]*BaseStation, 0, len(connectedStations))
	for _, cs := range connectedStations {
		if cs.Address != s.Address || s.pendingCleanup != nil {
			newConnectedStations = append(newConnectedStations, cs)
		}
	}
	connectedStations = newConnectedStations
	connectedStationsMutex.Unlock()
	return disconnectErr
}

// cleanupPendingInternal completes a previously failed synchronous Disconnect.
// It must run before a replacement device is connected.
func cleanupPendingInternal(s *BaseStation) error {
	if s.pendingCleanup == nil {
		return nil
	}
	if err := s.pendingCleanup.Disconnect(); err != nil {
		if bluetooth.IsDisconnectCleanupComplete(err) {
			log.Printf("Bluetooth: Pending disconnect cleanup completed for %s with warning: %v", s.Name, err)
			s.pendingCleanup = nil
			return nil
		}
		return err
	}
	s.pendingCleanup = nil
	return nil
}

// DisconnectStation disconnects from a specific base station.
func DisconnectStation(station *BaseStation) error {
	if station == nil {
		return nil
	}
	station.mutex.Lock() // Lock before calling internal disconnect
	defer station.mutex.Unlock()
	err := disconnectInternal(station)
	if err == nil {
		removePendingCleanupStation(station)
	}
	return err
}

func removePendingCleanupStation(station *BaseStation) {
	connectedStationsMutex.Lock()
	remaining := pendingCleanupStations[:0]
	for _, tracked := range pendingCleanupStations {
		if tracked != station {
			remaining = append(remaining, tracked)
		}
	}
	pendingCleanupStations = remaining
	connectedStationsMutex.Unlock()
}

// ReleaseStationForScan closes GATT handles so the Lighthouse can advertise
// again while preserving the last known state for an offline/stale display.
func ReleaseStationForScan(station *BaseStation) error {
	if station == nil {
		return nil
	}
	station.mutex.Lock()
	defer station.mutex.Unlock()

	err := disconnectInternal(station)
	if err == nil {
		removePendingCleanupStation(station)
	}
	return err
}

// DisconnectAllStations disconnects all tracked stations.
func DisconnectAllStations() error {
	connectedStationsMutex.Lock()
	stationsToDisconnect := make([]*BaseStation, 0, len(connectedStations)+len(pendingCleanupStations))
	stationsToDisconnect = append(stationsToDisconnect, connectedStations...)
	for _, pending := range pendingCleanupStations {
		found := false
		for _, tracked := range stationsToDisconnect {
			if tracked == pending {
				found = true
				break
			}
		}
		if !found {
			stationsToDisconnect = append(stationsToDisconnect, pending)
		}
	}
	log.Printf("Bluetooth: Disconnecting all %d tracked stations...", len(stationsToDisconnect))
	connectedStationsMutex.Unlock()

	var disconnectErrors []error
	for _, station := range stationsToDisconnect {
		if err := DisconnectStation(station); err != nil {
			disconnectErrors = append(disconnectErrors, fmt.Errorf("%s: %w", station.Snapshot().Address, err))
		}
	}
	log.Println("Bluetooth: Disconnect all stations attempt finished.")
	return errors.Join(disconnectErrors...)
}
