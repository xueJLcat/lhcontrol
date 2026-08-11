package station

import (
	"context"
	"errors"
	"fmt"
	"lhcontrol/internal/bluetooth"
	"sort"
	"sync"
	"time"
)

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
	if (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) &&
		(result.Cancelled || result.TimedOut) {
		// Cancellation and timeout are reported through the structured result,
		// keeping the legacy error contract consistent with the detailed form.
		err = nil
	}
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
	result, err := m.SetAllStationsPowerDetailedContext(context.Background(), state)
	if (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) &&
		(result.Cancelled || result.TimedOut) {
		return result, nil
	}
	return result, err
}

// SetAllStationsPowerDetailedContext applies a bulk power target while
// honoring caller cancellation as well as application shutdown.
func (m *Manager) SetAllStationsPowerDetailedContext(ctx context.Context, state string) (BulkPowerResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	busyTarget := ""
	if target, targetErr := bluetooth.ParsePowerTarget(state); targetErr == nil {
		busyTarget = target.String()
	}
	operationContext, cancelOperation := context.WithTimeout(ctx, m.config.BulkPowerTimeout())
	stopLifecycleCancellation := context.AfterFunc(m.lifecycleContext, cancelOperation)
	lifecycle := &bulkPowerLifecycle{cancel: cancelOperation, done: make(chan struct{})}
	m.bulkLifecycleMutex.Lock()
	if m.bulkLifecycle != nil {
		m.bulkLifecycleMutex.Unlock()
		stopLifecycleCancellation()
		cancelOperation()
		return BulkPowerResult{Target: busyTarget}, ErrOperationInProgress
	}
	m.bulkLifecycle = lifecycle
	m.bulkLifecycleMutex.Unlock()
	defer func() {
		stopLifecycleCancellation()
		cancelOperation()
		m.bulkLifecycleMutex.Lock()
		if m.bulkLifecycle == lifecycle {
			m.bulkLifecycle = nil
			close(lifecycle.done)
		}
		m.bulkLifecycleMutex.Unlock()
	}()
	result, err := m.setAllStationsPowerDetailed(operationContext, state)
	if contextErr := operationContext.Err(); contextErr != nil {
		result.Cancelled = true
		result.TimedOut = errors.Is(contextErr, context.DeadlineExceeded)
		reason := ReasonOperationCancelled
		if result.TimedOut {
			reason = ReasonBulkOperationTimeout
		} else if m.shuttingDown.Load() {
			reason = ReasonShuttingDown
		}
		// Cancellation can occur before worker goroutines start (for example
		// while the adapter is being initialized) or before candidates are
		// enumerated at all. Backfill the known fleet in that case and then
		// complete every untouched entry so callers never receive ambiguous
		// zero-value station results or an empty result list.
		if len(result.Results) == 0 {
			for _, info := range m.GetStationInfo() {
				result.Results = append(result.Results, BulkPowerStationResult{Address: info.Address, Name: info.Name})
			}
		}
		for index := range result.Results {
			item := &result.Results[index]
			if !item.Success && !item.Skipped && !item.CommandSent && item.Error == "" {
				item.Skipped = true
				item.Reason = reason
			}
		}
		if err == nil {
			err = contextErr
		}
		if result.TimedOut {
			err = fmt.Errorf("%w: %w", ErrBulkOperationTimeout, err)
		}
	}
	return result, err
}

// bulkCancelWaitLimit bounds the wait for an already-cancelled bulk operation.
// Workers observe the cancelled context everywhere except adapter calls that
// lack context support, which could otherwise block the caller indefinitely.
const bulkCancelWaitLimit = 60 * time.Second

