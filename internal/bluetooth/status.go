package bluetooth

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"tinygo.org/x/bluetooth"
)

// statusValueReadSize generously exceeds any plausible value of the power
// and mode characteristics so a device reporting an overlong value surfaces
// as application-level validation (a DeviceValueError) instead of a transport
// buffer error. Values that still exceed this buffer carry the transport's
// ErrReadValueTooLong marker and receive the same DeviceValueError
// classification below: classifying them as a transport failure would discard
// a healthy connection and reconnect on every read without ever changing the
// value the device reports.
const statusValueReadSize = 64

func readPowerStateInternalContext(ctx context.Context, station *BaseStation) error {
	if station.characteristic == nil {
		return transportError("read power characteristic", fmt.Errorf("power characteristic is nil for %s", station.Name))
	}

	log.Printf("Bluetooth: Reading power state for %s (%s)", station.Name, station.Address)
	buf := make([]byte, statusValueReadSize)
	n, err := readCharacteristicContext(ctx, station.characteristic, buf)
	if err != nil {
		station.LastPowerReadAt = time.Time{}
		if IsCapabilityUnsupported(err) {
			station.Capabilities.PowerRead = false
			station.clearPowerStateInternal()
			return unsupportedCapability("power read", err)
		}
		if errors.Is(err, bluetooth.ErrReadValueTooLong) {
			return deviceValueError("read power characteristic", fmt.Errorf("overlong value for %s: %w", station.Name, err))
		}
		return transportError("read power characteristic", fmt.Errorf("%s: %w", station.Name, err))
	}
	if n != 1 {
		station.LastPowerReadAt = time.Time{}
		return deviceValueError("read power characteristic", fmt.Errorf("unexpected value length %d for %s", n, station.Name))
	}

	rawState := int(buf[0])
	rawDecoded := DecodePowerState(buf[0])
	newState := decodePowerStateWithHistory(buf[0], station.PowerState, station.bootingSince, time.Now())
	if rawDecoded == PowerStateBooting && station.bootRawTrustedOn {
		newState = PowerStateOn
	} else if rawDecoded == PowerStateBooting && newState == PowerStateOn {
		// The same connection has reported a boot-like value throughout the
		// fallback window. Keep it stable from now on; IsPowerStateVerified
		// treats this compatibility On as verified for operation decisions.
		station.bootRawTrustedOn = true
	} else if rawDecoded != PowerStateBooting {
		station.bootRawTrustedOn = false
	}

	if station.PowerState != newState { // Check before logging
		log.Printf("Bluetooth: Power state for %s changed from %d to %d", station.Name, station.PowerState, newState)
	}
	station.setPowerStateInternal(newState, rawState)
	station.LastPowerReadAt = time.Now()
	station.updateLastReadInternal(station.LastPowerReadAt)

	return nil
}

