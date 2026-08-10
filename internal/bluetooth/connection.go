package bluetooth

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"tinygo.org/x/bluetooth"
)

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
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(discoveryTiming.DiscoveryRetryDelay):
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
			if !capabilities.PowerRead {
				// Capability discovery is authoritative for this connection. Do
				// not retain a power value and freshness timestamp read through an
				// older GATT database that exposed a readable characteristic.
				station.clearPowerStateInternal()
			}
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
	s.bootRawTrustedOn = false

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
