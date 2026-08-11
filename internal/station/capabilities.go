package station

import (
	"context"
	"errors"
	"fmt"
	"lhcontrol/internal/bluetooth"
)

func (m *Manager) IdentifyStation(address string) error {
	stationPtr, err := m.stationByAddress(address)
	if err != nil {
		return err
	}
	if m.shuttingDown.Load() {
		return ErrShuttingDown
	}
	canonicalAddress := stationPtr.Snapshot().Address
	operationContext, cancelOperation := m.newStationOperationContext(m.lifecycleContext)
	defer cancelOperation()
	if err := m.beginForegroundStationOperationContext(operationContext, canonicalAddress); err != nil {
		return stationOperationContextError(err)
	}
	defer m.endStationOperation(canonicalAddress)
	if err := m.ensureReady(); err != nil {
		return err
	}
	if err := operationContext.Err(); err != nil {
		return stationOperationContextError(err)
	}
	capabilities := stationPtr.Snapshot().Capabilities
	if !capabilities.Identify {
		err = runSafely("identify capability refresh", func() error {
			discoveryContext, cancelDiscovery := context.WithTimeout(operationContext, m.initialReadTimeoutDuration())
			defer cancelDiscovery()
			var refreshErr error
			capabilities, refreshErr = m.bluetoothOps.refreshCapabilities(discoveryContext, stationPtr)
			return refreshErr
		})
		if err != nil {
			if m.shuttingDown.Load() && errors.Is(err, context.Canceled) {
				return ErrShuttingDown
			}
			m.observeStationBluetoothError(stationPtr, canonicalAddress, err)
			return err
		}
	}
	if !capabilities.Identify {
		m.clearStatusFailureKind(canonicalAddress, statusRetryConnection)
		return fmt.Errorf("%w: identify is unavailable", ErrUnsupported)
	}
	err = runSafely("identify operation", func() error {
		return m.bluetoothOps.identify(operationContext, stationPtr)
	})
	if bluetooth.IsUnsupportedCapabilityError(err) {
		m.observeStationBluetoothError(stationPtr, canonicalAddress, err)
		return fmt.Errorf("%w: %v", ErrUnsupported, err)
	}
	if err != nil {
		if m.shuttingDown.Load() && errors.Is(err, context.Canceled) {
			return ErrShuttingDown
		}
		return stationOperationContextError(m.observeStationBluetoothError(stationPtr, canonicalAddress, err))
	}
	m.clearStatusFailureKind(canonicalAddress, statusRetryConnection)
	return nil
}

func (m *Manager) RefreshStationCapabilities(address string) (StationInfo, error) {
	stationPtr, err := m.stationByAddress(address)
	if err != nil {
		return StationInfo{}, err
	}
	if m.shuttingDown.Load() {
		return StationInfo{}, ErrShuttingDown
	}
	canonicalAddress := stationPtr.Snapshot().Address
	operationContext, cancelOperation := m.newStationOperationContext(m.lifecycleContext)
	defer cancelOperation()
	if err := m.beginForegroundStationOperationContext(operationContext, canonicalAddress); err != nil {
		return StationInfo{}, stationOperationContextError(err)
	}
	defer m.endStationOperation(canonicalAddress)
	if err := m.ensureReady(); err != nil {
		return StationInfo{}, err
	}
	if err := operationContext.Err(); err != nil {
		return StationInfo{}, stationOperationContextError(err)
	}
	if err := runSafely("capability refresh", func() error {
		discoveryContext, cancelDiscovery := context.WithTimeout(operationContext, m.initialReadTimeoutDuration())
		defer cancelDiscovery()
		_, refreshErr := m.bluetoothOps.refreshCapabilities(discoveryContext, stationPtr)
		return refreshErr
	}); err != nil {
		if m.shuttingDown.Load() && errors.Is(err, context.Canceled) {
			return StationInfo{}, ErrShuttingDown
		}
		m.observeStationBluetoothError(stationPtr, canonicalAddress, err)
		return StationInfo{}, stationOperationContextError(err)
	}
	if err := runSafely("capability refresh state read", func() error {
		readContext, cancelRead := context.WithTimeout(operationContext, m.initialReadTimeoutDuration())
		defer cancelRead()
		return m.bluetoothOps.fetchInitialPowerState(readContext, stationPtr)
	}); err != nil {
		if m.shuttingDown.Load() && errors.Is(err, context.Canceled) {
			return StationInfo{}, ErrShuttingDown
		}
		m.observeBluetoothError(err)
		var readErr *bluetooth.InitialReadError
		if !errors.As(err, &readErr) {
			m.observeStationBluetoothError(stationPtr, canonicalAddress, err)
			return StationInfo{}, stationOperationContextError(err)
		}
		m.recordStructuredReadResult(stationPtr, canonicalAddress, readErr.Power, readErr.Channel)
		m.recordMetadataReadResult(canonicalAddress, readErr.Metadata)
		// Capability discovery succeeded. Keep the refreshed station visible and
		// expose any unavailable state values through freshness and LastError
		// instead of turning a structured partial read into a total refresh
		// failure.
		return m.stationInfoByAddress(address)
	}
	// A complete rediscovery and state read resolves any error left by an older
	// foreground command. Keep partial reads conservative because they may not
	// have observed the field affected by that command.
	stationPtr.ClearOperationError()
	m.clearStatusFailure(canonicalAddress)
	return m.stationInfoByAddress(address)
}
