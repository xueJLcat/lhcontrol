package station

import (
	"context"
	"errors"
	"fmt"
	"lhcontrol/internal/bluetooth"
	"log"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// statusRefreshJoinLimit bounds the wait for status-read workers after the
// dispatch loop closes. A wedged WinRT cleanup can keep a worker blocked on a
// transport lock long past its read budget; without this cap a single wedged
// device would hold statusOperationMutex forever and stop every later status
// refresh (and the exclusive front-door operations that queue behind it).
const statusRefreshJoinLimit = 20 * time.Second

// isPureContextError reports whether every leaf in the error tree is a context
// cancellation or deadline. The bluetooth layer joins the stopping context
// error with a real failure a read hit just before it stopped
// (StatusReadError{Power: join(transport error, context.Canceled)}), and a
// plain errors.Is(err, context.Canceled) matches those mixed errors too. Only
// errors made exclusively of context errors justify the "interrupted, retry
// the read" handling; anything else must run the normal failure path.
func isPureContextError(err error) bool {
	if err == nil {
		return false
	}
	sawLeaf := false
	pure := true
	var walk func(current error)
	walk = func(current error) {
		if current == nil {
			return
		}
		if joined, ok := current.(interface{ Unwrap() []error }); ok {
			for _, nested := range joined.Unwrap() {
				walk(nested)
			}
			return
		}
		if wrapped := errors.Unwrap(current); wrapped != nil {
			walk(wrapped)
			return
		}
		sawLeaf = true
		if !errors.Is(current, context.Canceled) && !errors.Is(current, context.DeadlineExceeded) {
			pure = false
		}
	}
	walk(err)
	return sawLeaf && pure
}

func (m *Manager) CheckAllStationStatuses() ([]StationInfo, error) {
	if !m.statusOperationMutex.TryLock() {
		return m.GetStationInfo(), fmt.Errorf("status refresh already in progress: %w", ErrOperationInProgress)
	}
	defer m.statusOperationMutex.Unlock()
	refreshContext, cancelRefresh := context.WithTimeout(m.lifecycleContext, m.statusRefreshTimeoutDuration())
	defer cancelRefresh()
	statusDone := m.beginStatusLifecycle(cancelRefresh)
	endStatusLifecycleNow := true
	defer func() {
		if endStatusLifecycleNow {
			m.endStatusLifecycle(statusDone)
		}
	}()
	if err := m.beginSharedOperation(); err != nil {
		return m.GetStationInfo(), err
	}
	defer m.endSharedOperation()
	if err := m.ensureReady(); err != nil {
		return m.GetStationInfo(), err
	}
	candidates, disconnectedAddresses := m.selectStatusRefreshCandidates()
	if len(candidates) == 0 {
		for _, address := range disconnectedAddresses {
			m.ensureStatusRecoveryTracked(address)
		}
		return m.GetStationInfo(), nil
	}
	// Sort by the addresses captured during candidate selection: a comparator
	// that re-snapshotted each station would take every station's own mutex
	// O(n log n) times, and one wedged WinRT cleanup could stall the whole
	// refresh inside sort.Slice.
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].sortKey < candidates[j].sortKey
	})
	stationsToRead := make([]*bluetooth.BaseStation, 0, len(candidates))
	for _, candidate := range candidates {
		stationsToRead = append(stationsToRead, candidate.station)
	}
	type statusReadWork struct {
		index   int
		station *bluetooth.BaseStation
	}
	statusErrors := make([]error, len(stationsToRead))
	stationCompleted := make([]atomic.Bool, len(stationsToRead))
	work := make(chan statusReadWork)
	// Keep one GATT slot available for foreground commands while the periodic
	// refresh reads connected stations. The early return above guarantees at
	// least one station reaches the workers.
	const workerCount = 1
	var wg sync.WaitGroup
	for worker := 0; worker < workerCount; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range work {
				if readErr := m.readStationStatus(refreshContext, item.station); readErr != nil {
					statusErrors[item.index] = readErr
				}
				stationCompleted[item.index].Store(true)
			}
		}()
	}
