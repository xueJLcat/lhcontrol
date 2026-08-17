package station

import (
	"context"
	"errors"
	"strings"
	"time"
)

// foregroundDrainWaitLimit bounds how long a foreground global acquisition
// waits for manager-owned background work (status refresh or recovery) to
// drain. Background work is normally bounded by its own read and cleanup
// budgets, but an abandoned bounded-cleanup goroutine can keep a station lock
// held long past its deadline, leaving the background done channel un-closed.
// Without this limit a scan prepared from a deadline-free context (for
// example ScanAndFetchStationsContext on context.Background) would hang
// indefinitely instead of reporting Busy so the caller can retry.
const foregroundDrainWaitLimit = 45 * time.Second

func (m *Manager) registerOperation() error {
	m.lifecycleMutex.Lock()
	defer m.lifecycleMutex.Unlock()
	if m.shuttingDown.Load() {
		return ErrShuttingDown
	}
	m.activeOperations++
	return nil
}
func (m *Manager) unregisterOperation() {
	m.lifecycleMutex.Lock()
	m.activeOperations--
	if m.activeOperations == 0 {
		m.lifecycleCond.Broadcast()
	}
	m.lifecycleMutex.Unlock()
}
func (m *Manager) beginOperation() error {
	if err := m.registerOperation(); err != nil {
		return err
	}
	if !m.operationMutex.TryLock() {
		m.unregisterOperation()
		return ErrOperationInProgress
	}
	m.globalOperationMutex.Lock()
	m.exclusiveOperationActive = true
	m.globalOperationMutex.Unlock()
	return nil
}
func (m *Manager) endOperation() {
	m.globalOperationMutex.Lock()
	m.exclusiveOperationActive = false
	m.globalOperationMutex.Unlock()
	m.operationMutex.Unlock()
	m.unregisterOperation()
}

// beginForegroundGlobalOperation waits for manager-owned background status
// and recovery work, while preserving immediate Busy responses for another
// foreground global, device, or configuration operation.
func (m *Manager) beginForegroundGlobalOperation() error {
	return m.beginForegroundGlobalOperationContext(context.Background())
}

// attemptForegroundGlobalOperation performs one acquisition attempt. It
// returns (nil, nil) after acquiring the operation, a non-nil channel when
// manager-owned background work must drain first, or an error for an
// immediate rejection. Callers must never hold scanTransitionMutex across the
// wait on the returned channel: the drain can stretch several seconds (a
// background cleanup exiting a stuck adapter call among them), and holding the
// lock would suspend every new scan, StopScan, and foreground station or
// configuration action for the whole window.
func (m *Manager) attemptForegroundGlobalOperation(ctx context.Context) (<-chan struct{}, error) {
	if err := m.foregroundContextError(ctx); err != nil {
		return nil, err
	}
	err := m.beginOperation()
	if err == nil {
		if contextErr := m.foregroundContextError(ctx); contextErr != nil {
			m.endOperation()
			return nil, contextErr
		}
		m.globalOperationMutex.Lock()
		m.foregroundGlobalActive = true
		m.globalOperationMutex.Unlock()
		return nil, nil
	}
	if !errors.Is(err, ErrOperationInProgress) {
		return nil, err
	}
	m.globalOperationMutex.Lock()
	foregroundActive := m.foregroundGlobalActive
	m.globalOperationMutex.Unlock()
	if foregroundActive {
		return nil, err
	}
	m.recoveryOperationMutex.Lock()
	recoveryDone := m.recoveryOperationDone
	recoveryCancel := m.cancelRecovery
	m.recoveryOperationMutex.Unlock()
	m.statusLifecycleMutex.Lock()
	statusDone := m.statusOperationDone
	statusCancel := m.cancelStatusOperation
	m.statusLifecycleMutex.Unlock()
	var backgroundDone <-chan struct{}
	if recoveryDone != nil {
		backgroundDone = recoveryDone
		// Only cancel the lifecycle whose done channel is being waited on:
		// capturing the channel and the cancel together keeps them paired.
		// Cancelling the stored cancel unconditionally could hit a newer
		// refresh that replaced an abandoned one while this operation waits
		// on the abandoned done channel.
		if recoveryCancel != nil {
			recoveryCancel()
		}
	} else if statusDone != nil {
		backgroundDone = statusDone
		if statusCancel != nil {
			statusCancel()
		}
	}
	if backgroundDone == nil {
		return nil, err
	}
	return backgroundDone, nil
}

