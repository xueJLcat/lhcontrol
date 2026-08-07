package station

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"lhcontrol/internal/bluetooth"
)

func (m *Manager) PowerOnStation(address string) error {
	_, err := m.SetStationPower(address, "on")
	return err
}

func (m *Manager) PowerOffStation(address string) error {
	_, err := m.SetStationPower(address, "sleep")
	return err
}

func (m *Manager) stationByAddress(address string) (*bluetooth.BaseStation, error) {
	m.stationsMutex.RLock()
	stationPtr, ok := m.stations[address]
	if !ok {
		for stationAddress, candidate := range m.stations {
			if strings.EqualFold(stationAddress, address) {
				stationPtr, ok = candidate, true
				break
			}
		}
	}
	m.stationsMutex.RUnlock()
	if !ok || stationPtr == nil {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, address)
	}
	return stationPtr, nil
}

func (m *Manager) stationInfoByAddress(address string) (StationInfo, error) {
	for _, info := range m.GetStationInfo() {
		if strings.EqualFold(info.Address, address) {
			return info, nil
		}
	}
	return StationInfo{}, fmt.Errorf("%w: %s", ErrNotFound, address)
}

// SetStationPower sets one of the three stable target states. Confirmed is
// false when the firmware supports writing but does not expose power reads.
func (m *Manager) cachedPowerOutcome(stationPtr *bluetooth.BaseStation, target bluetooth.PowerState) (PowerActionResult, error, bool) {
	snapshot := stationPtr.Snapshot()
	switch classifyCachedPower(snapshot, target, time.Now()) {
	case cachedPowerBooting:
		return PowerActionResult{}, fmt.Errorf("station is booting; retry after transition: %w", ErrStationTransitioning), true
	case cachedPowerActionable:
		return PowerActionResult{}, nil, false
	}
	info, err := m.stationInfoByAddress(snapshot.Address)
	if err != nil {
		return PowerActionResult{}, err, true
	}
	return PowerActionResult{
		Station:   info,
		Skipped:   true,
		Reason:    "already at target state",
		Confirmed: true,
	}, nil, true
}

type cachedPowerDisposition uint8

const (
	cachedPowerActionable cachedPowerDisposition = iota
	cachedPowerBooting
	cachedPowerAtTarget
)

