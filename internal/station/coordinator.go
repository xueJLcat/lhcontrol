package station

import (
	"context"
	"errors"
	"strings"
)

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
	return nil
}
func (m *Manager) endOperation() {
	m.operationMutex.Unlock()
	m.unregisterOperation()
}

// beginForegroundGlobalOperation waits for manager-owned background status
// and recovery work, while preserving immediate Busy responses for another
// foreground global, device, or configuration operation.
func (m *Manager) beginForegroundGlobalOperation() error {
	return m.beginForegroundGlobalOperationContext(context.Background())
}
func (m *Manager) beginForegroundGlobalOperationContext(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := m.beginOperation()
		if err == nil {
			if contextErr := ctx.Err(); contextErr != nil {
				m.endOperation()
				return contextErr
			}
			m.globalOperationMutex.Lock()
			m.foregroundGlobalActive = true
			m.globalOperationMutex.Unlock()
			return nil
		}
		if !errors.Is(err, ErrOperationInProgress) {
			return err
		}
		m.globalOperationMutex.Lock()
		foregroundActive := m.foregroundGlobalActive
		m.globalOperationMutex.Unlock()
		if foregroundActive {
			return err
		}
		m.recoveryOperationMutex.Lock()
		recoveryDone := m.recoveryOperationDone
		m.recoveryOperationMutex.Unlock()
		m.statusLifecycleMutex.Lock()
		statusDone := m.statusOperationDone
		m.statusLifecycleMutex.Unlock()
		var backgroundDone <-chan struct{}
		if recoveryDone != nil {
			backgroundDone = recoveryDone
		} else if statusDone != nil {
			backgroundDone = statusDone
		}
		if backgroundDone == nil {
			return err
		}
		m.cancelBackgroundReadsForForeground()
		select {
		case <-backgroundDone:
		case <-ctx.Done():
			return ctx.Err()
		case <-m.shutdownCh:
			return ErrShuttingDown
		}
	}
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
	close(active.done)
	m.deviceOperationMutex.Unlock()
	<-m.deviceOperationSlots
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
	m.scanTransitionMutex.Lock()
	defer m.scanTransitionMutex.Unlock()
	if m.isScanning.Load() {
		return ErrOperationInProgress
	}
	for {
		m.recoveryOperationMutex.Lock()
		generationBefore := m.recoveryGeneration
		m.recoveryOperationMutex.Unlock()
		err := m.beginStationOperation(address)
		if !errors.Is(err, ErrOperationInProgress) {
			return err
		}
		var deviceBusy *deviceOperationBusyError
		if errors.As(err, &deviceBusy) && deviceBusy.backgroundDone != nil {
			if deviceBusy.cancelBackground != nil {
				deviceBusy.cancelBackground()
			} else {
				m.cancelRecoveryForForeground()
			}
			select {
			case <-deviceBusy.backgroundDone:
				continue
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
		m.recoveryOperationMutex.Unlock()
		if generationAfter != generationBefore {
			continue
		}
		if done == nil {
			return err
		}
		m.cancelRecoveryForForeground()
		select {
		case <-done:
		case <-m.shutdownCh:
			return ErrShuttingDown
		}
	}
}
func (m *Manager) beginBulkGlobalOperation() error {
	return m.beginBulkGlobalOperationContext(context.Background())
}
func (m *Manager) beginBulkGlobalOperationContext(ctx context.Context) error {
	m.scanTransitionMutex.Lock()
	defer m.scanTransitionMutex.Unlock()
	if m.isScanning.Load() {
		return ErrOperationInProgress
	}
	return m.beginForegroundGlobalOperationContext(ctx)
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
func (m *Manager) cancelBackgroundReadsForForeground() {
	m.statusLifecycleMutex.Lock()
	cancelStatus := m.cancelStatusOperation
	m.statusLifecycleMutex.Unlock()
	if cancelStatus != nil {
		cancelStatus()
	}
	m.cancelRecoveryForForeground()
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
	if !m.operationMutex.TryLock() {
		return true
	}
	m.operationMutex.Unlock()
	return false
}

// GetStationInfo returns the current state of the stations map.