// foregroundDrainDeadline returns the channel bounding background-drain waits.
// It applies even when the caller ctx carries its own deadline: a bulk context
// always has the bulk timeout, and without this cap a wedged background worker
// would consume the entire bulk budget waiting (surfacing as a bulk timeout)
// instead of failing fast as retryable Busy. The same applies to per-station
// foreground waits bounded by the station operation timeout. The effective
// wait is the minimum of this limit and the ctx deadline, both selected in the
// caller's loop.
func (m *Manager) foregroundDrainDeadline(ctx context.Context) (<-chan time.Time, func()) {
	limit := m.foregroundDrainWait
	if limit <= 0 {
		limit = foregroundDrainWaitLimit
	}
	timer := time.NewTimer(limit)
	return timer.C, func() { timer.Stop() }
}

// waitForForegroundGlobalOperation runs the attempt/wait loop shared by the
// global foreground acquisitions.
func (m *Manager) waitForForegroundGlobalOperation(ctx context.Context) error {
	drainDeadline, stopDrainDeadline := m.foregroundDrainDeadline(ctx)
	defer stopDrainDeadline()
	for {
		backgroundDone, err := m.attemptForegroundGlobalOperation(ctx)
		if err != nil {
			return err
		}
		if backgroundDone == nil {
			return nil
		}
		select {
		case <-backgroundDone:
		case <-drainDeadline:
			// Background work stayed queued past the bounded wait. Report Busy
			// (retryable) instead of hanging on a done channel that a wedged
			// background worker may never close.
			return ErrOperationInProgress
		case <-ctx.Done():
			return m.foregroundContextError(ctx)
		case <-m.shutdownCh:
			return ErrShuttingDown
		}
	}
}

func (m *Manager) beginForegroundGlobalOperationContext(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	return m.waitForForegroundGlobalOperation(ctx)
}
func (m *Manager) endForegroundGlobalOperation() {
	m.globalOperationMutex.Lock()
	m.foregroundGlobalActive = false
	m.globalOperationMutex.Unlock()
	m.endOperation()
}
func (m *Manager) beginStatusLifecycle(cancel context.CancelFunc) chan struct{} {
	done := make(chan struct{})
	m.statusLifecycleMutex.Lock()
	m.statusOperationDone = done
	m.cancelStatusOperation = cancel
	m.statusLifecycleMutex.Unlock()
	return done
}
func (m *Manager) endStatusLifecycle(done chan struct{}) {
	m.statusLifecycleMutex.Lock()
	if m.statusOperationDone == done {
		m.statusOperationDone = nil
		m.cancelStatusOperation = nil
		close(done)
	}
	m.statusLifecycleMutex.Unlock()
}

// abandonStatusLifecycle closes one abandoned refresh's done channel when its
// last worker releases the shared read lock, clearing the manager reference
// only when no newer refresh replaced it. Unlike endStatusLifecycle it always
// closes the channel: a foreground operation that captured it before a newer
// refresh published its own must still be released by the abandoned worker.
func (m *Manager) abandonStatusLifecycle(done chan struct{}) {
	m.statusLifecycleMutex.Lock()
	if m.statusOperationDone == done {
		m.statusOperationDone = nil
		m.cancelStatusOperation = nil
	}
	close(done)
	m.statusLifecycleMutex.Unlock()
}