func classifyCachedPower(
	snapshot bluetooth.BaseStationSnapshot,
	target bluetooth.PowerState,
	now time.Time,
) cachedPowerDisposition {
	if !isFresh(snapshot.LastPowerReadAt, now) {
		return cachedPowerActionable
	}
	if snapshot.PowerState == bluetooth.PowerStateBooting {
		return cachedPowerBooting
	}
	if snapshot.PowerState == target &&
		bluetooth.IsPowerStateVerified(snapshot.PowerState, snapshot.RawPowerState) {
		return cachedPowerAtTarget
	}
	return cachedPowerActionable
}

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
	if result, outcomeErr, handled := m.cachedPowerOutcome(stationPtr, target); handled {
		return result, outcomeErr
	}
	if err := m.beginForegroundStationOperation(canonicalAddress); err != nil {
		return PowerActionResult{}, err
	}
	defer m.endStationOperation(canonicalAddress)
	if result, outcomeErr, handled := m.cachedPowerOutcome(stationPtr, target); handled {
		return result, outcomeErr
	}
	snapshot := stationPtr.Snapshot()
	if err := m.ensureReady(); err != nil {
		return PowerActionResult{}, err
	}
	capabilities := snapshot.Capabilities
	if !snapshot.CapabilitiesKnown ||
		!capabilities.PowerWrite ||
		(target == bluetooth.PowerStateStandby && !capabilities.Standby) {
		err = runSafely("power capability refresh", func() error {
			discoveryContext, cancelDiscovery := context.WithTimeout(m.lifecycleContext, m.initialReadTimeout)
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
		controlResult, controlErr = m.bluetoothOps.setPowerState(m.lifecycleContext, stationPtr, target)
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
		return PowerActionResult{}, err
	}
	info, err := m.stationInfoByAddress(address)
	if err != nil {
		return PowerActionResult{}, err
	}
	m.clearStatusFailureKind(canonicalAddress, statusRetryConnection)
	return PowerActionResult{Station: info, CommandSent: true, Confirmed: controlResult.Confirmed}, nil
}

func (m *Manager) IdentifyStation(address string) error {
	stationPtr, err := m.stationByAddress(address)
	if err != nil {
		return err
	}
	canonicalAddress := stationPtr.Snapshot().Address
	if err := m.beginForegroundStationOperation(canonicalAddress); err != nil {
		return err
	}
	defer m.endStationOperation(canonicalAddress)
	if err := m.ensureReady(); err != nil {
		return err
	}
	capabilities := stationPtr.Snapshot().Capabilities
	if !capabilities.Identify {
		err = runSafely("identify capability refresh", func() error {
			discoveryContext, cancelDiscovery := context.WithTimeout(m.lifecycleContext, m.initialReadTimeout)
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
		return fmt.Errorf("%w: identify is unavailable", ErrUnsupported)
	}
	err = runSafely("identify operation", func() error {
		return m.bluetoothOps.identify(m.lifecycleContext, stationPtr)
	})
	if bluetooth.IsUnsupportedCapabilityError(err) {
		return fmt.Errorf("%w: %v", ErrUnsupported, err)
	}
	if err != nil {
		if m.shuttingDown.Load() && errors.Is(err, context.Canceled) {
			return ErrShuttingDown
		}
		return m.observeStationBluetoothError(stationPtr, canonicalAddress, err)
	}
	m.clearStatusFailureKind(canonicalAddress, statusRetryConnection)
	return nil
}

func (m *Manager) RefreshStationCapabilities(address string) (StationInfo, error) {
	stationPtr, err := m.stationByAddress(address)
	if err != nil {
		return StationInfo{}, err
	}
	canonicalAddress := stationPtr.Snapshot().Address
	if err := m.beginForegroundStationOperation(canonicalAddress); err != nil {
		return StationInfo{}, err
	}
	defer m.endStationOperation(canonicalAddress)
	if err := m.ensureReady(); err != nil {
		return StationInfo{}, err
	}
	if err := runSafely("capability refresh", func() error {
		discoveryContext, cancelDiscovery := context.WithTimeout(m.lifecycleContext, m.initialReadTimeout)
		defer cancelDiscovery()
		_, refreshErr := m.bluetoothOps.refreshCapabilities(discoveryContext, stationPtr)
		return refreshErr
	}); err != nil {
		if m.shuttingDown.Load() && errors.Is(err, context.Canceled) {
			return StationInfo{}, ErrShuttingDown
		}
		m.observeStationBluetoothError(stationPtr, canonicalAddress, err)
		return StationInfo{}, err
	}
	if err := runSafely("capability refresh state read", func() error {
		readContext, cancelRead := context.WithTimeout(m.lifecycleContext, m.initialReadTimeout)
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
			return StationInfo{}, err
		}
		m.recordStructuredReadResult(stationPtr, canonicalAddress, readErr.Power, readErr.Channel)
		m.recordMetadataReadResult(canonicalAddress, readErr.Metadata)
		// Capability discovery succeeded. Keep the refreshed station visible and
		// expose any unavailable state values through freshness and LastError
		// instead of turning a structured partial read into a total refresh
		// failure.
		return m.stationInfoByAddress(address)
	}
	m.clearStatusFailure(canonicalAddress)
	return m.stationInfoByAddress(address)
}

func (m *Manager) PowerOnAllStations() error {
	return m.setAllStationsPower("on")
}

func (m *Manager) PowerOffAllStations() error {
	return m.setAllStationsPower("sleep")
}

// SetAllStationsPower applies one stable target to known, writable stations.
// Stations already at the target and stations currently booting are skipped.
func (m *Manager) SetAllStationsPower(state string) error {
	return m.setAllStationsPower(state)
}

func (m *Manager) setAllStationsPower(state string) error {
	result, err := m.SetAllStationsPowerDetailedContext(context.Background(), state)
	if err != nil {
		return err
	}
	var operationErrors []error
	for _, stationResult := range result.Results {
		if !stationResult.Success && !stationResult.Skipped {
			operationErrors = append(operationErrors, fmt.Errorf("%s: %s", stationResult.Address, stationResult.Error))
		}
	}
	if len(operationErrors) > 0 {
		return fmt.Errorf("failed to set one or more stations to %s: %w", result.Target, errors.Join(operationErrors...))
	}
	return nil
}

// SetAllStationsPowerDetailed returns one result per known station.
// Per-device failures are data, not a top-level error, so Wails callers retain
// successful results when only part of a batch fails.
func (m *Manager) SetAllStationsPowerDetailed(state string) (BulkPowerResult, error) {
	return m.SetAllStationsPowerDetailedContext(context.Background(), state)
}

// SetAllStationsPowerDetailedContext applies a bulk power target while
// honoring caller cancellation as well as application shutdown.
func (m *Manager) SetAllStationsPowerDetailedContext(ctx context.Context, state string) (BulkPowerResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	operationContext, cancelOperation := context.WithCancel(ctx)
	stopLifecycleCancellation := context.AfterFunc(m.lifecycleContext, cancelOperation)
	defer func() {
		stopLifecycleCancellation()
		cancelOperation()
	}()
	return m.setAllStationsPowerDetailed(operationContext, state)
}

func (m *Manager) cachedBulkPowerResult(target bluetooth.PowerState) (BulkPowerResult, bool) {
	result := BulkPowerResult{Target: target.String(), Results: []BulkPowerStationResult{}}
	for _, info := range m.GetStationInfo() {
		item := BulkPowerStationResult{Address: info.Address, Name: info.Name, Station: info}
		switch classifyCachedPowerInfo(info, target) {
		case cachedPowerBooting:
			item.Skipped = true
			item.Reason = "station is booting"
		case cachedPowerAtTarget:
			item.Skipped = true
			item.Success = true
			item.Confirmed = true
			item.Reason = "already at target state"
		default:
			return BulkPowerResult{}, false
		}
		result.Results = append(result.Results, item)
	}
	return result, true
}

func classifyCachedPowerInfo(info StationInfo, target bluetooth.PowerState) cachedPowerDisposition {
	if !info.PowerFresh {
		return cachedPowerActionable
	}
	if bluetooth.PowerState(info.PowerState) == bluetooth.PowerStateBooting {
		return cachedPowerBooting
	}
	if info.PowerStateConfirmed && bluetooth.PowerState(info.PowerState) == target {
		return cachedPowerAtTarget
	}
	return cachedPowerActionable
}

func (m *Manager) setAllStationsPowerDetailed(ctx context.Context, state string) (BulkPowerResult, error) {
	target, err := bluetooth.ParsePowerTarget(state)
	result := BulkPowerResult{Target: target.String(), Results: []BulkPowerStationResult{}}
	if err != nil {
		return result, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	if m.shuttingDown.Load() {
		return result, ErrShuttingDown
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if cached, complete := m.cachedBulkPowerResult(target); complete {
		return cached, nil
	}
	if err := m.beginBulkGlobalOperationContext(ctx); err != nil {
		return result, err
	}
	defer m.endForegroundGlobalOperation()

	type bulkPowerWork struct {
		station     *bluetooth.BaseStation
		resultIndex int
	}
	type bulkPowerCandidate struct {
		station  *bluetooth.BaseStation
		snapshot bluetooth.BaseStationSnapshot
		name     string
	}
	selectionTime := time.Now()
	m.stationsMutex.RLock()
	candidates := make([]bulkPowerCandidate, 0, len(m.stations))
	for _, stationPtr := range m.stations {
		if stationPtr == nil {
			continue
		}
		snapshot := stationPtr.Snapshot()
		name := snapshot.Name
		if renamedName, ok := m.config.GetStationDisplayName(snapshot.Address, snapshot.Name); ok {
			name = renamedName
		}
		candidates = append(candidates, bulkPowerCandidate{station: stationPtr, snapshot: snapshot, name: name})
	}
	m.stationsMutex.RUnlock()
	sort.Slice(candidates, func(i, j int) bool {
		left, right := candidates[i], candidates[j]
		return stationValuesLess(
			left.snapshot.Channel, left.name, left.snapshot.Address,
			right.snapshot.Channel, right.name, right.snapshot.Address,
		)
	})

	work := make([]bulkPowerWork, 0, len(candidates))
	for _, candidate := range candidates {
		stationPtr := candidate.station
		snapshot := candidate.snapshot
		name := candidate.name
		stationResult := BulkPowerStationResult{Address: snapshot.Address, Name: name}
		switch classifyCachedPower(snapshot, target, selectionTime) {
		case cachedPowerBooting:
			stationResult.Skipped = true
			stationResult.Reason = "station is booting"
		case cachedPowerAtTarget:
			stationResult.Skipped = true
			stationResult.Success = true
			stationResult.Confirmed = true
			stationResult.Reason = "already at target state"
		}
		result.Results = append(result.Results, stationResult)
		resultIndex := len(result.Results) - 1
		if !stationResult.Skipped {
			work = append(work, bulkPowerWork{station: stationPtr, resultIndex: resultIndex})
		}
	}

	infoByAddress := make(map[string]StationInfo, len(result.Results))
	for _, info := range m.GetStationInfo() {
		infoByAddress[info.Address] = info
	}
	for index := range result.Results {
		if info, ok := infoByAddress[result.Results[index].Address]; ok {
			result.Results[index].Station = info
			result.Results[index].Name = info.Name
		}
	}
	if len(work) == 0 {
		return result, nil
	}
	if err := m.ensureReady(); err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}

	var wg sync.WaitGroup
	semaphore := make(chan struct{}, 2)

	for _, item := range work {
		wg.Add(1)
		go func(resultIndex int, s *bluetooth.BaseStation) {
			defer wg.Done()
			select {
			case semaphore <- struct{}{}:
			case <-ctx.Done():
				result.Results[resultIndex].Skipped = true
				if m.shuttingDown.Load() {
					result.Results[resultIndex].Reason = "application is shutting down"
				} else {
					result.Results[resultIndex].Reason = "operation cancelled"
				}
				return
			case <-m.shutdownCh:
				result.Results[resultIndex].Skipped = true
				result.Results[resultIndex].Reason = "application is shutting down"
				return
			}
			defer func() { <-semaphore }()
			if m.shuttingDown.Load() || ctx.Err() != nil {
				result.Results[resultIndex].Skipped = true
				if m.shuttingDown.Load() {
					result.Results[resultIndex].Reason = "application is shutting down"
				} else {
					result.Results[resultIndex].Reason = "operation cancelled"
				}
				return
			}
			stationResult := BulkPowerStationResult{
				Address: s.Address.String(),
			}
			cachedSkip := false
			workerErr := runSafely("bulk power worker", func() error {
				snapshot := s.Snapshot()
				stationResult.Address = snapshot.Address
				stationResult.Name = snapshot.Name
				switch classifyCachedPower(snapshot, target, time.Now()) {
				case cachedPowerBooting:
					cachedSkip = true
					stationResult.Skipped = true
					stationResult.Reason = "station is booting"
					return nil
				case cachedPowerAtTarget:
					cachedSkip = true
					stationResult.Skipped = true
					stationResult.Success = true
					stationResult.Confirmed = true
					stationResult.Reason = "already at target state"
					return nil
				}
				capabilities := snapshot.Capabilities
				var err error
				discoveryContext, cancelDiscovery := context.WithTimeout(ctx, m.initialReadTimeout)
				defer cancelDiscovery()
				if snapshot.CapabilitiesKnown &&
					(!capabilities.PowerWrite ||
						(target == bluetooth.PowerStateStandby && !capabilities.Standby)) {
					capabilities, err = m.bluetoothOps.refreshCapabilities(discoveryContext, s)
				} else if !snapshot.CapabilitiesKnown {
					capabilities, err = m.bluetoothOps.ensureCapabilities(discoveryContext, s)
				}
				if err == nil && !capabilities.PowerWrite {
					stationResult.Skipped = true
					stationResult.Reason = "power control is not supported"
					return nil
				}
				if err == nil && target == bluetooth.PowerStateStandby && !capabilities.Standby {
					stationResult.Skipped = true
					stationResult.Reason = "standby is not supported"
					return nil
				}
				if err != nil {
					return err
				}
				var controlResult bluetooth.PowerControlResult
				controlResult, err = m.bluetoothOps.setPowerState(ctx, s, target)
				stationResult.CommandSent = err == nil
				stationResult.Confirmed = controlResult.Confirmed
				if err == nil {
					stationResult.Success = true
				}
				return err
			})
			if workerErr != nil {
				var confirmationErr *bluetooth.PowerConfirmationError
				if errors.As(workerErr, &confirmationErr) {
					// A possibly-sent command keeps its command-sent outcome even
					// when shutdown cancelled the confirmation read.
					m.observeStationBluetoothError(s, stationResult.Address, workerErr)
					stationResult.CommandSent = true
					stationResult.Success = true
					stationResult.Confirmed = false
					stationResult.Error = workerErr.Error()
				} else if errors.Is(workerErr, context.Canceled) {
					stationResult.Skipped = true
					if m.shuttingDown.Load() {
						stationResult.Reason = "application is shutting down"
					} else {
						stationResult.Reason = "operation cancelled"
					}
					stationResult.CommandSent = false
					stationResult.Error = ""
					if info, infoErr := m.stationInfoByAddress(stationResult.Address); infoErr == nil {
						stationResult.Station = info
						stationResult.Name = info.Name
					}
					result.Results[resultIndex] = stationResult
					return
				} else {
					m.observeStationBluetoothError(s, stationResult.Address, workerErr)
					if bluetooth.IsUnsupportedCapabilityError(workerErr) {
						stationResult.Skipped = true
						stationResult.Reason = workerErr.Error()
					}
					if !stationResult.Skipped {
						stationResult.Error = workerErr.Error()
					}
				}
			}
			if info, infoErr := m.stationInfoByAddress(stationResult.Address); infoErr == nil {
				stationResult.Station = info
				stationResult.Name = info.Name
			}
			communicationSucceeded := !cachedSkip && (workerErr == nil || stationResult.CommandSent)
			if communicationSucceeded && !bluetooth.RequiresReconnect(workerErr) &&
				!bluetooth.IsAdapterUnavailable(workerErr) {
				m.clearStatusFailureKind(stationResult.Address, statusRetryConnection)
			}
			result.Results[resultIndex] = stationResult
		}(item.resultIndex, item.station)
	}

	wg.Wait()
	if err := ctx.Err(); err != nil {
		if m.shuttingDown.Load() {
			return result, nil
		}
		return result, err
	}
	return result, nil
}
