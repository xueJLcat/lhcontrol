package station

import (
	"fmt"
	"lhcontrol/internal/bluetooth"
	"log"
	"time"
)

func (m *Manager) RenameStation(originalName string, newName string) error {
	if err := m.beginForegroundSharedOperation(); err != nil {
		return err
	}
	defer m.endForegroundSharedOperation()
	// Copy only the pointers under the map lock and snapshot them afterwards:
	// Snapshot takes each station's own mutex, which a wedged WinRT cleanup can
	// hold far longer than this operation should block on the fleet map lock.
	m.stationsMutex.RLock()
	stationPtrs := make([]*bluetooth.BaseStation, 0, len(m.stations))
	for _, stationPtr := range m.stations {
		if stationPtr == nil {
			continue
		}
		stationPtrs = append(stationPtrs, stationPtr)
	}
	m.stationsMutex.RUnlock()
	addresses := make([]string, 0)
	for _, stationPtr := range stationPtrs {
		snapshot := stationPtr.Snapshot()
		if snapshot.Name == originalName {
			addresses = append(addresses, snapshot.Address)
		}
	}
	if len(addresses) == 0 {
		return fmt.Errorf("%w: no station has original name %q", ErrNotFound, originalName)
	}
	return m.config.SetRenamedStationForAddresses(originalName, newName, addresses)
}
func (m *Manager) RenameStationByAddress(address, newName string) error {
	if err := m.beginForegroundSharedOperation(); err != nil {
		return err
	}
	defer m.endForegroundSharedOperation()
	station, err := m.stationByAddress(address)
	if err != nil {
		// The station has not been discovered in this session (no successful
		// scan yet, Bluetooth unavailable, or scan-on-startup disabled). Still
		// allow renaming by address so callers can manage aliases for known
		// devices. There is no scan-known original name to preserve, so no
		// legacy per-name tombstone applies; a malformed address is rejected.
		canonical, ok := bluetooth.CanonicalAddress(address)
		if !ok {
			return err
		}
		return m.config.SetRenamedStationByAddress(canonical, "", newName)
	}
	snapshot := station.Snapshot()
	return m.config.SetRenamedStationByAddress(snapshot.Address, snapshot.Name, newName)
}
func (m *Manager) SaveConfig() error {
	if err := m.beginForegroundSharedOperation(); err != nil {
		return err
	}
	defer m.endForegroundSharedOperation()
	return m.config.Save()
}

// BeginShutdown rejects new work and requests cancellation of an active scan
// without waiting. App shutdown uses it before draining the local HTTP server.
func (m *Manager) BeginShutdown() {
	m.shutdownOnce.Do(func() {
		m.lifecycleMutex.Lock()
		m.shuttingDown.Store(true)
		m.cancelLifecycle()
		close(m.shutdownCh)
		m.lifecycleMutex.Unlock()
	})
	m.scanLifecycleMutex.Lock()
	lifecycle := m.scanLifecycle
	m.scanLifecycleMutex.Unlock()
	if lifecycle != nil {
		lifecycle.cancel()
	}
}

// shutdownDrainLimit bounds the exit drain. Adapter calls without context
// support (device disconnect and its cleanup retries among them) can block
// far longer than a closing window should wait; after the limit the process
// exits and the OS reclaims the handles. This matches the bounded waits the
// codebase already applies to CancelBulkPower and the API shutdown.
const shutdownDrainLimit = 60 * time.Second

func (m *Manager) Shutdown() {
	m.BeginShutdown()
	m.lifecycleMutex.Lock()
	if m.shutdownDraining == nil {
		// BeginShutdown is already idempotent, but the drain itself is not: a
		// second caller (desktop exit plus an API-triggered shutdown, or a
		// repeated call) would otherwise run a concurrent fleet disconnect
		// alongside the first drain. Publish one shared done channel and let
		// every caller wait on the single drain.
		m.shutdownDraining = make(chan struct{})
		go m.runShutdownDrain(m.shutdownDraining)
	}
	drained := m.shutdownDraining
	m.lifecycleMutex.Unlock()
	limit := m.shutdownDrainTimeout
	if limit <= 0 {
		limit = shutdownDrainLimit
	}
	drainTimer := time.NewTimer(limit)
	defer drainTimer.Stop()
	select {
	case <-drained:
	case <-drainTimer.C:
		log.Printf("Bluetooth shutdown drain exceeded %s; exiting without complete cleanup", limit)
	}
}

// runShutdownDrain performs the ordered shutdown cleanup exactly once and
// closes done when finished. It is started under the lifecycle lock by the
// first Shutdown caller; later callers wait on the same done channel.
func (m *Manager) runShutdownDrain(done chan struct{}) {
	defer close(done)
	if err := m.StopScan(); err != nil {
		log.Printf("Bluetooth scan cancellation was incomplete: %v", err)
	}
	m.statusRecoveryWg.Wait()
	m.lifecycleMutex.Lock()
	for m.activeOperations > 0 {
		m.lifecycleCond.Wait()
	}
	m.lifecycleMutex.Unlock()
	m.asyncScanWg.Wait()
	// scanCallbackWg is intentionally not awaited: shutdown may itself be
	// invoked from a scan callback, so waiting for callbacks would
	// self-deadlock. Event emissions are guarded by the caller's shutdown
	// flag instead.
	// Wrap the fleet disconnect in the same panic guard every other adapter
	// cleanup uses: a driver panic here would crash the process mid-drain
	// instead of finishing the shutdown bookkeeping and closing done.
	if err := runSafely("shutdown disconnect", bluetooth.DisconnectAllStations); err != nil {
		log.Printf("Bluetooth shutdown cleanup was incomplete: %v", err)
	}
}
