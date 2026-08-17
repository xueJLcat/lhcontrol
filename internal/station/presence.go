package station

// ApplyPresenceMissThreshold re-evaluates cached presence immediately after
// the user changes the consecutive-miss threshold. Raising the threshold can
// revive a disconnected station whose recovery was previously exhausted, so
// schedule a fresh recovery attempt for that newly present station, rebasing
// any stale absent-era backoff deadlines to run immediately.
func (m *Manager) ApplyPresenceMissThreshold() {
	threshold := m.config.GetPresenceMissThreshold()
	for _, current := range m.stationPointers() {
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