// beginSharedOperation participates in shutdown and excludes global operations
// without consuming a GATT slot. It is used for short configuration writes.
func (m *Manager) beginSharedOperation() error {
	if err := m.registerOperation(); err != nil {
		return err
	}
	if !m.operationMutex.TryRLock() {
		m.unregisterOperation()
		return ErrOperationInProgress
	}
	return nil
}
func (m *Manager) endSharedOperation() {
	m.operationMutex.RUnlock()
	m.unregisterOperation()
}
func (m *Manager) beginForegroundSharedOperation() error {
	m.scanTransitionMutex.Lock()
	defer m.scanTransitionMutex.Unlock()
	if m.isScanning.Load() {
		return ErrOperationInProgress
	}
	if err := m.beginSharedOperation(); err != nil {
		return err
	}
	m.globalOperationMutex.Lock()
	m.foregroundSharedActive++
	m.globalOperationMutex.Unlock()
	return nil
}
func (m *Manager) endForegroundSharedOperation() {
	m.globalOperationMutex.Lock()
	m.foregroundSharedActive--
	m.globalOperationMutex.Unlock()
	m.endSharedOperation()
}

// beginStationOperation rejects duplicate requests for one physical station
// and caps independent GATT work at two devices. It never waits while holding
// the global read lock, so a request flood cannot starve a scan.
func (m *Manager) beginStationOperation(address string) error {
	return m.beginStationOperationKind(address, deviceOperationForeground)
}
func (m *Manager) beginStationOperationKind(address string, kind deviceOperationKind) error {
	return m.beginStationOperationKindContext(address, kind, nil)
}
func (m *Manager) beginStationOperationKindContext(address string, kind deviceOperationKind, cancel context.CancelFunc) error {
	if err := m.registerOperation(); err != nil {
		return err
	}
	if !m.operationMutex.TryRLock() {
		m.unregisterOperation()
		return ErrOperationInProgress
	}
	key := strings.ToLower(address)
	m.deviceOperationMutex.Lock()
	if active, exists := m.activeDeviceOperations[key]; exists {
		var backgroundDone <-chan struct{}
		if active.kind == deviceOperationStatus || active.kind == deviceOperationRecovery {
			backgroundDone = active.done
		}
		m.deviceOperationMutex.Unlock()
		m.operationMutex.RUnlock()
		m.unregisterOperation()
		return &deviceOperationBusyError{backgroundDone: backgroundDone, cancelBackground: active.cancel}
	}
	select {
	case m.deviceOperationSlots <- struct{}{}:
	default:
		m.deviceOperationMutex.Unlock()
		m.operationMutex.RUnlock()
		m.unregisterOperation()
		return ErrOperationInProgress
	}
	m.activeDeviceOperations[key] = activeDeviceOperation{
		kind:   kind,
		done:   make(chan struct{}),
		cancel: cancel,
	}
	m.deviceOperationMutex.Unlock()
	return nil
}
func (m *Manager) endStationOperation(address string) {
	key := strings.ToLower(address)
	m.deviceOperationMutex.Lock()
	active, exists := m.activeDeviceOperations[key]
	if !exists {
		m.deviceOperationMutex.Unlock()
		// No matching beginStationOperation holds a slot, the shared read
		// lock, or a lifecycle registration for this station. Releasing them
		// here would either block forever on an empty slot channel or silently
		// release resources owned by another active operation.
		return
	}
	delete(m.activeDeviceOperations, key)
	// Release the slot token before waking waiters on active.done: a waiter
	// woken by the close retries beginStationOperation immediately, and the
	// slot must already be free by then or it gets a spurious Busy. Each map
	// entry holds exactly one token, so the receive cannot block under the lock.
	<-m.deviceOperationSlots
	close(active.done)
	m.deviceOperationMutex.Unlock()
	m.operationMutex.RUnlock()
	m.unregisterOperation()
}
func (m *Manager) beginRecoveryStationOperation(address string) error {
	m.recoveryOperationMutex.Lock()
	if m.recoveryOperationDone != nil {
		m.recoveryOperationMutex.Unlock()
		return ErrOperationInProgress
	}
	done := make(chan struct{})
	recoveryContext, cancelRecovery := context.WithCancel(m.lifecycleContext)
	m.recoveryOperationDone = done
	m.recoveryContext = recoveryContext
	m.cancelRecovery = cancelRecovery
	m.recoveryGeneration++
	m.recoveryOperationMutex.Unlock()
	if err := m.beginStationOperationKindContext(address, deviceOperationRecovery, cancelRecovery); err != nil {
		cancelRecovery()
		m.recoveryOperationMutex.Lock()
		if m.recoveryOperationDone == done {
			m.recoveryOperationDone = nil
			m.recoveryContext = nil
			m.cancelRecovery = nil
			m.recoveryGeneration++
			close(done)
		}
		m.recoveryOperationMutex.Unlock()
		return err
	}
	return nil
}
func (m *Manager) endRecoveryStationOperation(address string) {
	m.endStationOperation(address)
	m.recoveryOperationMutex.Lock()
	done := m.recoveryOperationDone
	cancelRecovery := m.cancelRecovery
	m.recoveryOperationDone = nil
	m.recoveryContext = nil
	m.cancelRecovery = nil
	if cancelRecovery != nil {
		cancelRecovery()
	}
	if done != nil {
		m.recoveryGeneration++
		close(done)
	}
	m.recoveryOperationMutex.Unlock()
}

