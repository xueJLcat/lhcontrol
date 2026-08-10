package station

import (
	"context"
	"errors"
	"fmt"
	"lhcontrol/internal/bluetooth"
	"time"
)

func (m *Manager) SetStationPower(address, state string) (PowerActionResult, error) {
	target, err := bluetooth.ParsePowerTarget(state)
	if err != nil {
		return PowerActionResult{}, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	stationPtr, err := m.stationByAddress(address)
	if err != nil {
		return PowerActionResult{}, err
	}
	if m.shuttingDown.Load() {
		return PowerActionResult{}, ErrShuttingDown
	}
	canonicalAddress := stationPtr.Snapshot().Address
	if classifyCachedPower(stationPtr.Snapshot(), target, time.Now()) == cachedPowerBooting {
		return PowerActionResult{}, fmt.Errorf("station is booting; retry after transition: %w", ErrStationTransitioning)
	}
	operationContext, cancelOperation := m.newStationOperationContext(m.lifecycleContext)
	defer cancelOperation()
	if err := m.beginForegroundStationOperationContext(operationContext, canonicalAddress); err != nil {
		return PowerActionResult{}, stationOperationContextError(err)
	}
	defer m.endStationOperation(canonicalAddress)
	snapshot := stationPtr.Snapshot()
	if err := m.ensureReady(); err != nil {
		return PowerActionResult{}, err
	}
	if err := operationContext.Err(); err != nil {
		return PowerActionResult{}, stationOperationContextError(err)
	}
	// A display can legitimately remain fresh longer than the fixed write
	// safety window. Always attempt a current read before acting on an expired
	// power snapshot; a successful target-state read turns the request into a
	// confirmed no-op instead of sending a duplicate command.
	disposition := classifyCachedPower(snapshot, target, time.Now())
	if disposition == cachedPowerBooting {
		// The state observed before the operation lock was acquired can change
		// while background reads drain; a transition that lands after begin must
		// still protect the station, matching the bulk re-check in the workers.
		return PowerActionResult{}, fmt.Errorf("station is booting; retry after transition: %w", ErrStationTransitioning)
	}
	if disposition == cachedPowerAtTarget || !isOperationallyFresh(snapshot.LastPowerReadAt, time.Now()) {
		var readErr error
		readErr = runSafely("power cache verification", func() error {
			readContext, cancelRead := context.WithTimeout(operationContext, m.initialReadTimeoutDuration())
			defer cancelRead()
			return m.bluetoothOps.fetchInitialPowerState(readContext, stationPtr)
		})
		if powerReadSucceeded(readErr) {
			// A completed, authoritative power observation remains usable even if
			// the independent optional channel read consumed the final operation
			// budget. Resolve target/booting outcomes before rejecting the expired
			// context, but never continue to discovery or a write after expiry.
			if result, outcomeErr, handled := m.cachedPowerOutcome(stationPtr, target); handled {
				m.recordPartialPowerVerificationResult(stationPtr, canonicalAddress, readErr)
				if outcomeErr == nil {
					// Recording a transport-level channel failure can disconnect the
					// station. Return the post-recovery snapshot while retaining the
					// already-established confirmed no-op outcome.
					info, infoErr := m.stationInfoByAddress(address)
					if infoErr != nil {
						return PowerActionResult{}, infoErr
					}
					result.Station = info
				}
				return result, outcomeErr
			}
			m.recordPartialPowerVerificationResult(stationPtr, canonicalAddress, readErr)
		}
		if err := operationContext.Err(); err != nil {
			return PowerActionResult{}, stationOperationContextError(err)
		}
		snapshot = stationPtr.Snapshot()
	}
	capabilities := snapshot.Capabilities
	if !snapshot.CapabilitiesKnown ||
		!capabilities.PowerWrite ||
		(target == bluetooth.PowerStateStandby && !capabilities.Standby) {
		err = runSafely("power capability refresh", func() error {
			discoveryContext, cancelDiscovery := context.WithTimeout(operationContext, m.initialReadTimeoutDuration())
			defer cancelDiscovery()
			var refreshErr error
			if snapshot.CapabilitiesKnown {
				capabilities, refreshErr = m.bluetoothOps.refreshCapabilities(discoveryContext, stationPtr)
			} else {
				capabilities, refreshErr = m.bluetoothOps.ensureCapabilities(discoveryContext, stationPtr)
			}
			return refreshErr
		})
		if err != nil {
			if m.shuttingDown.Load() && errors.Is(err, context.Canceled) {
				return PowerActionResult{}, ErrShuttingDown
			}
			m.observeStationBluetoothError(stationPtr, canonicalAddress, err)
			return PowerActionResult{}, err
		}
	}
	if !capabilities.PowerWrite {
		return PowerActionResult{}, fmt.Errorf("%w: power write is unavailable", ErrUnsupported)
	}
	if target == bluetooth.PowerStateStandby && !capabilities.Standby {
		return PowerActionResult{}, fmt.Errorf("%w: standby is unavailable", ErrUnsupported)
	}
	var controlResult bluetooth.PowerControlResult
	err = runSafely("power operation", func() error {
		var controlErr error
		controlResult, controlErr = m.bluetoothOps.setPowerState(operationContext, stationPtr, target)
		return controlErr
	})
	if err != nil {
		m.observeStationBluetoothError(stationPtr, canonicalAddress, err)
		var confirmationErr *bluetooth.PowerConfirmationError
		if errors.As(err, &confirmationErr) {
			if !bluetooth.RequiresReconnect(err) && !bluetooth.IsAdapterUnavailable(err) {
				m.clearStatusFailureKind(canonicalAddress, statusRetryConnection)
			}
			info, infoErr := m.stationInfoByAddress(address)
			if infoErr == nil {
				return PowerActionResult{
					Station:           info,
					CommandSent:       true,
					Confirmed:         false,
					ConfirmationError: err.Error(),
				}, err
			}
		}
		if m.shuttingDown.Load() && errors.Is(err, context.Canceled) {
			return PowerActionResult{}, ErrShuttingDown
		}
		if bluetooth.IsUnsupportedCapabilityError(err) {
			return PowerActionResult{}, fmt.Errorf("%w: %v", ErrUnsupported, err)
		}
		return PowerActionResult{}, stationOperationContextError(err)
	}
	info, err := m.stationInfoByAddress(address)
	if err != nil {
		return PowerActionResult{}, err
	}
	m.clearStatusFailureKind(canonicalAddress, statusRetryConnection)
	return PowerActionResult{Station: info, CommandSent: true, Confirmed: controlResult.Confirmed}, nil
}
