package bluetooth

import (
	"context"
	"errors"
	"fmt"
	"log"
	"runtime/debug"
	"sync"
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
	uuidInitMutex          sync.Mutex
	uuidInitDone           bool
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

type contextCharacteristicWriter interface {
	WriteContext(context.Context, []byte) (int, error)
	WriteWithoutResponseContext(context.Context, []byte) (int, error)
}

func normalizeContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

// sleepContext waits for delay or returns early when ctx is done. Long poll
// and retry waits use it so application shutdown can interrupt them.
func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
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

// initProtocolUUIDs parses the protocol UUIDs once. A failed parse does not
// consume the initialization: the next Initialize call retries instead of
// caching the error for the lifetime of the process, matching the adapter
// retry behavior around it.
func initProtocolUUIDs() error {
	uuidInitMutex.Lock()
	defer uuidInitMutex.Unlock()
	if uuidInitDone {
		return uuidInitErr
	}
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
		parsed, err := bluetooth.ParseUUID(item.value)
		if err != nil {
			uuidInitErr = fmt.Errorf("could not parse %s UUID: %w", item.name, err)
			return uuidInitErr
		}
		*item.target = parsed
	}
	uuidInitErr = nil
	uuidInitDone = true
	return nil
}

func Initialize() error {
	if err := adapter.Enable(); err != nil {
		return fmt.Errorf("could not enable Bluetooth adapter: %w", err)
	}
	adapter.SetConnectHandler(handleAdapterConnectionChange)

	return initProtocolUUIDs()
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
		station.queueDeviceInvalidation(device)
	}
}

// queueDeviceInvalidation schedules invalidation for a disconnected device.
// Rapid connect/disconnect flapping coalesces into a single worker per
// station instead of accumulating one goroutine per OS notification.
func (bs *BaseStation) queueDeviceInvalidation(device bluetooth.Device) {
	bs.invalidationMutex.Lock()
	for _, pending := range bs.pendingInvalidations {
		if pending == device {
			bs.invalidationMutex.Unlock()
			return
		}
	}
	bs.pendingInvalidations = append(bs.pendingInvalidations, device)
	if bs.invalidationRunning {
		bs.invalidationMutex.Unlock()
		return
	}
	bs.invalidationRunning = true
	bs.invalidationMutex.Unlock()
	go bs.drainDeviceInvalidations()
}

func (bs *BaseStation) drainDeviceInvalidations() {
	for {
		bs.invalidationMutex.Lock()
		if len(bs.pendingInvalidations) == 0 {
			bs.pendingInvalidations = nil
			bs.invalidationRunning = false
			bs.invalidationMutex.Unlock()
			return
		}
		device := bs.pendingInvalidations[0]
		bs.pendingInvalidations[0] = bluetooth.Device{}
		bs.pendingInvalidations = bs.pendingInvalidations[1:]
		bs.invalidationMutex.Unlock()
		bs.invalidateOneSafely(device)
	}
}

