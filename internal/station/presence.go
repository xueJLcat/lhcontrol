package station

// ApplyPresenceMissThreshold re-evaluates cached presence immediately after
// the user changes the consecutive-miss threshold. Raising the threshold can
// revive a disconnected station whose recovery was previously exhausted, so
// schedule a fresh recovery attempt for that newly present station, rebasing
// any stale absent-era backoff deadlines to run immediately.
func (m *Manager) ApplyPresenceMissThreshold() {
	threshold := m.config.GetPresenceMissThreshold()
	for _, current := range m.stationPointers() {
		// The Try variant skips a station whose lock a wedged transport call
		// holds, deferring that station's reclassification instead of blocking
		// the whole pass; the next scan re-evaluates it.
		if !current.TryApplyPresenceMissThreshold(threshold) {
			continue
		}
		snapshot, ok := current.SnapshotNonBlocking()
		if !ok {
			continue
		}
		if snapshot.Present && !snapshot.Connected {
			m.rebaseRecoveryForRevivedStation(snapshot.Address)
		} else if !snapshot.Present {
			m.stopExhaustedAbsentRecovery(snapshot.Address, current)
		}
	}
}
