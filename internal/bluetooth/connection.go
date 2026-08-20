package bluetooth

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"tinygo.org/x/bluetooth"
)

// connectNotStartedError reports a connect/discover request that was
// cancelled before it touched any connection state. Callers cleaning up after
// a cancelled operation must not disconnect a session this call never used;
// errors.Is still matches the wrapped context error.
type connectNotStartedError struct {
	Err error
}

func (e *connectNotStartedError) Error() string { return e.Err.Error() }

func (e *connectNotStartedError) Unwrap() error { return e.Err }

func isConnectNotStarted(err error) bool {
	var notStarted *connectNotStartedError
	return errors.As(err, &notStarted)
}

// contextNotStartedError marks a context error produced at a point where the
// connect/discover request has not created any connection state this call
// owns. Callers cleaning up a cancelled operation must not disconnect a
// session the call never used — including one a concurrent operation rebuilt
// while this call waited with the station lock released. Non-context errors
// pass through unchanged.
func contextNotStartedError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return &connectNotStartedError{Err: err}
	}
	return err
}

func connectAndDiscoverInternalContext(ctx context.Context, station *BaseStation) error {
	ctx = normalizeContext(ctx)
	if err := ctx.Err(); err != nil {
		return &connectNotStartedError{Err: err}
	}
	// The gate releases the station lock while waiting for an in-flight
	// disconnect or pending cleanup, so a cancellation landing there means
	// this call never touched connection state (and a concurrent operation
	// may even have rebuilt the session meanwhile). Mark the context error
	// so callers do not tear a session down this call never used.
	if err := connectGate(ctx, station); err != nil {
		return contextNotStartedError(err)
	}
	if station.isConnected && station.device != nil && station.characteristic != nil {
		connected, err := station.device.Connected()
		if err == nil {
			if connected {
				station.setConnectionErrorInternal(nil)
				return nil // Already good
			}
		} else if !errors.Is(err, bluetooth.ErrDeviceDisconnected) {
			// A status-query failure (a transient COM/RPC error around
			// GetConnectionStatus) is not proof the link dropped; keep the
			// cached session and let the next operation's own error
			// classification decide whether a reconnect is needed. Tearing the
			// session down here would make every transient query blip pay a
			// full reconnect and rediscovery. Do not record it as a connection
			// error either: nothing observed the link itself failing. The
			// closed-session sentinel is the exception: the library already
			// knows the link is dead, so the cache must fall through to the
			// teardown and reconnect below.
			log.Printf("Bluetooth: Connection status query failed for %s, keeping cached session: %v", station.Name, err)
			return nil
		}
		if err := disconnectInternal(station); err != nil {
			return transportError("disconnect stale station connection", err)
		}
	}

	if !station.isConnected || station.device == nil {
		// The stale-session teardown above releases the station lock around
		// WinRT calls; an OS disconnect landing in that window can start an
		// eager cleanup (disconnectInFlight/pendingCleanup) for this station.
		// Re-run the gate immediately before opening the replacement session.
		// A cancellation inside the gate still owns no session (and the gate's
		// unlocked window may have let a concurrent reconnect finish), so keep
		// the context error marked as a never-started connect.
		if err := connectGate(ctx, station); err != nil {
			return contextNotStartedError(err)
		}
		// The gate releases the lock around its own WinRT cleanup; a
		// concurrent operation can complete a full reconnect inside that
		// window. Re-check the connection state before opening another
		// session: a duplicate GATT session would overwrite station.device
		// and orphan the first session's WinRT objects until process exit.
		if !station.isConnected || station.device == nil {
			log.Printf("Bluetooth: Internal connect attempt for %s...", station.Name)
			device, err := connectContext(ctx, station.Address)
			if err != nil {
				station.isConnected = false
				station.device = nil
				station.characteristic = nil
				station.modeCharacteristic = nil
				station.identifyCharacteristic = nil
				// A failed connect does not invalidate earlier observations:
				// keep the read timestamps so cached-state classification and
				// the duplicate-command suppression stay authoritative,
				// matching the discovery-retry failure path and the
				// interrupted-read preservation contract.
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
	}

	if station.characteristic == nil {
		log.Printf("Bluetooth: Internal discovery attempt for %s...", station.Name)

		var err error

		discoveryTiming := CurrentTiming()
		maxRetries := discoveryTiming.DiscoveryAttempts
		for i := 0; i < maxRetries; i++ {
			if contextErr := ctx.Err(); contextErr != nil {
				return contextErr
			}
			if i > 0 {
				log.Printf("Bluetooth: Retrying discovery for %s (attempt %d/%d)...", station.Name, i+1, maxRetries)
				if cleanupErr := disconnectInternal(station); cleanupErr != nil {
					return transportError("cleanup before discovery retry", cleanupErr)
				}
				// The retry delay runs outside the station lock, matching every
				// other wait in this package: snapshots and short readers must
				// not queue behind up to DiscoveryAttempts full retry windows.
				station.mutex.Unlock()
				waitErr := sleepContext(ctx, discoveryTiming.DiscoveryRetryDelay)
				station.mutex.Lock()
				if waitErr != nil {
					// The retry already disconnected this call's session, and
					// the unlocked sleep window may have let a concurrent
					// operation reconnect. A cancellation here owns no session.
					return contextNotStartedError(waitErr)
				}
				// The unlocked sleep window lets an OS disconnect start an eager
				// cleanup for this station. Re-run the same gate the entry uses:
				// opening a new GATT session while the previous one is still
				// released drops or corrupts single-connection peripherals.
				// A cancellation inside the gate keeps the same never-started
				// marking for the same reason.
				if gateErr := connectGate(ctx, station); gateErr != nil {
					return contextNotStartedError(gateErr)
				}
				// The unlocked windows also let a concurrent operation complete
				// a full reconnect for this station. Reuse that session instead
				// of opening a duplicate GATT session that would orphan the
				// first session's WinRT objects until process exit.
				if station.isConnected && station.device != nil {
					if station.characteristic != nil {
						return nil
					}
				} else {
					device, connectErr := connectContext(ctx, station.Address)
					if connectErr != nil {
						if errors.Is(connectErr, context.Canceled) || errors.Is(connectErr, context.DeadlineExceeded) {
							// The aborted connect created no session; mark it so
							// caller cleanup does not disconnect a session the
							// retry never used.
							return contextNotStartedError(connectErr)
						}
						err = transportError("retry station connection", connectErr)
						continue
					}
					station.device = device
					station.isConnected = true
					connectedStationsMutex.Lock()
					found := false
					for _, connected := range connectedStations {
						if connected.Address == station.Address {
							found = true
							break
						}
					}
					if !found {
						connectedStations = append(connectedStations, station)
					}
					connectedStationsMutex.Unlock()
				}
			}

			services, discoverErr := discoverServicesContext(ctx, station.device)
			if errors.Is(discoverErr, context.Canceled) || errors.Is(discoverErr, context.DeadlineExceeded) {
				return discoverErr
			}
			if discoverErr != nil {
				err = transportError("discover GATT services", discoverErr)
				continue
			}
			outcome, outcomeErr := discoverServicesOutcome(ctx, station.Name, services)
			if outcomeErr != nil {
				if errors.Is(outcomeErr, context.Canceled) || errors.Is(outcomeErr, context.DeadlineExceeded) {
					return outcomeErr
				}
				err = transportError("discover control characteristics", outcomeErr)
				continue
			}
			if !outcome.controlServiceFound || outcome.power == nil {
				// Discovery succeeded but the service list is incomplete. The
				// Windows uncached discovery drops services on unstable links
				// and freshly booted devices, so a missing control service is
				// an incomplete result, not an explicit capability rejection.
				// Keep it retryable; classifying it as unsupported would mark
				// the station permanently capability-less and stop recovery.
				err = fmt.Errorf("%s discovery did not return the Lighthouse power control service", station.Name)
				continue
			}
			applyDiscoveryOutcome(station, outcome)
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

// discoveryOutcome is one GATT discovery pass over a connected session: the
// control characteristics with their capabilities, and the optional device
// information values.
type discoveryOutcome struct {
	power                characteristicIO
	mode                 characteristicIO
	identify             characteristicIO
	capabilities         Capabilities
	metadata             DeviceMetadata
	metadataServiceFound bool
	metadataRecognized   int
	metadataSuccessful   int
	metadataErrors       []error
	controlServiceFound  bool
}

// discoverServicesOutcome walks discovered services once, collecting the
// control characteristics and optional metadata. It returns a context error or
// a control-characteristic discovery failure; optional device-information
// failures are collected in the outcome instead.
func discoverServicesOutcome(ctx context.Context, stationName string, services []bluetooth.DeviceService) (discoveryOutcome, error) {
	outcome := discoveryOutcome{metadataErrors: make([]error, 0)}
	for serviceIndex := range services {
		service := services[serviceIndex]
		serviceUUID := service.UUID()
		if serviceUUID != powerControlServiceUUID && serviceUUID != deviceInformationServiceUUID {
			continue
		}
		if serviceUUID == deviceInformationServiceUUID {
			outcome.metadataServiceFound = true
		}
		chars, characteristicErr := discoverCharacteristicsContext(ctx, service)
		if characteristicErr != nil {
			if errors.Is(characteristicErr, context.Canceled) || errors.Is(characteristicErr, context.DeadlineExceeded) {
				return outcome, characteristicErr
			}
			if serviceUUID == powerControlServiceUUID {
				return outcome, characteristicErr
			}
			log.Printf("Bluetooth: Optional device information discovery failed for %s: %v", stationName, characteristicErr)
			outcome.metadataErrors = append(outcome.metadataErrors, fmt.Errorf("discover device information characteristics: %w", characteristicErr))
			continue
		}

		if serviceUUID == powerControlServiceUUID {
			outcome.controlServiceFound = true
			for characteristicIndex := range chars {
				current := &chars[characteristicIndex]
				properties := current.Properties()
				switch current.UUID() {
				case powerControlCharacteristicUUID:
					outcome.power = current
					outcome.capabilities.PowerRead = hasRead(properties)
					outcome.capabilities.PowerWrite = hasWrite(properties)
					outcome.capabilities.PowerNotify = hasNotify(properties)
					outcome.capabilities.Standby = outcome.capabilities.PowerWrite
				case modeCharacteristicUUID:
					outcome.mode = current
					outcome.capabilities.ChannelRead = hasRead(properties)
					outcome.capabilities.ChannelWrite = hasWrite(properties)
					outcome.capabilities.ChannelNotify = hasNotify(properties)
				case identifyCharacteristicUUID:
					outcome.identify = current
					outcome.capabilities.Identify = hasWrite(properties)
				}
			}
			continue
		}

		outcome.capabilities.DeviceInformation = true
		for characteristicIndex := range chars {
			current := &chars[characteristicIndex]
			switch current.UUID() {
			case manufacturerCharacteristicUUID, modelCharacteristicUUID,
				serialCharacteristicUUID, hardwareCharacteristicUUID,
				firmwareCharacteristicUUID:
			default:
				continue
			}
			outcome.metadataRecognized++
			value, readErr := readMetadataValueContext(ctx, current)
			if readErr != nil {
				if errors.Is(readErr, context.Canceled) || errors.Is(readErr, context.DeadlineExceeded) {
					return outcome, readErr
				}
				log.Printf("Bluetooth: Optional metadata read failed for %s (%s): %v", stationName, current.UUID(), readErr)
				outcome.metadataErrors = append(outcome.metadataErrors, fmt.Errorf("read %s: %w", current.UUID(), readErr))
				continue
			}
			outcome.metadataSuccessful++
			switch current.UUID() {
			case manufacturerCharacteristicUUID:
				outcome.metadata.Manufacturer = value
			case modelCharacteristicUUID:
				outcome.metadata.Model = value
			case serialCharacteristicUUID:
				outcome.metadata.SerialNumber = value
			case hardwareCharacteristicUUID:
				outcome.metadata.HardwareRevision = value
			case firmwareCharacteristicUUID:
				outcome.metadata.FirmwareRevision = value
			}
		}
	}
	return outcome, nil
}

// applyDiscoveryOutcome writes one successful discovery pass onto the station.
// Assumes the caller holds the station write lock.
func applyDiscoveryOutcome(station *BaseStation, outcome discoveryOutcome) {
	station.characteristic = outcome.power
	station.modeCharacteristic = outcome.mode
	station.identifyCharacteristic = outcome.identify
	station.Capabilities = outcome.capabilities
	station.CapabilitiesKnown = true
	if !outcome.capabilities.PowerRead {
		// Capability discovery is authoritative for this connection. Do
		// not retain a power value and freshness timestamp read through an
		// older GATT database that exposed a readable characteristic.
		station.clearPowerStateInternal()
	}
	if !outcome.capabilities.ChannelRead {
		// Do not retain a channel obtained from an older discovery when
		// the current firmware/session no longer exposes a readable Mode
		// characteristic. A stale value must not participate in safety
		// checks.
		station.Channel = ChannelUnknown
		station.LastChannelReadAt = time.Time{}
	}
	station.applyMetadataDiscovery(
		outcome.metadata,
		outcome.metadataServiceFound,
		outcome.metadataRecognized,
		outcome.metadataSuccessful,
		errors.Join(outcome.metadataErrors...),
		time.Now(),
	)
	station.setConnectionErrorInternal(nil)
}

// InitialReadError reports non-fatal failures after connection and characteristic
// discovery succeeded. Callers can keep the station visible while showing the
// affected values as unknown.

// beginInFlightDisconnect publishes the disconnect marker while the caller is
// about to run device.Disconnect outside the station lock. It returns the
// channel that waitForInFlightDisconnect blocks on. Callers must hold s.mutex.
func beginInFlightDisconnect(s *BaseStation) chan struct{} {
	wait := make(chan struct{})
	s.disconnectInFlight = wait
	return wait
}

// endInFlightDisconnect clears and closes the marker once the out-of-lock
// Disconnect call has returned. Callers must hold s.mutex.
func endInFlightDisconnect(s *BaseStation, wait chan struct{}) {
	if s.disconnectInFlight == wait {
		s.disconnectInFlight = nil
	}
	close(wait)
}

// waitForInFlightDisconnect blocks until a disconnect that released the
// station lock has finished. Without this, a reconnect starting inside that
// window would open a new GATT session while the old one is still being
// released; single connection peripherals drop or corrupt such overlapping
// sessions. Callers must hold s.mutex and keep it held on return.
func waitForInFlightDisconnect(ctx context.Context, s *BaseStation) error {
	for s.disconnectInFlight != nil {
		wait := s.disconnectInFlight
		s.mutex.Unlock()
		select {
		case <-wait:
		case <-ctx.Done():
			s.mutex.Lock()
			return ctx.Err()
		}
		s.mutex.Lock()
	}
	return nil
}

// connectGate centralizes the pre-connect guard every connection must pass:
// wait for a disconnect that released the station lock, then finish any
// pending cleanup left by a failed synchronous Disconnect. Skipping it opens
// a new GATT session while the previous one is still being released, which
// single-connection peripherals drop or corrupt. Callers must hold
// station.mutex; the gate may release and reacquire the lock but always
// returns with it held.
func connectGate(ctx context.Context, station *BaseStation) error {
	if err := waitForInFlightDisconnect(ctx, station); err != nil {
		return err
	}
	if err := cleanupPendingInternal(station); err != nil {
		return transportError("finish previous connection cleanup", err)
	}
	return nil
}

// disconnectInternal disconnects a station. It must be called with s.mutex
// held and returns with s.mutex held; the WinRT cleanup itself runs outside
// the lock because an unresponsive device can stretch it far past every
// caller deadline, and every observable field already describes a
// disconnected station by then.
func disconnectInternal(s *BaseStation) error {
	if cleanupErr := cleanupPendingInternal(s); cleanupErr != nil {
		return cleanupErr
	}
	var disconnectErr error
	if s.device != nil {
		device := s.device
		log.Printf("Bluetooth: Disconnecting internal for %s", s.Name)
		detachConnectionStateInternal(s)
		wait := beginInFlightDisconnect(s)
		s.mutex.Unlock()
		disconnectErr = device.Disconnect()
		s.mutex.Lock()
		endInFlightDisconnect(s, wait)
		if bluetooth.IsDisconnectCleanupComplete(disconnectErr) {
			log.Printf("Bluetooth: Disconnect cleanup warning for %s: %v", s.Name, disconnectErr)
			disconnectErr = nil
		} else if disconnectErr != nil && s.pendingCleanup == nil {
			s.pendingCleanup = device
			trackPendingCleanupStation(s)
		}
	} else {
		// No device handle remains, so there is nothing to tear down: keep
		// the read timestamps intact. finishInterruptedInitialRead restores
		// them deliberately after a disconnect for observations that completed
		// before the cancellation, and a later redundant disconnect (explicit
		// disconnect, scan pre-release, or fleet invalidation on an already
		// disconnected station) must not wipe that freshness signal.
		detachConnectionHandlesInternal(s)
	}

	connectedStationsMutex.Lock()
	newConnectedStations := make([]*BaseStation, 0, len(connectedStations))
	for _, cs := range connectedStations {
		// Disconnect() can block past every caller deadline while the station
		// lock is released; a concurrent operation may reconnect this station
		// before this cleanup returns. Only release the tracking entry when
		// this disconnect still owns the connection state, otherwise a rebuilt
		// connection loses its entry and leaks from DisconnectAllStations and
		// adapter-change invalidation.
		if cs == s && s.device == nil && !s.isConnected && s.pendingCleanup == nil {
			continue
		}
		newConnectedStations = append(newConnectedStations, cs)
	}
	connectedStations = newConnectedStations
	connectedStationsMutex.Unlock()
	return disconnectErr
}

// detachConnectionStateInternal clears every connection-owned field so the
// station reads as disconnected before the (possibly long) WinRT cleanup
// runs. Callers must hold s.mutex for writing.
func detachConnectionStateInternal(s *BaseStation) {
	s.isConnected = false
	s.device = nil
	s.characteristic = nil
	s.modeCharacteristic = nil
	s.identifyCharacteristic = nil
	s.LastPowerReadAt = time.Time{}
	s.LastChannelReadAt = time.Time{}
	s.bootRawTrustedOn = false
	// The boot fallback window is connection-scoped: a disconnect can outlast
	// the window, and carrying the old timestamp would fast-forward the first
	// boot-like read after reconnect to a trusted On.
	s.bootingSince = time.Time{}
}

// detachConnectionHandlesInternal clears the connection handle state without
// touching the last read timestamps, for stations whose device handle is
// already gone. Observations recorded before the handle disappeared remain
// authoritative (and callers use their timestamps to avoid duplicate
// commands), so a redundant disconnect must not expire them. Callers must
// hold s.mutex for writing.
func detachConnectionHandlesInternal(s *BaseStation) {
	s.isConnected = false
	s.device = nil
	s.characteristic = nil
	s.modeCharacteristic = nil
	s.identifyCharacteristic = nil
	s.bootRawTrustedOn = false
	s.bootingSince = time.Time{}
}

// cleanupPendingInternal completes a previously failed synchronous Disconnect.
// It must run before a replacement device is connected. Called with s.mutex
// held; returns with s.mutex held. The WinRT cleanup runs outside the lock
// for the same reason as disconnectInternal.
func cleanupPendingInternal(s *BaseStation) error {
	if s.pendingCleanup == nil {
		return nil
	}
	pending := s.pendingCleanup
	s.pendingCleanup = nil
	wait := beginInFlightDisconnect(s)
	s.mutex.Unlock()
	err := pending.Disconnect()
	s.mutex.Lock()
	endInFlightDisconnect(s, wait)
	if err != nil {
		if bluetooth.IsDisconnectCleanupComplete(err) {
			log.Printf("Bluetooth: Pending disconnect cleanup completed for %s with warning: %v", s.Name, err)
			removePendingCleanupStation(s)
			if s.device == nil && !s.isConnected {
				pruneDisconnectedStation(s)
			}
			return nil
		}
		// Keep the handle around for a later retry unless an interleaved
		// disconnect recorded a newer pending handle meanwhile.
		if s.pendingCleanup == nil {
			s.pendingCleanup = pending
			trackPendingCleanupStation(s)
		}
		return err
	}
	// A reconnect that completes the pending cleanup must also leave the
	// global tracking list; otherwise a healthy station keeps a stale entry
	// until an explicit disconnect removes it. A station that is still
	// disconnected also leaves the connected list; a concurrent reconnect is
	// re-added with deduplication by the connect path.
	removePendingCleanupStation(s)
	if s.device == nil && !s.isConnected {
		pruneDisconnectedStation(s)
	}
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

// trackPendingCleanupStation keeps the invariant that a station holding a
// pendingCleanup handle is always tracked for fleet-wide cleanup. Without it
// a Disconnect that fails while another operation already pruned the station
// from connectedStations leaks its WinRT handles until process exit.
func trackPendingCleanupStation(station *BaseStation) {
	connectedStationsMutex.Lock()
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

// pruneDisconnectedStation drops a station whose cleanup just completed from
// the connected tracking list when no live connection remains. The reconnect
// path re-adds entries with deduplication, so pruning right after a
// successful cleanup cannot lose a healthy connection.
func pruneDisconnectedStation(station *BaseStation) {
	connectedStationsMutex.Lock()
	remaining := connectedStations[:0]
	for _, tracked := range connectedStations {
		if tracked != station {
			remaining = append(remaining, tracked)
		}
	}
	connectedStations = remaining
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