// beginForegroundStationOperation waits only for an active background
// recovery. It still rejects conflicts with other foreground work immediately,
// while preventing a hidden recovery task from making a UI-permitted second
// device action fail with Busy.
func (m *Manager) beginForegroundStationOperation(address string) error {
	return m.beginForegroundStationOperationContext(context.Background(), address)
}

// beginForegroundStationOperationContext guards the busy check and the
// acquisition under the scan transition lock, but never holds it across the
// waits for background work: finishScan needs the same lock to publish the
// terminal scan state, and a foreground wait that can stretch several seconds
// (a background cleanup draining a stuck adapter call among them) would keep
// isScanning true and delay every new scan, bulk, or StopScan behind it.
func (m *Manager) beginForegroundStationOperationContext(ctx context.Context, address string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	drainDeadline, stopDrainDeadline := m.foregroundDrainDeadline(ctx)
	defer stopDrainDeadline()
	for {
		if err := m.foregroundContextError(ctx); err != nil {
			return err
		}
		m.recoveryOperationMutex.Lock()
		generationBefore := m.recoveryGeneration
		m.recoveryOperationMutex.Unlock()
		m.scanTransitionMutex.Lock()
		if err := m.foregroundContextError(ctx); err != nil {
			m.scanTransitionMutex.Unlock()
			return err
		}
		if m.isScanning.Load() {
			m.scanTransitionMutex.Unlock()
			return ErrOperationInProgress
		}
		err := m.beginStationOperation(address)
		m.scanTransitionMutex.Unlock()
		if !errors.Is(err, ErrOperationInProgress) {
			return err
		}
		var deviceBusy *deviceOperationBusyError
		if errors.As(err, &deviceBusy) {
			if deviceBusy.backgroundDone == nil {
				// Another foreground operation owns this station. Reject the
				// duplicate immediately; falling through to the recovery wait
				// would cancel and block on some unrelated station's
				// background work before still returning Busy, contradicting
				// the never-wait contract for per-station conflicts.
				return err
			}
			if deviceBusy.cancelBackground != nil {
				deviceBusy.cancelBackground()
			} else {
				m.cancelRecoveryForForeground()
			}
			select {
			case <-deviceBusy.backgroundDone:
				continue
			case <-drainDeadline:
				// A wedged background worker (an abandoned status read stuck in
				// an adapter call that ignores cancellation among them) must not
				// consume the entire per-station operation budget and surface as
				// an operation timeout; report retryable Busy like the global
				// drain paths do.
				return ErrOperationInProgress
			case <-ctx.Done():
				return m.foregroundContextError(ctx)
			case <-m.shutdownCh:
				return ErrShuttingDown
			}
		}
		if m.foregroundSlotMissHook != nil {
			m.foregroundSlotMissHook()
		}
		m.recoveryOperationMutex.Lock()
		done := m.recoveryOperationDone
		generationAfter := m.recoveryGeneration
		recoveryCancel := m.cancelRecovery
		m.recoveryOperationMutex.Unlock()
		if generationAfter != generationBefore {
			continue
		}
		if done == nil {
			return err
		}
		// Cancel the recovery captured together with the done channel above:
		// reading the stored cancel later could hit a replacement recovery
		// published while this operation waits on the captured channel.
		if recoveryCancel != nil {
			recoveryCancel()
		}
		select {
		case <-done:
		case <-drainDeadline:
			// Same bounded-drain rule as the device-busy wait: a wedged
			// recovery worker must keep the station retryable as Busy instead
			// of consuming the whole per-station budget as an operation timeout.
			return ErrOperationInProgress
		case <-ctx.Done():
			return m.foregroundContextError(ctx)
		case <-m.shutdownCh:
			return ErrShuttingDown
		}
	}
}

