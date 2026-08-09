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
	initialSnapshot := stationPtr.Snapshot()
	if initialSnapshot.Channel == channel && isOperationallyFresh(initialSnapshot.LastChannelReadAt, time.Now()) {
		return ChannelChangeResult{
			Address: initialSnapshot.Address, PreviousChannel: channel, Channel: channel, Confirmed: true, Warnings: []string{},
		}, nil
	}
	operationContext, cancelOperation := m.newStationOperationContext(m.lifecycleContext)
	defer cancelOperation()
	if err := m.beginForegroundStationOperationContext(operationContext, canonicalAddress); err != nil {
		return result, stationOperationContextError(err)
	}
	defer m.endStationOperation(canonicalAddress)
	targetSnapshot := stationPtr.Snapshot()
	result.Address = targetSnapshot.Address
	if targetSnapshot.Channel == channel &&
		isOperationallyFresh(targetSnapshot.LastChannelReadAt, time.Now()) {
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
		return result, stationOperationContextError(err)
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
		return result, err
	}
	m.clearStatusFailureKind(canonicalAddress, statusRetryConnection|statusRetryChannel)
	return result, nil
}