dispatch:
	for index, station := range stationsToRead {
		select {
		case work <- statusReadWork{index: index, station: station}:
		case <-refreshContext.Done():
			m.recordSkippedStatusReads(refreshContext, stationsToRead[index:], statusErrors[index:])
			break dispatch
		}
	}
	close(work)
	joined := make(chan struct{})
	go func() {
		defer close(joined)
		wg.Wait()
	}()
	joinLimit := m.statusRefreshJoinWait
	if joinLimit <= 0 {
		joinLimit = statusRefreshJoinLimit
	}
	joinTimer := time.NewTimer(joinLimit)
	select {
	case <-joined:
		joinTimer.Stop()
	case <-joinTimer.C:
		// A worker is blocked past its budget (typically on an adapter call
		// that ignores cancellation). Abandon the refresh instead of holding
		// statusOperationMutex until it unblocks: the worker keeps running and
		// releases its slot when the OS call returns, and registering every
		// unsettled candidate for recovery lets the scheduler re-read it. Do
		// not touch statusErrors from here on: the abandoned worker may still
		// be writing its entry.
		log.Printf("Bluetooth status refresh worker did not finish within %s; abandoning the refresh", joinLimit)
		for index, stationPtr := range stationsToRead {
			if stationCompleted[index].Load() {
				// A finished read already settled its own outcome: successes
				// need no follow-up, and failures recorded their own backoff.
				// Re-marking them due now would override that schedule and
				// immediately reconnect stations the refresh already handled.
				continue
			}
			m.trackStatusRefreshPending(stationPtr.Snapshot().Address)
		}
		// Stations first observed disconnected in this abandoned refresh still
		// need recovery tracking; the normal path registers them after the
		// worker join.
		for _, address := range disconnectedAddresses {
			m.ensureStatusRecoveryTracked(address)
		}
		m.scheduleStatusRecovery()
		// The abandoned worker still holds the shared read lock and a GATT
		// slot. Keep this refresh's lifecycle done channel open until the
		// worker releases them so an exclusive foreground operation (scan or
		// bulk) that starts meanwhile can drain on the channel instead of
		// observing a nil channel and returning an immediate, non-waiting
		// Busy for as long as the OS call stays wedged. The synchronous
		// lifecycle end is skipped by the flag checked in the deferred end.
		endStatusLifecycleNow = false
		go func() {
			<-joined
			m.abandonStatusLifecycle(statusDone)
		}()
		return m.GetStationInfo(), fmt.Errorf("status refresh did not finish within %s: %w", joinLimit, ErrOperationInProgress)
	}
	// Start newly discovered disconnect recovery only after foreground status
	// reads have released their slots. Otherwise this refresh can make one of
	// its own connected-device reads fail with Busy.
	for _, address := range disconnectedAddresses {
		m.ensureStatusRecoveryTracked(address)
	}
	m.scheduleStatusRecovery()
	statusInfos := m.GetStationInfo()
	incomplete := make([]error, 0, len(statusErrors))
	for _, statusErr := range statusErrors {
		if statusErr != nil {
			incomplete = append(incomplete, statusErr)
		}
	}
	if len(incomplete) > 0 {
		return statusInfos, fmt.Errorf("one or more station status reads were incomplete: %w", errors.Join(incomplete...))
	}
	return statusInfos, nil
}

// statusRefreshCandidate carries the station together with the address
// captured at selection time so ordering never re-snapshots a station.
type statusRefreshCandidate struct {
	station *bluetooth.BaseStation
	sortKey string
}

// selectStatusRefreshCandidates splits present stations into connected
// stations eligible for a status read and addresses first observed
// disconnected, which need recovery tracking instead.
func (m *Manager) selectStatusRefreshCandidates() ([]statusRefreshCandidate, []string) {
	// Snapshot and stationConnected take each station's own mutex, which an
	// abandoned WinRT cleanup can hold for a long time; run them outside the
	// fleet lock so one wedged station cannot stall every fleet reader and
	// writer.
	stationPtrs := m.stationPointers()
	candidates := make([]statusRefreshCandidate, 0)
	disconnectedAddresses := make([]string, 0)
	for _, stationPtr := range stationPtrs {
		snapshot := stationPtr.Snapshot()
		if !snapshot.Present {
			continue
		}
		if m.bluetoothOps.stationConnected(stationPtr) {
			candidates = append(candidates, statusRefreshCandidate{
				station: stationPtr,
				sortKey: strings.ToLower(snapshot.Address),
			})
		} else {
			disconnectedAddresses = append(disconnectedAddresses, snapshot.Address)
		}
	}
	sort.Strings(disconnectedAddresses)
	return candidates, disconnectedAddresses
}