// Some Lighthouse 2.0 firmware reports booting raw values (such as 0x01)
// both while starting and while already awake. Always expose a fresh
// boot-like observation as Booting, even after On, then fall back after a
// bounded observation window. readPowerStateInternalContext makes that
// fallback sticky for the current connection so compatibility firmware does
// not oscillate between Booting and On on every poll.
func decodePowerStateWithHistory(raw byte, previous PowerState, bootingSince, now time.Time) PowerState {
	state := DecodePowerState(raw)
	if state != PowerStateBooting {
		return state
	}
	if previous == PowerStateBooting && !bootingSince.IsZero() && now.Sub(bootingSince) >= CurrentTiming().BootFallbackAfter {
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

	// Read well past the expected four-byte maximum so an overlong value is
	// rejected by the length validation below instead of being truncated to a
	// seemingly valid integer or surfacing as a transport buffer error.
	buf := make([]byte, statusValueReadSize)
	n, err := readCharacteristicContext(ctx, station.modeCharacteristic, buf)
	if err != nil {
		station.LastChannelReadAt = time.Time{}
		if IsCapabilityUnsupported(err) {
			station.Capabilities.ChannelRead = false
			station.Channel = ChannelUnknown
			return unsupportedCapability("channel read", err)
		}
		if errors.Is(err, bluetooth.ErrReadValueTooLong) {
			return deviceValueError("read channel characteristic", fmt.Errorf("overlong value for %s: %w", station.Name, err))
		}
		return transportError("read channel characteristic", fmt.Errorf("%s: %w", station.Name, err))
	}
	if n < 1 || n > 4 {
		station.LastChannelReadAt = time.Time{}
		return deviceValueError("read channel characteristic", fmt.Errorf("unexpected value length %d for %s", n, station.Name))
	}

	channel, err := DecodeChannel(buf[:n])
	if err != nil {
		station.LastChannelReadAt = time.Time{}
		// An out-of-range channel value is malformed device data, like the
		// length violation above: reconnecting cannot change what the device
		// reports, so it must carry the DeviceValueError classification that
		// short-circuits confirmation reconnects and failure accounting.
		return deviceValueError("read channel characteristic", fmt.Errorf("invalid channel for %s: %w", station.Name, err))
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

func finishStatusReadResult(station *BaseStation, powerErr, channelErr error) error {
	station.setPowerErrorInternal(powerErr)
	station.setChannelErrorInternal(channelErr)
	if powerErr != nil || channelErr != nil {
		return &StatusReadError{Power: powerErr, Channel: channelErr}
	}
	station.setConnectionErrorInternal(nil)
	return nil
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
	// The context can expire while this read waits behind another operation.
	// In that case no field read started, so preserve the last authoritative
	// values and error domains instead of turning cancellation into a failed
	// power observation.
	if err := ctx.Err(); err != nil {
		return err
	}

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
		station.clearPowerStateInternal()
	}
	if contextErr := ctx.Err(); contextErr != nil {
		if powerReadErr != nil {
			powerReadErr = errors.Join(powerReadErr, contextErr)
			// Report the interruption on the channel field too instead of a
			// nil: a nil channel error looks like a clean observation and
			// makes callers clear the station's channel-retry state for a
			// channel that was never read. The pure-context classification
			// routes upstream to a pending re-read instead.
			return finishStatusReadResult(station, powerReadErr, contextErr)
		}
		if station.Capabilities.ChannelRead {
			return finishStatusReadResult(station, nil, contextErr)
		}
		// Cancellation landed after the last supported field completed. The
		// observation is authoritative, matching the initial-read path.
		return finishStatusReadResult(station, nil, nil)
	}
	if station.Capabilities.ChannelRead {
		channelReadErr = readChannelInternalContext(ctx, station)
	} else {
		station.Channel = ChannelUnknown
		station.LastChannelReadAt = time.Time{}
	}
	if contextErr := ctx.Err(); contextErr != nil && channelReadErr != nil {
		channelReadErr = errors.Join(channelReadErr, contextErr)
	}
	// A channel read that returned successfully remains complete even if the
	// caller cancelled concurrently just before this post-read check.
	return finishStatusReadResult(station, powerReadErr, channelReadErr)
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

// applyMetadataDiscovery commits one completed optional device-information
// discovery. The caller holds the station write lock. A revision is recorded
// even when the read is partial, because a zero freshness timestamp otherwise
// makes the first failure indistinguishable from no attempt at all.
func (bs *BaseStation) applyMetadataDiscovery(
	discovered DeviceMetadata,
	serviceFound bool,
	recognized int,
	successful int,
	metadataErr error,
	now time.Time,
) {
	bs.Metadata, bs.MetadataReadAt = reconcileMetadata(
		bs.Metadata,
		discovered,
		serviceFound,
		recognized,
		successful,
		metadataErr != nil,
		now,
	)
	bs.setMetadataErrorInternal(metadataErr)
	bs.MetadataReadRevision++
}

// connectAndDiscoverInternal handles connection and discovery.
// Assumes caller holds the write lock (station.mutex.Lock()).
func connectAndDiscoverInternal(station *BaseStation) error {
	return connectAndDiscoverInternalContext(context.Background(), station)
}

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
	if err := ctx.Err(); err != nil {
		return Capabilities{}, err
	}
	station.mutex.Lock()
	defer station.mutex.Unlock()
	// Cancellation can land while this check waits for another station
	// operation. If discovery never starts, preserve the healthy session.
	if err := ctx.Err(); err != nil {
		return Capabilities{}, err
	}
	if err := connectAndDiscoverInternalContext(ctx, station); err != nil {
		if isConnectNotStarted(err) {
			// Discovery never started, so the cached session stays intact.
			return Capabilities{}, err
		}
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
	if err := ctx.Err(); err != nil {
		return Capabilities{}, err
	}
	station.mutex.Lock()
	defer station.mutex.Unlock()
	// Cancellation may land while this refresh waits for an in-flight station
	// operation. Re-check before disconnecting: a refresh that never started
	// must not destroy a healthy cached connection or invalidate capabilities.
	if err := ctx.Err(); err != nil {
		return Capabilities{}, err
	}
	if err := disconnectInternal(station); err != nil {
		return Capabilities{}, transportError("cleanup before capability refresh", err)
	}
	station.CapabilitiesKnown = false
	if err := connectAndDiscoverInternalContext(ctx, station); err != nil {
		if isConnectNotStarted(err) {
			// The refresh deliberately disconnected first; a connect that never
			// started leaves nothing further to clean up.
			return Capabilities{}, err
		}
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

func finishInitialReadResult(
	station *BaseStation,
	powerReadErr error,
	channelReadErr error,
	metadataReadErr error,
) error {
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
	return nil
}

func finishInterruptedInitialRead(
	station *BaseStation,
	contextErr error,
	powerReadCompleted bool,
	channelReadCompleted bool,
	powerReadErr error,
	channelReadErr error,
	metadataReadErr error,
) error {
	// A cancelled GATT read still needs the same connection cleanup as a
	// cancelled discovery. Preserve timestamps for fields that completed first:
	// those observations remain authoritative even though their connection was
	// closed, and callers can use them to avoid duplicate commands.
	powerReadAt := station.LastPowerReadAt
	channelReadAt := station.LastChannelReadAt
	if cleanupErr := disconnectInternal(station); cleanupErr != nil {
		contextErr = errors.Join(contextErr, transportError("cleanup cancelled initial read", cleanupErr))
	}
	if powerReadCompleted {
		station.LastPowerReadAt = powerReadAt
	}
	if channelReadCompleted {
		station.LastChannelReadAt = channelReadAt
	}
	if channelReadErr == nil && !channelReadCompleted {
		channelReadErr = contextErr
	} else if channelReadErr != nil {
		channelReadErr = errors.Join(channelReadErr, contextErr)
	}
	return finishInitialReadResult(station, powerReadErr, channelReadErr, metadataReadErr)
}

// FetchInitialPowerStateContext performs the full initial GATT read using one
// context so a stopped scan can cancel connection, discovery, and reads.
func FetchInitialPowerStateContext(ctx context.Context, station *BaseStation) error {
	if station == nil {
		return fmt.Errorf("station is nil")
	}
	ctx = normalizeContext(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}

	station.mutex.Lock() // Lock for the whole operation
	defer station.mutex.Unlock()
	// A timeout may expire while this initial read waits behind another
	// operation. Do not treat a read that never started as cancelled discovery.
	if err := ctx.Err(); err != nil {
		return err
	}

	err := connectAndDiscoverInternalContext(ctx, station)
	if err != nil {
		if isConnectNotStarted(err) {
			// The read never started; keep the pre-existing healthy session
			// instead of tearing it down as cancelled discovery would.
			return err
		}
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
	powerReadCompleted := false
	if station.Capabilities.PowerRead {
		log.Printf("Bluetooth: FetchInitialPowerState proceeding to read state for %s.", station.Name)
		if powerReadErr = readPowerStateInternalContext(ctx, station); powerReadErr != nil {
			log.Printf("Bluetooth: Failed to read state in FetchInitialPowerState for %s: %v", station.Name, powerReadErr)
		} else {
			powerReadCompleted = true
		}
	} else {
		station.clearPowerStateInternal()
	}
	if contextErr := ctx.Err(); contextErr != nil {
		if powerReadCompleted {
			// Cancellation landed after a complete power observation, between
			// independent fields. Preserve that authoritative read so a caller
			// can avoid sending a duplicate power command. Only the unread
			// channel is incomplete; no GATT call remains in flight to clean up.
			if station.Capabilities.ChannelRead {
				channelReadErr = contextErr
			}
			return finishInitialReadResult(station, powerReadErr, channelReadErr, metadataReadErr)
		}
		return finishCancelledInitialRead(station, contextErr)
	}
	channelReadAttempted := false
	channelReadCompleted := false
	if station.Capabilities.ChannelRead {
		channelReadAttempted = true
		channelReadErr = readChannelInternalContext(ctx, station)
		if channelReadErr != nil {
			// Channel support is optional so power control remains usable on
			// firmware that does not expose a readable mode characteristic.
			log.Printf("Bluetooth: Failed to read channel in FetchInitialPowerState for %s: %v", station.Name, channelReadErr)
		} else {
			channelReadCompleted = true
		}
	} else {
		station.Channel = ChannelUnknown
		station.LastChannelReadAt = time.Time{}
	}

	if contextErr := ctx.Err(); contextErr != nil {
		if channelReadAttempted && !channelReadCompleted {
			return finishInterruptedInitialRead(
				station,
				contextErr,
				powerReadCompleted,
				channelReadCompleted,
				powerReadErr,
				channelReadErr,
				metadataReadErr,
			)
		}
		if powerReadCompleted || channelReadCompleted {
			// At least one field completed before cancellation. The context-aware
			// read has already completed, so retain successful observations and
			// report only any field that actually failed.
			return finishInitialReadResult(station, powerReadErr, channelReadErr, metadataReadErr)
		}
		return finishCancelledInitialRead(station, contextErr)
	}
	if err := finishInitialReadResult(station, powerReadErr, channelReadErr, metadataReadErr); err != nil {
		return err
	}
	log.Printf("Bluetooth: FetchInitialPowerState successful for %s. State: %d, channel: %d", station.Name, station.PowerState, station.Channel)
	return nil
}

// writeCharacteristicValueInternal writes one byte using the modes advertised
// by the characteristic. A transport failure from WriteWithoutResponse must
// not be retried as a response write because the first command may have
// reached the device.
// Assumes caller holds station.mutex.