// invalidateOneSafely bounds a panic in one device invalidation to that item.
// The drain worker is the only consumer of the invalidation queue and keeps
// the running latch set for its whole lifetime; an unguarded panic would kill
// it with the latch never reset, silently dropping every later OS disconnect
// notification for this station while the queue grows without bound.
func (bs *BaseStation) invalidateOneSafely(device bluetooth.Device) {
	defer func() {
		if recovered := recover(); recovered != nil {
			log.Printf("[BT] device invalidation panicked: %v\n%s", recovered, debug.Stack())
		}
	}()
	invalidateDisconnectedDevice(bs, device)
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
	station.bootRawTrustedOn = false
	// Match disconnectInternal: the boot fallback observation window must not
	// survive an OS disconnect, or the next connection inherits a fast-forward.
	station.bootingSince = time.Time{}
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

	// Kick an eager cleanup of the detached handle. The transport's own
	// connection-status callback normally starts one, but that fallback can
	// silently fail (WinRT thread initialization or COM errors); without this
	// kick the GATT session stays owned by the OS until the next operation on
	// this station or a fleet-wide cleanup, and single-connection peripherals
	// refuse a replacement session while the old one lingers. The cleanup is
	// idempotent: the transport deduplicates attempts, and a concurrent
	// cleanup observes pendingCleanup==nil and returns.
	go func() {
		defer func() { _ = recover() }()
		station.mutex.Lock()
		defer station.mutex.Unlock()
		if station.pendingCleanup != nil {
			_ = cleanupPendingInternal(station)
		}
	}()
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

// DeviceValueError reports a value that the transport delivered intact but
// that violates the protocol's format rules (for example a characteristic
// reporting an unexpected byte length). The link itself worked, so cached
// GATT handles must not be discarded: reconnecting cannot change what the
// device reports, and treating the value as a transport failure would turn
// every read into a disconnect/reconnect cycle.
type DeviceValueError struct {
	Operation string
	Err       error
}

func (e *DeviceValueError) Error() string {
	return fmt.Sprintf("%s: %v", e.Operation, e.Err)
}

func (e *DeviceValueError) Unwrap() error {
	return e.Err
}

func deviceValueError(operation string, err error) error {
	if err == nil {
		return nil
	}
	return &DeviceValueError{Operation: operation, Err: err}
}

// IsDeviceValueError reports failures caused by malformed device data rather
// than a broken link.
func IsDeviceValueError(err error) bool {
	var valueErr *DeviceValueError
	return errors.As(err, &valueErr)
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

// IsProtocolRejection reports ATT responses that reject the request for
// protocol, security-policy, or resource reasons (authentication,
// authorization, encryption, value shape, queue capacity). The peer received
// the request and answered it, so the link is healthy: reconnecting cannot
// change pairing, encryption, or device resource state and would only start a
// disconnect/reconnect cycle that fails the same way on every poll.
func IsProtocolRejection(err error) bool {
	var protocolErr bluetooth.AttributeProtocolError
	if !errors.As(err, &protocolErr) {
		return false
	}
	return isProtocolRejectionCode(protocolErr)
}

func isProtocolRejectionCode(protocolErr bluetooth.AttributeProtocolError) bool {
	switch protocolErr {
	case bluetooth.ErrAttInsufficientAuthentication,
		bluetooth.ErrAttInsufficientAuthorization,
		bluetooth.ErrAttInsufficientEncryption,
		bluetooth.ErrAttInsufficientEncKeySize,
		bluetooth.ErrAttInsufficientResources,
		bluetooth.ErrAttInvalidLength,
		bluetooth.ErrAttInvalidOffset,
		bluetooth.ErrAttInvalidPDU,
		bluetooth.ErrAttPrepareQueueFull,
		bluetooth.ErrAttUnsupportedGroupType:
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
		// A Value Not Allowed response is a protocol decision about the
		// requested value and proves the peer processed the request; the link
		// itself is healthy, so it must not pay a reconnect. It stays outside
		// IsCapabilityUnsupported so power writes keep their dedicated standby
		// downgrade instead of disabling every power write.
		if protocolErr == bluetooth.ErrAttValueNotAllowed {
			return false
		}
		// Security-policy and resource rejections are decisions about the
		// request, not a broken link; the reconnect they used to trigger
		// could never change authentication, encryption, or device resources
		// and only caused repeated session teardown on healthy connections.
		if isProtocolRejectionCode(protocolErr) {
			return false
		}
		return !IsCapabilityUnsupported(protocolErr)
	}
	if _, ok := err.(*UnsupportedCapabilityError); ok {
		// A capability rejection is a protocol decision, never a broken link.
		// Recursing into the wrapped ATT response would classify codes outside
		// the unsupported whitelist (such as standby's Value Not Allowed) as
		// transport failures and make healthy connections pay a reconnect.
		return false
	}
	if IsDeviceValueError(err) {
		// Malformed device data is a property of the device, not the link.
		return false
	}
	if transportErr, ok := err.(*DeviceTransportError); ok {
		if RequiresReconnect(transportErr.Err) {
			return true
		}
		if errors.Is(transportErr.Err, context.Canceled) ||
			errors.Is(transportErr.Err, context.DeadlineExceeded) {
			// The operation's own cancellation/deadline aborted the transport
			// call; the connection itself is not known to be broken, so a
			// healthy link must not pay a reconnect for it.
			return false
		}
		if IsUnsupportedCapabilityError(transportErr.Err) {
			return false
		}
		if IsDeviceValueError(transportErr.Err) {
			return false
		}
		if errors.Is(transportErr.Err, bluetooth.ErrAttValueNotAllowed) {
			// A peer rejection of the requested value is a protocol decision,
			// never a broken link; the bare-code branch above documents why.
			return false
		}
		if IsProtocolRejection(transportErr.Err) {
			// Security-policy and resource rejections wrapped by the transport
			// are protocol decisions too; the bare-code branch documents why a
			// healthy link must not pay a reconnect for them.
			return false
		}
		return true
	}
	if errors.Is(err, bluetooth.ErrGATTUnreachable) ||
		errors.Is(err, bluetooth.ErrGATTProtocol) ||
		errors.Is(err, bluetooth.ErrGATTAccessDenied) ||
		errors.Is(err, bluetooth.ErrGATTCommunication) ||
		errors.Is(err, bluetooth.ErrDeviceDisconnected) {
		return true
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	return RequiresReconnect(errors.Unwrap(err))
}

func IsGATTCommunicationFailure(err error) bool {
	return RequiresReconnect(err)
}

// isContextOnlyError reports whether every leaf in the error tree is a context
// cancellation or deadline. A read whose only failure is an expired budget or
// the caller's own cancellation stays a clean interruption; any genuine
// transport leaf disqualifies it so callers keep the failure's bookkeeping.
func isContextOnlyError(err error) bool {
	if err == nil {
		return false
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, nested := range joined.Unwrap() {
			if nested == nil {
				continue
			}
			if !isContextOnlyError(nested) {
				return false
			}
		}
		return true
	}
	if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if wrapped := errors.Unwrap(err); wrapped != nil {
		return isContextOnlyError(wrapped)
	}
	return true
}

// ScanForDuration performs a blocking BLE scan for the specified duration
// and returns a list of discovered base stations.
// Uses time.AfterFunc to stop the scan.
