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
	if err != nil {
		// Cancellation and timeout are carried by structured fields in the
		// detailed form, but the legacy error contract has no such channel:
		// swallowing them here would report a batch where every station was
		// skipped as a success.
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
	// A shutdown that interrupted a batch mid-flight has two shapes whose
	// top-level error depends only on when the shutdown's AfterFunc
	// cancellation reached the bulk context — a race callers cannot act on.
	// Per-station results already describe every affected station, so swallow
	// the noise once the batch actually performed work. A bulk rejected at
	// its entry (nothing performed) keeps ErrShuttingDown so callers still
	// learn the operation never ran.
	if errors.Is(err, ErrShuttingDown) && result.Cancelled {
		for _, stationResult := range result.Results {
			if stationResult.Success || stationResult.CommandSent {
				return result, nil
			}
		}
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
		// Keep the Results contract consistent with every other path: an empty
		// list, not a nil slice (JSON [] rather than null).
		return BulkPowerResult{Target: busyTarget, Results: []BulkPowerStationResult{}}, ErrOperationInProgress
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
	result, contextAffected, err := m.setAllStationsPowerDetailed(operationContext, state)
	contextErr := operationContext.Err()
	if contextAffected && contextErr == nil && errors.Is(err, ErrShuttingDown) {
		// Lifecycle cancellation is delivered asynchronously by context.AfterFunc.
		// Preserve a deterministic cancelled result if shutdown reached a worker
		// before the derived operation context observed it.
		contextErr = context.Canceled
	}
	if contextAffected && contextErr != nil {
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
		// enumerated at all. Backfill through the same candidate pipeline the
		// normal path uses so the pre-work entries carry stations' snapshots
		// and the same selection semantics (booting skips, display names,
		// deterministic order) instead of a raw list of every known station.
		if len(result.Results) == 0 {
			if backfillTarget, backfillErr := bluetooth.ParsePowerTarget(state); backfillErr == nil {
				backfilled, _ := m.seedBulkPowerResults(m.selectBulkPowerCandidates(), backfillTarget, time.Now())
				result.Results = backfilled.Results
				m.attachBulkPowerStationInfos(result.Results)
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
	waitLimit := time.NewTimer(bulkCancelWaitLimit)
	defer waitLimit.Stop()
	select {
	case <-lifecycle.done:
		return nil
	case <-m.shutdownCh:
		return ErrShuttingDown
	case <-waitLimit.C:
		return fmt.Errorf("bulk operation did not stop within %s after cancellation", bulkCancelWaitLimit)
	}
}

type bulkPowerWork struct {
	station     *bluetooth.BaseStation
	resultIndex int
}

type bulkPowerCandidate struct {
	station  *bluetooth.BaseStation
	snapshot bluetooth.BaseStationSnapshot
	name     string
}

// bulkInterruptionReason attributes one skip reason to an interruption that
// stopped a bulk worker. Shutdown wins over a concurrent deadline because the
// worker observes a cancelled context either way, and a station-level timeout
// is only reported when the bulk context is still healthy.
func (m *Manager) bulkInterruptionReason(ctx context.Context, workerErr error) string {
	if m.shuttingDown.Load() {
		return ReasonShuttingDown
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return ReasonBulkOperationTimeout
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return ReasonOperationCancelled
	}
	if errors.Is(workerErr, context.DeadlineExceeded) {
		// Only this station's own budget expired; the bulk deadline is still
		// pending, so report the station-level timeout instead of blaming the
		// whole batch.
		return ReasonStationOperationTimeout
	}
	return ReasonOperationCancelled
}

// isContextInterruption reports whether a worker failure was actually shaped
// by cancellation or shutdown rather than failing on its own.
func isContextInterruption(ctx context.Context, workerErr error, shuttingDown bool) bool {
	return (errors.Is(workerErr, context.Canceled) || errors.Is(workerErr, context.DeadlineExceeded)) &&
		(ctx.Err() != nil || shuttingDown)
}

// failUntouchedBulkResults records a setup failure on every entry the batch
// never started. Untouched means not succeeded, not skipped, not sent, and
// not already carrying an error.
func failUntouchedBulkResults(results []BulkPowerStationResult, err error) {
	for index := range results {
		item := &results[index]
		if !item.Success && !item.Skipped && !item.CommandSent && item.Error == "" {
			item.Error = err.Error()
		}
	}
}

// setAllStationsPowerDetailed reports whether cancellation or shutdown
// actually prevented any selected station from reaching a terminal result.
// The caller uses that signal to distinguish an interrupted batch from a
// context that happened to finish after all station work was already done.
func (m *Manager) setAllStationsPowerDetailed(ctx context.Context, state string) (BulkPowerResult, bool, error) {
	target, err := bluetooth.ParsePowerTarget(state)
	result := BulkPowerResult{Target: target.String(), Results: []BulkPowerStationResult{}}
	if err != nil {
		return result, false, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	if m.shuttingDown.Load() {
		return result, true, ErrShuttingDown
	}
	if err := ctx.Err(); err != nil {
		return result, true, err
	}
	if err := m.beginBulkGlobalOperationContext(ctx); err != nil {
		contextAffected := errors.Is(err, context.Canceled) ||
			errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrShuttingDown)
		return result, contextAffected, err
	}
	defer m.endForegroundGlobalOperation()

	selectionTime := time.Now()
	result, work := m.seedBulkPowerResults(m.selectBulkPowerCandidates(), target, selectionTime)
	m.attachBulkPowerStationInfos(result.Results)
	if len(work) == 0 {
		return result, false, nil
	}
	if err := m.ensureReady(); err != nil {
		// A shutdown rejection shares the entry shape with every other
		// interrupted batch: leave the entries untouched so the outer
		// cancellation backfill marks them Skipped with the shutdown reason
		// instead of failed-with-error. Non-shutdown failures (an unavailable
		// adapter) keep per-entry error details.
		if !errors.Is(err, ErrShuttingDown) {
			failUntouchedBulkResults(result.Results, err)
		}
		return result, errors.Is(err, ErrShuttingDown), err
	}
	if err := ctx.Err(); err != nil {
		return result, true, err
	}

	var wg sync.WaitGroup
	semaphore := make(chan struct{}, 2)
	contextAffected := make([]bool, len(result.Results))

	for _, item := range work {
		wg.Add(1)
		go func(resultIndex int, s *bluetooth.BaseStation) {
			defer wg.Done()
			if m.runBulkPowerWorker(ctx, semaphore, target, s, &result.Results[resultIndex]) {
				contextAffected[resultIndex] = true
			}
		}(item.resultIndex, item.station)
	}

	wg.Wait()
	for _, affected := range contextAffected {
		if !affected {
			continue
		}
		if err := ctx.Err(); err != nil {
			return result, true, err
		}
		if m.shuttingDown.Load() {
			return result, true, ErrShuttingDown
		}
		return result, true, context.Canceled
	}
	return result, false, nil
}

// selectBulkPowerCandidates snapshots every known station, applies
// display-name overrides, and orders the batch deterministically.
func (m *Manager) selectBulkPowerCandidates() []bulkPowerCandidate {
	// Copy the station pointers under the read lock and snapshot them after
	// releasing it, matching GetStationInfo: Snapshot takes each station's
	// own mutex, which an abandoned WinRT cleanup can hold for a long time,
	// and holding the fleet lock meanwhile would stall every other fleet
	// reader and writer behind that one station.
	m.stationsMutex.RLock()
	stationPtrs := make([]*bluetooth.BaseStation, 0, len(m.stations))
	for _, stationPtr := range m.stations {
		if stationPtr == nil {
			continue
		}
		stationPtrs = append(stationPtrs, stationPtr)
	}
	m.stationsMutex.RUnlock()
	candidates := make([]bulkPowerCandidate, 0, len(stationPtrs))
	for _, stationPtr := range stationPtrs {
		snapshot := stationPtr.Snapshot()
		name := snapshot.Name
		if renamedName, ok := m.config.GetStationDisplayName(snapshot.Address, snapshot.Name); ok {
			name = renamedName
		}
		candidates = append(candidates, bulkPowerCandidate{station: stationPtr, snapshot: snapshot, name: name})
	}
	sort.Slice(candidates, func(i, j int) bool {
		left, right := candidates[i], candidates[j]
		return stationValuesLess(
			left.snapshot.Channel, left.name, left.snapshot.Address,
			right.snapshot.Channel, right.name, right.snapshot.Address,
		)
	})
	return candidates
}

// seedBulkPowerResults creates one entry per candidate, skipping stations
// already booting at selection time, and returns the work list for every
// entry that still needs a worker.
func (m *Manager) seedBulkPowerResults(candidates []bulkPowerCandidate, target bluetooth.PowerState, selectionTime time.Time) (BulkPowerResult, []bulkPowerWork) {
	result := BulkPowerResult{Target: target.String(), Results: []BulkPowerStationResult{}}
	work := make([]bulkPowerWork, 0, len(candidates))
	for _, candidate := range candidates {
		stationResult := BulkPowerStationResult{Address: candidate.snapshot.Address, Name: candidate.name}
		switch classifyCachedPower(candidate.snapshot, target, selectionTime) {
		case cachedPowerBooting:
			stationResult.Skipped = true
			stationResult.Reason = ReasonStationBooting
		}
		result.Results = append(result.Results, stationResult)
		if !stationResult.Skipped {
			work = append(work, bulkPowerWork{station: candidate.station, resultIndex: len(result.Results) - 1})
		}
	}
	return result, work
}

// attachBulkPowerStationInfos backfills the full station projection onto
// every entry in one fleet-info lookup.
func (m *Manager) attachBulkPowerStationInfos(results []BulkPowerStationResult) {
	infoByAddress := make(map[string]StationInfo, len(results))
	for _, info := range m.GetStationInfo() {
		infoByAddress[info.Address] = info
	}
	for index := range results {
		if info, ok := infoByAddress[results[index].Address]; ok {
			results[index].Station = info
			results[index].Name = info.Name
		}
	}
}

func (m *Manager) attachBulkPowerStationInfo(stationResult *BulkPowerStationResult) {
	if info, infoErr := m.stationInfoByAddress(stationResult.Address); infoErr == nil {
		stationResult.Station = info
		stationResult.Name = info.Name
	}
}

// runBulkPowerWorker drives one station's bulk attempt to a terminal entry and
// reports whether cancellation or shutdown actually shaped the outcome.
func (m *Manager) runBulkPowerWorker(ctx context.Context, semaphore chan struct{}, target bluetooth.PowerState, s *bluetooth.BaseStation, entry *BulkPowerStationResult) bool {
	select {
	case semaphore <- struct{}{}:
	case <-ctx.Done():
		entry.Skipped = true
		entry.Reason = m.bulkInterruptionReason(ctx, nil)
		return true
	case <-m.shutdownCh:
		entry.Skipped = true
		entry.Reason = ReasonShuttingDown
		return true
	}
	defer func() { <-semaphore }()
	operationContext, cancelOperation := m.newStationOperationContext(ctx)
	defer cancelOperation()
	if m.shuttingDown.Load() || ctx.Err() != nil {
		entry.Skipped = true
		entry.Reason = m.bulkInterruptionReason(ctx, nil)
		return true
	}
	// Seed the entry with the authoritative snapshot before any work that
	// could panic: a recovered panic must still report the station identity
	// instead of an empty address that matches no station.
	initialSnapshot := s.Snapshot()
	stationResult := BulkPowerStationResult{
		Address: initialSnapshot.Address,
		Name:    initialSnapshot.Name,
	}
	metadataReadRevision := initialSnapshot.MetadataReadRevision
	defer func() {
		m.reconcileMetadataReadResult(stationResult.Address, metadataReadRevision, s.Snapshot())
	}()
	cachedSkip := false
	workerErr := runSafely("bulk power worker", func() error {
		var applyErr error
		cachedSkip, applyErr = m.applyBulkPowerCommand(operationContext, s, target, &stationResult)
		return applyErr
	})
	contextAffected := false
	if workerErr != nil {
		if isContextInterruption(ctx, workerErr, m.shuttingDown.Load()) {
			contextAffected = true
		}
		// A context interruption owns the entry entirely and skips the
		// post-error bookkeeping the other outcomes share.
		if m.finalizeBulkPowerWorkerOutcome(ctx, s, &stationResult, workerErr) {
			*entry = stationResult
			return contextAffected
		}
	}
	m.attachBulkPowerStationInfo(&stationResult)
	communicationSucceeded := !cachedSkip && (workerErr == nil || stationResult.CommandSent)
	if communicationSucceeded && !bluetooth.RequiresReconnect(workerErr) &&
		!bluetooth.IsAdapterUnavailable(workerErr) {
		m.clearStatusFailureKind(stationResult.Address, statusRetryConnection)
	}
	*entry = stationResult
	return contextAffected
}

// applyBulkPowerCommand performs one station's cached-state verification,
// capability gate, and power write for a bulk attempt. It mutates the result
// incrementally so a recovered panic still reports the fields already settled,
// and reports whether the outcome was a cached skip that never communicated.
func (m *Manager) applyBulkPowerCommand(operationContext context.Context, s *bluetooth.BaseStation, target bluetooth.PowerState, stationResult *BulkPowerStationResult) (bool, error) {
	snapshot := s.Snapshot()
	stationResult.Address = snapshot.Address
	stationResult.Name = snapshot.Name
	disposition := classifyCachedPower(snapshot, target, time.Now())
	if disposition == cachedPowerBooting {
		stationResult.Skipped = true
		stationResult.Reason = ReasonStationBooting
		return true, nil
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
				stationResult.Skipped = true
				stationResult.Reason = ReasonStationBooting
				return true, nil
			case cachedPowerAtTarget:
				stationResult.Skipped = true
				stationResult.Success = true
				stationResult.Confirmed = true
				stationResult.Reason = ReasonAlreadyAtTarget
				return true, nil
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
	if err != nil {
		return false, err
	}
	// The station may enter a boot transition while capability
	// discovery is in flight. Re-evaluate at the final write boundary
	// instead of acting on the worker's earlier snapshot.
	if isFreshBootingPower(s.Snapshot(), time.Now()) {
		stationResult.Skipped = true
		stationResult.Reason = ReasonStationBooting
		return true, nil
	}
	if !capabilities.PowerWrite {
		stationResult.Skipped = true
		stationResult.Reason = ReasonUnsupportedCapability
		return false, nil
	}
	if target == bluetooth.PowerStateStandby && !capabilities.Standby {
		stationResult.Skipped = true
		stationResult.Reason = ReasonUnsupportedStandby
		return false, nil
	}
	var controlResult bluetooth.PowerControlResult
	controlResult, err = m.bluetoothOps.setPowerState(operationContext, s, target)
	stationResult.CommandSent = err == nil
	stationResult.Confirmed = controlResult.Confirmed
	if err == nil {
		stationResult.Success = true
	}
	return false, err
}

// finalizeBulkPowerWorkerOutcome classifies a failed bulk attempt into the
// entry's terminal shape. It reports whether the failure was a context
// interruption, in which case the entry is complete and needs no further
// bookkeeping.
func (m *Manager) finalizeBulkPowerWorkerOutcome(ctx context.Context, s *bluetooth.BaseStation, stationResult *BulkPowerStationResult, workerErr error) bool {
	var confirmationErr *bluetooth.PowerConfirmationError
	switch {
	case errors.As(workerErr, &confirmationErr):
		// A possibly-sent command keeps its command-sent outcome even
		// when shutdown cancelled the confirmation read.
		m.observeStationBluetoothError(s, stationResult.Address, workerErr)
		stationResult.CommandSent = true
		stationResult.Success = true
		stationResult.Confirmed = false
		stationResult.Error = workerErr.Error()
	case errors.Is(workerErr, context.Canceled), errors.Is(workerErr, context.DeadlineExceeded):
		stationResult.Skipped = true
		stationResult.Reason = m.bulkInterruptionReason(ctx, workerErr)
		stationResult.CommandSent = false
		stationResult.Error = ""
		m.attachBulkPowerStationInfo(stationResult)
		return true
	default:
		m.observeStationBluetoothError(s, stationResult.Address, workerErr)
		if bluetooth.IsUnsupportedCapabilityError(workerErr) {
			// Reason is a closed public contract: classify through the
			// constants instead of leaking the raw error string.
			stationResult.Skipped = true
			stationResult.Reason = ReasonUnsupportedCapability
			var unsupported *bluetooth.UnsupportedCapabilityError
			if errors.As(workerErr, &unsupported) && unsupported.Capability == "standby" {
				stationResult.Reason = ReasonUnsupportedStandby
			}
		}
		if !stationResult.Skipped {
			stationResult.Error = workerErr.Error()
		}
	}
	return false
}