// foregroundContextError keeps caller cancellation and deadlines intact, but
// normalizes the lifecycle cancellation triggered by BeginShutdown to the
// manager's public shutdown sentinel. This closes the race where shutdown can
// begin after a public action's early guard but before the coordinator reads
// its context.
func (m *Manager) foregroundContextError(ctx context.Context) error {
	err := ctx.Err()
	if errors.Is(err, context.Canceled) && m.shuttingDown.Load() {
		return ErrShuttingDown
	}
	return err
}
func (m *Manager) beginBulkGlobalOperation() error {
	return m.beginBulkGlobalOperationContext(context.Background())
}

// beginBulkGlobalOperationContext rejects a bulk while a scan runs, and
// acquires the global operation once manager-owned background work has
// drained. The scan check and each acquisition attempt run under the scan
// transition lock (the same critical section reserveScan uses), but the wait
// for background work deliberately runs outside it: the wait can reach the
// bulk timeout when a background cleanup drains a stuck adapter call, and
// holding the lock across it would suspend every new scan, StopScan, and
// foreground station or configuration action for the whole window. The check
// repeats after every drained wait so a scan that started meanwhile still
// rejects the bulk instead of running alongside it.
func (m *Manager) beginBulkGlobalOperationContext(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	drainDeadline, stopDrainDeadline := m.foregroundDrainDeadline(ctx)
	defer stopDrainDeadline()
	for {
		if err := m.foregroundContextError(ctx); err != nil {
			return err
		}
		m.scanTransitionMutex.Lock()
		if m.isScanning.Load() {
			m.scanTransitionMutex.Unlock()
			return ErrOperationInProgress
		}
		backgroundDone, err := m.attemptForegroundGlobalOperation(ctx)
		m.scanTransitionMutex.Unlock()
		if err != nil {
			return err
		}
		if backgroundDone == nil {
			return nil
		}
		select {
		case <-backgroundDone:
		case <-drainDeadline:
			// Same bounded-drain rule as waitForForegroundGlobalOperation: a
			// wedged background worker must keep the bulk retryable as Busy
			// instead of holding the global lock wait open indefinitely.
			return ErrOperationInProgress
		case <-ctx.Done():
			return m.foregroundContextError(ctx)
		case <-m.shutdownCh:
			return ErrShuttingDown
		}
	}
}
func (m *Manager) hasForegroundOperation() bool {
	m.globalOperationMutex.Lock()
	active := m.foregroundGlobalActive || m.foregroundSharedActive > 0
	m.globalOperationMutex.Unlock()
	if active {
		return true
	}
	m.deviceOperationMutex.Lock()
	defer m.deviceOperationMutex.Unlock()
	for _, operation := range m.activeDeviceOperations {
		if operation.kind == deviceOperationForeground {
			return true
		}
	}
	return false
}

// cancelRecoveryForForeground makes a read-only recovery yield its GATT slot.
// The recovery scheduler will retry it without counting the cancellation as a
// station failure.
func (m *Manager) cancelRecoveryForForeground() {
	m.recoveryOperationMutex.Lock()
	cancel := m.cancelRecovery
	m.recoveryOperationMutex.Unlock()
	if cancel != nil {
		cancel()
	}
}

// IsBusy reports whether an exclusive global operation (a scan or a bulk
// power batch) currently holds the global operation lock. Shared work such
// as status refreshes, single-station commands, and configuration writes
// does not make it return true.
func (m *Manager) IsBusy() bool {
	m.globalOperationMutex.Lock()
	defer m.globalOperationMutex.Unlock()
	return m.exclusiveOperationActive
}

// GetStationInfo returns the current state of the stations map.