func (m *Manager) CancelBulkPower() error {
	m.bulkLifecycleMutex.Lock()
	lifecycle := m.bulkLifecycle
	if lifecycle != nil {
		lifecycle.cancel()
	}
	m.bulkLifecycleMutex.Unlock()
	if lifecycle == nil {
		return nil
	}
	select {
	case <-lifecycle.done:
		return nil
	case <-m.shutdownCh:
		return ErrShuttingDown
	case <-time.After(bulkCancelWaitLimit):
		return fmt.Errorf("bulk operation did not stop within %s after cancellation", bulkCancelWaitLimit)
	}
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
			stationResult.Reason = ReasonStationBooting
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
		for index := range result.Results {
			item := &result.Results[index]
			if !item.Success && !item.Skipped && !item.CommandSent && item.Error == "" {
				item.Error = err.Error()
			}
		}
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
					result.Results[resultIndex].Reason = ReasonShuttingDown
				} else if errors.Is(ctx.Err(), context.DeadlineExceeded) {
					result.Results[resultIndex].Reason = ReasonBulkOperationTimeout
				} else {
					result.Results[resultIndex].Reason = ReasonOperationCancelled
				}
				return
			case <-m.shutdownCh:
				result.Results[resultIndex].Skipped = true
				result.Results[resultIndex].Reason = ReasonShuttingDown
				return
			}
			defer func() { <-semaphore }()
			operationContext, cancelOperation := m.newStationOperationContext(ctx)
			defer cancelOperation()
			if m.shuttingDown.Load() || ctx.Err() != nil {
				result.Results[resultIndex].Skipped = true
				if m.shuttingDown.Load() {
					result.Results[resultIndex].Reason = ReasonShuttingDown
				} else if errors.Is(ctx.Err(), context.DeadlineExceeded) {
					result.Results[resultIndex].Reason = ReasonBulkOperationTimeout
				} else {
					result.Results[resultIndex].Reason = ReasonOperationCancelled
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
				disposition := classifyCachedPower(snapshot, target, time.Now())
				if disposition == cachedPowerBooting {
					cachedSkip = true
					stationResult.Skipped = true
					stationResult.Reason = ReasonStationBooting
					return nil
				}
				if disposition == cachedPowerAtTarget || !isOperationallyFresh(snapshot.LastPowerReadAt, time.Now()) {
					readContext, cancelRead := context.WithTimeout(operationContext, m.initialReadTimeoutDuration())
					readErr := m.bluetoothOps.fetchInitialPowerState(readContext, s)
					cancelRead()
					readSucceeded := powerReadSucceeded(readErr)
					verifiedDisposition := cachedPowerActionable
					if readSucceeded {
						// Classify before recovery bookkeeping: a transport-level channel
						// error may disconnect the station and intentionally clear freshness,
						// but it cannot invalidate the power value read just beforehand.
						verifiedDisposition = classifyCachedPower(s.Snapshot(), target, time.Now())
					}
					m.recordPowerVerificationResult(s, stationResult.Address, snapshot, readErr)
					if readSucceeded {
						switch verifiedDisposition {
						case cachedPowerBooting:
							cachedSkip = true
							stationResult.Skipped = true
							stationResult.Reason = ReasonStationBooting
							return nil
						case cachedPowerAtTarget:
							cachedSkip = true
							stationResult.Skipped = true
							stationResult.Success = true
							stationResult.Confirmed = true
							stationResult.Reason = ReasonAlreadyAtTarget
							return nil
						}
					}
				}
				snapshot = s.Snapshot()
				capabilities := snapshot.Capabilities
				var err error
				discoveryContext, cancelDiscovery := context.WithTimeout(operationContext, m.initialReadTimeoutDuration())
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
					stationResult.Reason = ReasonUnsupportedCapability
					return nil
				}
				if err == nil && target == bluetooth.PowerStateStandby && !capabilities.Standby {
					stationResult.Skipped = true
					stationResult.Reason = ReasonUnsupportedStandby
					return nil
				}
				if err != nil {
					return err
				}
				var controlResult bluetooth.PowerControlResult
				controlResult, err = m.bluetoothOps.setPowerState(operationContext, s, target)
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
				} else if errors.Is(workerErr, context.Canceled) || errors.Is(workerErr, context.DeadlineExceeded) {
					stationResult.Skipped = true
					if m.shuttingDown.Load() {
						stationResult.Reason = ReasonShuttingDown
					} else if errors.Is(ctx.Err(), context.DeadlineExceeded) {
						stationResult.Reason = ReasonBulkOperationTimeout
					} else if errors.Is(workerErr, context.DeadlineExceeded) {
						// Only this station's own budget expired; the bulk
						// deadline is still pending, so report the station-level
						// timeout instead of blaming the whole batch.
						stationResult.Reason = ReasonStationOperationTimeout
					} else {
						stationResult.Reason = ReasonOperationCancelled
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
