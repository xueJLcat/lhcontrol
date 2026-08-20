package station

import (
	"context"
	"errors"
	"fmt"
	"lhcontrol/internal/bluetooth"
	"time"
)

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
	operationContext, cancelOperation := m.newStationOperationContext(m.lifecycleContext)
	defer cancelOperation()
	if err := m.beginForegroundStationOperationContext(operationContext, canonicalAddress); err != nil {
		return result, m.stationOperationContextError(err)
	}
	defer m.endStationOperation(canonicalAddress)
	targetSnapshot := stationPtr.Snapshot()
	metadataReadRevision := targetSnapshot.MetadataReadRevision
	defer func() {
		m.reconcileMetadataReadResult(canonicalAddress, metadataReadRevision, stationPtr.Snapshot())
	}()
	result.Address = targetSnapshot.Address
	// The no-op shortcut must pass the same presence gate as the write path:
	// a station the fleet already considers absent would be rejected below,
	// so confirming it here without a write would report an inconsistent
	// outcome for the same state.
	if targetSnapshot.Channel == channel &&
		isOperationallyFresh(targetSnapshot.LastChannelReadAt, time.Now()) &&
		targetSnapshot.Present && targetSnapshot.MissedScans == 0 &&
		!targetSnapshot.PresenceUncertain &&
		isRecent(targetSnapshot.LastSeenAt, time.Now(), m.channelScanFreshnessWindowDuration()) {
		result.PreviousChannel = channel
		result.Channel = channel
		result.Confirmed = true
		return result, nil
	}
	if targetSnapshot.PowerState == bluetooth.PowerStateBooting &&
		isOperationallyFresh(targetSnapshot.LastPowerReadAt, time.Now()) {
		return result, fmt.Errorf(
			"station is booting; retry channel change after transition: %w",
			ErrStationTransitioning,
		)
	}
	if err := m.ensureReady(); err != nil {
		return result, err
	}
	if err := operationContext.Err(); err != nil {
		return result, m.stationOperationContextError(err)
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
	if !isRecent(targetSnapshot.LastSeenAt, time.Now(), m.channelScanFreshnessWindowDuration()) {
		return result, fmt.Errorf("%w before changing a channel", ErrScanRequired)
	}
	capabilities := targetSnapshot.Capabilities
	if !capabilities.ChannelRead || !capabilities.ChannelWrite {
		err = runSafely("channel capability refresh", func() error {
			discoveryContext, cancelDiscovery := context.WithTimeout(operationContext, m.initialReadTimeoutDuration())
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
			return result, m.stationOperationContextError(err)
		}
	}
	targetSnapshot = stationPtr.Snapshot()
	// Capability refresh is an awaited Bluetooth phase; a notification can
	// make the original non-booting snapshot obsolete before the channel write.
	if isFreshBootingPower(targetSnapshot, time.Now()) {
		return result, fmt.Errorf(
			"station is booting; retry channel change after transition: %w",
			ErrStationTransitioning,
		)
	}
	if !capabilities.ChannelRead || !capabilities.ChannelWrite {
		return result, fmt.Errorf("%w: safe channel changes require read and write support", ErrUnsupported)
	}
	targetSnapshot = stationPtr.Snapshot()
	result.Address = targetSnapshot.Address
	if !targetSnapshot.Present || targetSnapshot.MissedScans > 0 || targetSnapshot.PresenceUncertain {
		return result, fmt.Errorf("%w: station %s was not seen in the latest scan", ErrNotFound, address)
	}
	if !isRecent(targetSnapshot.LastSeenAt, time.Now(), m.channelScanFreshnessWindowDuration()) {
		return result, fmt.Errorf("%w before changing a channel", ErrScanRequired)
	}
	if targetSnapshot.Channel == channel && isOperationallyFresh(targetSnapshot.LastChannelReadAt, time.Now()) {
		result.PreviousChannel = channel
		result.Channel = channel
		result.Confirmed = true
		return result, nil
	}
	hasUnknown := false
	conflictCheckTime := time.Now()
	// Snapshot each other station outside the fleet lock: a wedged WinRT
	// cleanup on another station can hold its mutex far longer than this
	// operation's budget.
	fleetPointers := m.stationPointers()
	otherStations := make([]*bluetooth.BaseStation, 0, len(fleetPointers))
	for _, other := range fleetPointers {
		if other == stationPtr {
			continue
		}
		otherStations = append(otherStations, other)
	}
	for _, other := range otherStations {
		// Non-blocking snapshot: a station wedged inside a transport call must
		// not hang the conflict check (and with it the channel write). Its
		// channel cannot be verified while locked, so count it as unknown.
		snapshot, ok := other.SnapshotNonBlocking()
		if !ok {
			hasUnknown = true
			continue
		}
		if !snapshot.Present {
			continue
		}
		if snapshot.MissedScans > 0 || snapshot.PresenceUncertain ||
			!isRecent(snapshot.LastSeenAt, conflictCheckTime, m.channelScanFreshnessWindowDuration()) ||
			!isOperationallyFresh(snapshot.LastChannelReadAt, conflictCheckTime) {
			hasUnknown = true
			continue
		}
		if snapshot.Channel == bluetooth.ChannelUnknown {
			hasUnknown = true
			continue
		}
		if snapshot.Channel == channel {
			return result, fmt.Errorf("%w: channel %d is used by %s (%s)", ErrChannelConflict, channel, snapshot.Name, snapshot.Address)
		}
	}
	if hasUnknown {
		result.Warnings = append(result.Warnings, "One or more visible stations have an unknown channel; conflicts cannot be fully verified.")
		if !allowUnknownConflictRisk {
			return result, fmt.Errorf("%w: one or more visible stations have an unknown channel", ErrScanRequired)
		}
	}
	// The conflict check above can take several seconds; a boot transition
	// landing in that window must still block the write, matching the guard
	// before the capability refresh and the power paths' pre-write re-check.
	// Presence can also be reclassified inside the same window (the miss
	// threshold is applied from settings while no scan runs), so re-run the
	// entry presence gate instead of writing to a station the fleet already
	// considers absent.
	recheckSnapshot := stationPtr.Snapshot()
	if !recheckSnapshot.Present || recheckSnapshot.MissedScans > 0 || recheckSnapshot.PresenceUncertain {
		return result, fmt.Errorf("%w: station %s was not seen in the latest scan", ErrNotFound, address)
	}
	if isFreshBootingPower(recheckSnapshot, time.Now()) {
		return result, fmt.Errorf(
			"station is booting; retry channel change after transition: %w",
			ErrStationTransitioning,
		)
	}
	var writeResult bluetooth.ChannelWriteResult
	err = runSafely("channel operation", func() error {
		var channelErr error
		writeResult, channelErr = m.bluetoothOps.setChannel(operationContext, stationPtr, channel)
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
		// Normalize an expired operation budget or a shutdown cancellation the
		// same way the other single-station operations do, so Wails and HTTP
		// consumers observe the public sentinels instead of raw context
		// errors.
		return result, m.stationOperationContextError(err)
	}
	m.clearStatusFailureKind(canonicalAddress, statusRetryConnection|statusRetryChannel)
	return result, nil
}
