package station

import "lhcontrol/internal/bluetooth"

// ApplyPresenceMissThreshold re-evaluates cached presence immediately after
// the user changes the consecutive-miss threshold. Raising the threshold can
// revive a disconnected station whose recovery was previously exhausted, so
// schedule a fresh recovery attempt for that newly present station, rebasing
// any stale absent-era backoff deadlines to run immediately.
func (m *Manager) ApplyPresenceMissThreshold() {
	threshold := m.config.GetPresenceMissThreshold()
	m.stationsMutex.RLock()
	stations := make([]*bluetooth.BaseStation, 0, len(m.stations))
	for _, station := range m.stations {
		if station != nil {
			stations = append(stations, station)
		}
	}
	m.stationsMutex.RUnlock()

	for _, current := range stations {
		if !current.ApplyPresenceMissThreshold(threshold) {
			continue
		}
		snapshot := current.Snapshot()
		if snapshot.Present && !snapshot.Connected {
			m.rebaseRecoveryForRevivedStation(snapshot.Address)
		} else if !snapshot.Present {
			m.stopExhaustedAbsentRecovery(snapshot.Address, current)
		}
	}
}
