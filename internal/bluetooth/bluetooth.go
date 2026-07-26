package bluetooth

import (
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
	connectedStationsMutex sync.Mutex
	uuidInitOnce           sync.Once
	uuidInitErr            error
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
}

// DiscoveredStation contains only immutable scan data. Keeping the mutex-bearing
// BaseStation out of scan results avoids copying a sync.RWMutex.
type DiscoveredStation struct {
	Name    string
	Address bluetooth.Address
}

// IsConnected returns the current connection status safely.
func (bs *BaseStation) IsConnected() bool {
	bs.mutex.Lock()
	if !bs.isConnected || bs.device == nil {
		bs.mutex.Unlock()
		return false
	}
	device := bs.device
	connected, err := device.Connected()
	if err == nil && connected {
		bs.mutex.Unlock()
		return true
	}
	bs.isConnected = false
	bs.device = nil
	bs.characteristic = nil
	bs.modeCharacteristic = nil
	bs.identifyCharacteristic = nil
	bs.LastPowerReadAt = time.Time{}
	bs.LastChannelReadAt = time.Time{}
	if err != nil {
		bs.LastError = err.Error()
	}
	bs.mutex.Unlock()
	_ = device.Disconnect()
	connectedStationsMutex.Lock()
	remaining := connectedStations[:0]
	for _, station := range connectedStations {
		if station != bs {
			remaining = append(remaining, station)
		}
	}
	connectedStations = remaining
	connectedStationsMutex.Unlock()
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
	bs.mutex.Unlock()
}

