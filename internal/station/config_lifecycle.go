package station

import (
	"fmt"
	"lhcontrol/internal/bluetooth"
	"log"
)

func (m *Manager) RenameStation(originalName string, newName string) error {
	if err := m.beginForegroundSharedOperation(); err != nil {
		return err
	}
	defer m.endForegroundSharedOperation()
	addresses := make([]string, 0)
	m.stationsMutex.RLock()
	for _, stationPtr := range m.stations {
		if stationPtr == nil {
			continue
		}
		snapshot := stationPtr.Snapshot()
		if snapshot.Name == originalName {
			addresses = append(addresses, snapshot.Address)
		}
	}
	m.stationsMutex.RUnlock()
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
func (m *Manager) Shutdown() {
	m.BeginShutdown()
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
	if err := bluetooth.DisconnectAllStations(); err != nil {
		log.Printf("Bluetooth shutdown cleanup was incomplete: %v", err)
	}
}