// readStationStatus performs one station's refresh read and returns the
// per-station error the caller should record, if any. Every failure shape
// (busy slot, interruption, fleet deadline, per-station budget, transport
// error) runs its own bookkeeping here so the dispatch loop stays flat.
func (m *Manager) readStationStatus(refreshContext context.Context, stationPtr *bluetooth.BaseStation) error {
	address := stationPtr.Snapshot().Address
	readContext, cancelRead := context.WithTimeout(refreshContext, m.statusReadTimeoutDuration())
	// Distinguish this station's own read budget from the fleet-wide
	// refresh deadline: only when the refresh deadline is the binding
	// constraint is a deadline failure fleet-wide. Otherwise it must go
	// through the structured per-station handling below so backoff and
	// failure accounting still run.
	readDeadline, hasReadDeadline := readContext.Deadline()
	refreshDeadline, hasRefreshDeadline := refreshContext.Deadline()
	ownBudget := hasReadDeadline && hasRefreshDeadline && readDeadline.Before(refreshDeadline)
	if err := m.beginStationOperationKindContext(address, deviceOperationStatus, cancelRead); err != nil {
		cancelRead()
		if errors.Is(err, ErrOperationInProgress) {
			m.trackStatusRefreshPending(address)
			return nil
		}
		return fmt.Errorf("%s: status read skipped: %w", address, err)
	}
	defer m.endStationOperation(address)
	defer cancelRead()
	workerErr := runSafely("station status worker", func() error {
		return m.bluetoothOps.readPowerStateContext(readContext, stationPtr)
	})
	if workerErr == nil {
		m.clearStatusFailureKind(
			address,
			statusRetryConnection|statusRetryChannel|statusRetryRefresh,
		)
		return nil
	}
	if m.shuttingDown.Load() && errors.Is(workerErr, context.Canceled) {
		return fmt.Errorf("%s: status read cancelled: %w", address, workerErr)
	}
	if isPureContextError(workerErr) && errors.Is(workerErr, context.Canceled) &&
		m.lifecycleContext.Err() == nil {
		// Only an error made exclusively of context errors is a
		// plain interruption: the bluetooth layer joins a real
		// read failure with the cancelling context error, and
		// that mixed outcome must take the failure path below.
		m.trackStatusRefreshPending(address)
		return nil
	}
	if !ownBudget && errors.Is(refreshContext.Err(), context.DeadlineExceeded) &&
		isPureContextError(workerErr) && errors.Is(workerErr, context.DeadlineExceeded) {
		// Only an error made exclusively of context errors is a fleet-window
		// interruption: the bluetooth layer joins a real read failure with
		// the deadline it hit, and that mixed outcome must take the failure
		// path below, matching the cancellation branch above.
		m.trackStatusRefreshPending(address)
		return fmt.Errorf("%s: status refresh deadline exceeded: %w", address, workerErr)
	}
	m.observeBluetoothError(workerErr)
	if bluetooth.IsStationNotConnected(workerErr) {
		// The link dropped between candidate selection and this read. That is
		// the same observation the selection-time disconnect branch makes, so
		// route it to the same immediate reconnect path instead of counting a
		// transport failure with backoff: the read never reached the link and
		// must not consume the absent-station retry budget either.
		m.ensureStatusRecoveryTracked(address)
		return nil
	}
	var readErr *bluetooth.StatusReadError
	if errors.As(workerErr, &readErr) {
		// A per-station read-budget deadline is not evidence the link
		// is broken, matching recoverOneStation and the bluetooth
		// RequiresReconnect rule. Back off and let the next refresh
		// retry instead of disconnecting a possibly-healthy station.
		if readErr.Power != nil &&
			errors.Is(readErr.Power, context.DeadlineExceeded) &&
			!bluetooth.RequiresReconnect(readErr.Power) {
			m.noteStatusFailure(address)
			m.trackStatusRefreshPending(address)
			// The refresh marker must not fall due before the connection
			// backoff just recorded: recovery picks the earliest due
			// schedule, so an immediate marker would re-run the read that
			// just timed out, negate the backoff, and count failures
			// against the absent-station budget ahead of schedule.
			m.deferStatusRecovery(address, m.statusConnectionRetryDelay(address))
			return fmt.Errorf("%s: status read deadline exceeded: %w", address, workerErr)
		}
		m.recordStructuredReadResult(stationPtr, address, readErr.Power, readErr.Channel)
	} else if errors.Is(workerErr, context.DeadlineExceeded) &&
		!bluetooth.RequiresReconnect(workerErr) {
		// The context can expire just before ReadPowerStateContext
		// starts, in which case it returns a bare deadline rather than
		// a structured read error. Treat it like the structured power
		// timeout above instead of disconnecting a healthy station.
		m.noteStatusFailure(address)
		m.trackStatusRefreshPending(address)
		m.deferStatusRecovery(address, m.statusConnectionRetryDelay(address))
	} else {
		m.recordUnstructuredStationFailure(stationPtr, address, workerErr)
	}
	return fmt.Errorf("%s: %w", address, workerErr)
}

// recordSkippedStatusReads registers every station never dispatched because
// the refresh context stopped, marking each for recovery and reporting the
// stop unless a plain caller-side cancellation silenced the batch.
func (m *Manager) recordSkippedStatusReads(refreshContext context.Context, skipped []*bluetooth.BaseStation, statusErrors []error) {
	for offset, stationPtr := range skipped {
		address := stationPtr.Snapshot().Address
		m.trackStatusRefreshPending(address)
		if !errors.Is(refreshContext.Err(), context.Canceled) || m.lifecycleContext.Err() != nil {
			stopDescription := "status refresh stopped"
			if errors.Is(refreshContext.Err(), context.DeadlineExceeded) {
				stopDescription = "status refresh deadline exceeded"
			}
			statusErrors[offset] = fmt.Errorf("%s: %s: %w", address, stopDescription, refreshContext.Err())
		}
	}
}