// MarkMissed requires two consecutive successful scans to mark a station
// absent. A single Windows BLE scan can miss a station while its GATT session
// is still being released.
func (bs *BaseStation) MarkMissed() {
	bs.mutex.Lock()
	bs.MissedScans++
	if bs.MissedScans >= 2 {
		bs.Present = false
	}
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

func IsAdapterUnavailable(err error) bool {
	return errors.Is(err, bluetooth.ErrRadioNotAvailable) ||
		errors.Is(err, bluetooth.ErrDisabledByPolicy)
}

// ScanForDuration performs a blocking BLE scan for the specified duration
// and returns a list of discovered base stations.
// Uses time.AfterFunc to stop the scan.
func ScanForDuration(duration time.Duration) ([]DiscoveredStation, error) {
	// log.Printf("[BT] ScanForDuration: Starting scan for %v...", duration)
	localStations := make(map[string]DiscoveredStation)
	var localMutex sync.Mutex
	var scanErr error
	var stopRequested atomic.Bool
	stopResult := make(chan error, 1)

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
		if _, found := localStations[addressString]; !found {
			// log.Printf("[BT] Scan: Discovered %s (%s)", result.LocalName(), result.Address.String())
		}
		localStations[addressString] = DiscoveredStation{
			Name:    localName,
			Address: result.Address,
		}
		localMutex.Unlock()
	}

	// Schedule StopScan using time.AfterFunc
	stopTimer := time.AfterFunc(duration, func() {
		stopRequested.Store(true)
		log.Printf("[BT] ScanForDuration (AfterFunc): Duration %v elapsed. Calling StopScan...", duration)
		err := stopScanSafely()
		if err != nil {
			log.Printf("[BT] ScanForDuration (AfterFunc): adapter.StopScan() error: %v", err)
		}
		stopResult <- err
	})

	// Start the blocking scan directly
	log.Println("[BT] ScanForDuration (AfterFunc): Calling adapter.Scan()...")
	scanErr = scanSafely(scanCallback) // This blocks until StopScan is called (by timer) or an error occurs
	timerStopped := stopTimer.Stop()
	var stopErr error
	if !timerStopped {
		// The timer callback has started. Wait for it so a late StopScan cannot
		// accidentally stop a subsequent scan.
		stopErr = <-stopResult
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

	if err := scanCompletionError(scanErr); err != nil {
		return nil, err
	}
	if stopErr != nil {
		return nil, fmt.Errorf("failed to stop Bluetooth scan safely: %w", stopErr)
	}
	return results, nil
}

func scanSafely(callback func(*bluetooth.Adapter, bluetooth.ScanResult)) (returnErr error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			returnErr = fmt.Errorf("Bluetooth scan panicked: %v\n%s", recovered, debug.Stack())
		}
	}()
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
	return stopScanSafely()
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
	if station.characteristic == nil {
		return fmt.Errorf("power characteristic is nil for %s", station.Name)
	}

	log.Printf("Bluetooth: Reading power state for %s (%s)", station.Name, station.Address)
	buf := make([]byte, 1)
	n, err := station.characteristic.Read(buf)
	if err != nil {
		station.LastPowerReadAt = time.Time{}
		return fmt.Errorf("failed to read power characteristic for %s: %w", station.Name, err)
	}
	if n != 1 {
		station.LastPowerReadAt = time.Time{}
		return fmt.Errorf("unexpected bytes read (%d) for power on %s", n, station.Name)
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
	if station.modeCharacteristic == nil {
		station.LastChannelReadAt = time.Time{}
		return fmt.Errorf("mode characteristic is nil for %s", station.Name)
	}

	// Read one extra byte so an overlong value is rejected instead of being
	// silently truncated to a seemingly valid four-byte integer.
	buf := make([]byte, 5)
	n, err := station.modeCharacteristic.Read(buf)
	if err != nil {
		station.LastChannelReadAt = time.Time{}
		return fmt.Errorf("failed to read channel for %s: %w", station.Name, err)
	}
	if n < 1 || n > 4 {
		station.LastChannelReadAt = time.Time{}
		return fmt.Errorf("unexpected bytes read (%d) for channel on %s", n, station.Name)
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
	if station == nil {
		return fmt.Errorf("station is nil")
	}

	station.mutex.Lock() // Lock for the duration
	defer station.mutex.Unlock()

	if !station.isConnected || station.device == nil {
		return fmt.Errorf("station %s is not connected", station.Name)
	}
	var readErrors []error
	if station.Capabilities.PowerRead && station.characteristic != nil {
		if err := readPowerStateInternal(station); err != nil {
			readErrors = append(readErrors, err)
		}
	} else {
		station.setPowerStateInternal(PowerStateUnknown, RawPowerStateUnknown)
	}
	if station.Capabilities.ChannelRead {
		if err := readChannelInternal(station); err != nil {
			readErrors = append(readErrors, err)
		}
	} else {
		station.Channel = ChannelUnknown
		station.LastChannelReadAt = time.Time{}
	}
	readErr := errors.Join(readErrors...)
	if readErr != nil {
		station.LastError = readErr.Error()
	} else {
		station.LastError = ""
	}
	return readErr
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
	buf := make([]byte, 256)
	n, err := characteristic.Read(buf)
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

// connectAndDiscoverInternal handles connection and discovery.
// Assumes caller holds the write lock (station.mutex.Lock()).
func connectAndDiscoverInternal(station *BaseStation) error {
	if station.isConnected && station.device != nil && station.characteristic != nil {
		connected, err := station.device.Connected()
		if err == nil && connected {
			return nil // Already good
		}
		disconnectInternal(station)
	}

	if !station.isConnected || station.device == nil {
		log.Printf("Bluetooth: Internal connect attempt for %s...", station.Name)
		device, err := adapter.Connect(station.Address, bluetooth.ConnectionParams{})
		if err != nil {
			station.isConnected = false
			station.device = nil
			station.characteristic = nil
			station.modeCharacteristic = nil
			station.identifyCharacteristic = nil
			station.LastPowerReadAt = time.Time{}
			station.LastChannelReadAt = time.Time{}
			station.LastError = err.Error()
			return fmt.Errorf("connection failed internal: %w", err)
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
			if i > 0 {
				log.Printf("Bluetooth: Retrying discovery for %s (attempt %d/%d)...", station.Name, i+1, maxRetries)
				disconnectInternal(station)
				time.Sleep(500 * time.Millisecond)
				device, connectErr := adapter.Connect(station.Address, bluetooth.ConnectionParams{})
				if connectErr != nil {
					err = fmt.Errorf("connection retry failed: %w", connectErr)
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

			services, discoverErr := station.device.DiscoverServices(nil)
			err = discoverErr
			if err != nil {
				continue
			}

			var powerCharacteristic characteristicIO
			var modeCharacteristic characteristicIO
			var identifyCharacteristic characteristicIO
			capabilities := Capabilities{}
			metadata := DeviceMetadata{}
			metadataRead := false
			controlServiceFound := false

			for serviceIndex := range services {
				service := services[serviceIndex]
				serviceUUID := service.UUID()
				if serviceUUID != powerControlServiceUUID && serviceUUID != deviceInformationServiceUUID {
					continue
				}
				chars, characteristicErr := service.DiscoverCharacteristics(nil)
				if characteristicErr != nil {
					if serviceUUID == powerControlServiceUUID {
						err = characteristicErr
						break
					}
					log.Printf("Bluetooth: Optional device information discovery failed for %s: %v", station.Name, characteristicErr)
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
					value, readErr := readMetadataValue(current)
					if readErr != nil {
						log.Printf("Bluetooth: Optional metadata read failed for %s (%s): %v", station.Name, current.UUID(), readErr)
						continue
					}
					switch current.UUID() {
					case manufacturerCharacteristicUUID:
						metadata.Manufacturer = value
						metadataRead = true
					case modelCharacteristicUUID:
						metadata.Model = value
						metadataRead = true
					case serialCharacteristicUUID:
						metadata.SerialNumber = value
						metadataRead = true
					case hardwareCharacteristicUUID:
						metadata.HardwareRevision = value
						metadataRead = true
					case firmwareCharacteristicUUID:
						metadata.FirmwareRevision = value
						metadataRead = true
					}
				}
			}

			if err != nil {
				continue
			}
			if !controlServiceFound || powerCharacteristic == nil {
				err = fmt.Errorf("power control service or characteristic not found")
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
			station.Metadata = mergeMetadata(station.Metadata, metadata)
			if metadataRead {
				station.MetadataReadAt = time.Now()
			}
			err = nil
			break
		}

		if err != nil {
			station.CapabilitiesKnown = false
			disconnectInternal(station)
			station.LastError = err.Error()
			return fmt.Errorf("discovery failed internal for %s after %d retries: %w", station.Name, maxRetries, err)
		}

		log.Printf("Bluetooth: Internal discovery successful for %s.", station.Name)
	}
	return nil
}

// InitialReadError reports non-fatal failures after connection and characteristic
// discovery succeeded. Callers can keep the station visible while showing the
// affected values as unknown.
type InitialReadError struct {
	Power   error
	Channel error
}

func (e *InitialReadError) Error() string {
	return fmt.Sprintf("initial station read was incomplete: %v", errors.Join(e.Power, e.Channel))
}

func (e *InitialReadError) Unwrap() error {
	return errors.Join(e.Power, e.Channel)
}

// EnsureCapabilities connects and discovers characteristics before a caller
// decides whether an operation is supported. This avoids rejecting a station
// forever because an earlier transient discovery left cached capabilities empty.
func EnsureCapabilities(station *BaseStation) (Capabilities, error) {
	if station == nil {
		return Capabilities{}, fmt.Errorf("station is nil")
	}
	station.mutex.Lock()
	defer station.mutex.Unlock()
	if err := connectAndDiscoverInternal(station); err != nil {
		return Capabilities{}, err
	}
	return station.Capabilities, nil
}

// RefreshCapabilities forces service and characteristic discovery on the
// current connection. It is used when a required optional characteristic was
// missing from an earlier, possibly incomplete discovery result.
func RefreshCapabilities(station *BaseStation) (Capabilities, error) {
	if station == nil {
		return Capabilities{}, fmt.Errorf("station is nil")
	}
	station.mutex.Lock()
	defer station.mutex.Unlock()
	disconnectInternal(station)
	station.CapabilitiesKnown = false
	if err := connectAndDiscoverInternal(station); err != nil {
		return Capabilities{}, err
	}
	return station.Capabilities, nil
}

// FetchInitialPowerState attempts to connect (if necessary) and read the initial power state.
func FetchInitialPowerState(station *BaseStation) error {
	if station == nil {
		return fmt.Errorf("station is nil")
	}

	station.mutex.Lock() // Lock for the whole operation
	defer station.mutex.Unlock()

	err := connectAndDiscoverInternal(station)
	if err != nil {
		station.LastError = err.Error()
		log.Printf("Bluetooth: Failed to connect/discover in FetchInitialPowerState for %s: %v", station.Name, err)
		return err
	}

	var powerReadErr error
	var channelReadErr error
	if station.Capabilities.PowerRead {
		log.Printf("Bluetooth: FetchInitialPowerState proceeding to read state for %s.", station.Name)
		if powerReadErr = readPowerStateInternal(station); powerReadErr != nil {
			log.Printf("Bluetooth: Failed to read state in FetchInitialPowerState for %s: %v", station.Name, powerReadErr)
		}
	} else {
		station.setPowerStateInternal(PowerStateUnknown, RawPowerStateUnknown)
	}
	if station.Capabilities.ChannelRead {
		if channelReadErr = readChannelInternal(station); channelReadErr != nil {
			// Channel support is optional so power control remains usable on
			// firmware that does not expose a readable mode characteristic.
			log.Printf("Bluetooth: Failed to read channel in FetchInitialPowerState for %s: %v", station.Name, channelReadErr)
		}
	} else {
		station.Channel = ChannelUnknown
		station.LastChannelReadAt = time.Time{}
	}

	if powerReadErr != nil || channelReadErr != nil {
		readErr := &InitialReadError{Power: powerReadErr, Channel: channelReadErr}
		station.LastError = readErr.Error()
		return readErr
	}
	station.LastError = ""
	log.Printf("Bluetooth: FetchInitialPowerState successful for %s. State: %d, channel: %d", station.Name, station.PowerState, station.Channel)
	return nil
}

// writePowerValueInternal writes one power-control byte, falling back to a
// response write when the firmware does not support WriteWithoutResponse.
// Assumes caller holds station.mutex.
func writeCharacteristicValueInternal(characteristic characteristicIO, value byte) error {
	n, err := characteristic.WriteWithoutResponse([]byte{value})
	if err != nil {
		n, err = characteristic.Write([]byte{value})
	}
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("wrote %d bytes instead of 1", n)
	}
	return nil
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
			consecutiveReadErrors++
			if consecutiveReadErrors >= 2 && attempt < attempts-1 {
				disconnectInternal(station)
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
		if err = connectAndDiscoverInternal(station); err != nil {
			log.Printf("Bluetooth: connect/discover failed during power attempt %d/%d for %s: %v", i+1, maxRetries, station.Name, err)
			if i == maxRetries-1 {
				return PowerControlResult{}, fmt.Errorf("failed to connect/discover before power command: %w", err)
			}
			disconnectInternal(station)
			time.Sleep(500 * time.Millisecond)
			continue
		}
		if !station.Capabilities.PowerWrite {
			return PowerControlResult{}, fmt.Errorf("power control is not supported for %s", station.Name)
		}

		log.Printf("Bluetooth: Sending %s command to %s", target, station.Name)
		if target == PowerStateSleep {
			// Some Lighthouse 2.0 firmware expects wake/prepare then sleep.
			err = writePowerValueInternal(station, 0x01)
			if err == nil {
				time.Sleep(50 * time.Millisecond)
				err = writePowerValueInternal(station, command)
			}
		} else {
			err = writePowerValueInternal(station, command)
		}
		if err == nil {
			break
		}
		log.Printf("Bluetooth: Write %s failed for %s: %v. Retrying...", target, station.Name, err)
		disconnectInternal(station)
		if i < maxRetries-1 {
			time.Sleep(500 * time.Millisecond)
		}
	}

	if err != nil {
		station.LastError = err.Error()
		return PowerControlResult{}, fmt.Errorf("failed to write %s command after %d retries: %w", target, maxRetries, err)
	}

	if !station.Capabilities.PowerRead {
		station.setPowerStateInternal(PowerStateUnknown, RawPowerStateUnknown)
		station.LastReadAt = time.Time{}
		station.LastError = ""
		return PowerControlResult{State: target, Confirmed: false}, nil
	}
	if err = confirmPowerStateInternal(station, target); err != nil {
		station.LastError = err.Error()
		return PowerControlResult{State: station.PowerState, Confirmed: false}, &PowerConfirmationError{
			Target: target,
			Actual: station.PowerState,
			Raw:    station.RawPowerState,
			Err:    fmt.Errorf("state confirmation failed for %s: %w", station.Name, err),
		}
	}
	station.LastReadAt = time.Now()
	station.LastError = ""
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
			return fmt.Errorf("identify is not supported for %s", station.Name)
		} else if err := writeCharacteristicValueInternal(station.identifyCharacteristic, 0x01); err != nil {
			lastErr = err
		} else {
			station.LastError = ""
			return nil
		}
		if attempt == 0 {
			disconnectInternal(station)
			time.Sleep(250 * time.Millisecond)
		}
	}
	station.LastError = lastErr.Error()
	return fmt.Errorf("failed to identify %s after retry: %w", station.Name, lastErr)
}

type ChannelWriteResult struct {
	PreviousChannel int
	Channel         int
	WriteWarning    string
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
		return result, fmt.Errorf("safe channel control requires readable and writable mode support for %s", station.Name)
	}
	if err := readChannelInternal(station); err != nil {
		station.LastError = err.Error()
		return result, fmt.Errorf("failed to read the existing channel for %s: %w", station.Name, err)
	}
	result.PreviousChannel = station.Channel
	if station.Channel == channel {
		result.Channel = station.Channel
		station.LastReadAt = time.Now()
		station.LastError = ""
		return result, nil
	}

	if writeErr := writeCharacteristicValueInternal(station.modeCharacteristic, byte(channel)); writeErr != nil {
		if readErr := readChannelInternal(station); readErr == nil {
			result.Channel = station.Channel
			if station.Channel == channel {
				result.WriteWarning = fmt.Sprintf("the write call reported an error, but channel %d was confirmed by readback: %v", channel, writeErr)
				station.LastReadAt = time.Now()
				station.LastError = ""
				return result, nil
			}
		} else {
			writeErr = errors.Join(writeErr, fmt.Errorf("final channel read failed: %w", readErr))
		}
		station.LastError = writeErr.Error()
		return result, fmt.Errorf("failed to write channel %d for %s: %w", channel, station.Name, writeErr)
	}
	var confirmationErr error
	consecutiveReadErrors := 0
	for attempt := 0; attempt < 5; attempt++ {
		time.Sleep(250 * time.Millisecond)
		if err := readChannelInternal(station); err != nil {
			confirmationErr = err
			consecutiveReadErrors++
			if consecutiveReadErrors >= 2 && attempt < 4 {
				disconnectInternal(station)
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
			station.LastError = ""
			return result, nil
		}
		confirmationErr = fmt.Errorf("reported channel %d, expected %d", station.Channel, channel)
	}
	if err := readChannelInternal(station); err == nil {
		result.Channel = station.Channel
	} else if confirmationErr == nil {
		confirmationErr = err
	}
	if confirmationErr == nil {
		confirmationErr = fmt.Errorf("no channel confirmation was received")
	}
	station.LastError = confirmationErr.Error()
	return result, fmt.Errorf("channel %d was written but could not be confirmed for %s: %w", channel, station.Name, confirmationErr)
}

// disconnectInternal performs disconnection without locking (must be called within locked context).
// Also removes station from the global tracking list.
func disconnectInternal(s *BaseStation) {
	if s.device != nil {
		log.Printf("Bluetooth: Disconnecting internal for %s", s.Name)
		_ = s.device.Disconnect()
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
		if cs.Address != s.Address {
			newConnectedStations = append(newConnectedStations, cs)
		}
	}
	connectedStations = newConnectedStations
	connectedStationsMutex.Unlock()
}

// DisconnectStation disconnects from a specific base station.
func DisconnectStation(station *BaseStation) {
	if station == nil {
		return
	}
	station.mutex.Lock() // Lock before calling internal disconnect
	defer station.mutex.Unlock()
	disconnectInternal(station) // Use internal helper
}

// ReleaseStationForScan closes GATT handles so the Lighthouse can advertise
// again while preserving the last known state for an offline/stale display.
func ReleaseStationForScan(station *BaseStation) {
	if station == nil {
		return
	}
	station.mutex.Lock()
	defer station.mutex.Unlock()

	disconnectInternal(station)
}

// DisconnectAllStations disconnects all tracked stations.
func DisconnectAllStations() {
	connectedStationsMutex.Lock()
	log.Printf("Bluetooth: Disconnecting all %d tracked stations...", len(connectedStations))
	stationsToDisconnect := make([]*BaseStation, len(connectedStations))
	copy(stationsToDisconnect, connectedStations)
	connectedStationsMutex.Unlock()

	for _, station := range stationsToDisconnect {
		DisconnectStation(station)
	}
	log.Println("Bluetooth: Disconnect all stations attempt finished.")
}
