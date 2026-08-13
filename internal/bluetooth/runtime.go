package bluetooth

import (
	"context"
	"errors"
	"fmt"
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
		station.queueDeviceInvalidation(device)
	}
}

// queueDeviceInvalidation schedules invalidation for a disconnected device.
// Rapid connect/disconnect flapping coalesces into a single worker per
// station instead of accumulating one goroutine per OS notification.
func (bs *BaseStation) queueDeviceInvalidation(device bluetooth.Device) {
	bs.invalidationMutex.Lock()
	bs.pendingInvalidation = &device
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
		device := bs.pendingInvalidation
		bs.pendingInvalidation = nil
		if device == nil {
			bs.invalidationRunning = false
			bs.invalidationMutex.Unlock()
			return
		}
		bs.invalidationMutex.Unlock()
		invalidateDisconnectedDevice(bs, *device)
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
	if _, ok := err.(*UnsupportedCapabilityError); ok {
		// A capability rejection is a protocol decision, never a broken link.
		// Recursing into the wrapped ATT response would classify codes outside
		// the unsupported whitelist (such as standby's Value Not Allowed) as
		// transport failures and make healthy connections pay a reconnect.
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
		return true
	}
	if errors.Is(err, bluetooth.ErrGATTUnreachable) ||
		errors.Is(err, bluetooth.ErrGATTProtocol) ||
		errors.Is(err, bluetooth.ErrGATTAccessDenied) ||
		errors.Is(err, bluetooth.ErrGATTCommunication) {
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

// ScanForDuration performs a blocking BLE scan for the specified duration
// and returns a list of discovered base stations.
// Uses time.AfterFunc to stop the scan.
