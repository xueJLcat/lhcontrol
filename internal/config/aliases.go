package config

func (c *Config) GetRenamedStation(originalName string) (string, bool) {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	renamedName, ok := c.RenamedStations[originalName]
	return renamedName, ok
}

// SetRenamedStation updates a local display name and persists the config.
func (c *Config) SetRenamedStation(originalName string, newName string) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.recoverBlockedPersistenceLocked()
	if c.RenamedStations == nil {
		c.RenamedStations = make(map[string]string)
	}
	previous, existed := c.RenamedStations[originalName]
	if newName == "" {
		delete(c.RenamedStations, originalName)
	} else {
		c.RenamedStations[originalName] = newName
	}
	if err := c.saveLocked(); err != nil {
		if existed {
			c.RenamedStations[originalName] = previous
		} else {
			delete(c.RenamedStations, originalName)
		}
		return err
	}
	return nil
}

// SetRenamedStationForAddresses keeps the legacy name-based API effective
// without clobbering per-device choices. A per-address entry that differs
// from the previous legacy alias is an explicit user decision (a dedicated
// alias or an explicit reset tombstone) and is left untouched; entries that
// mirror the previous legacy alias (or do not exist) follow the rename, and
// a legacy clear removes those mirrors so the affected devices fall back to
// their factory names.
func (c *Config) SetRenamedStationForAddresses(originalName, newName string, addresses []string) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.recoverBlockedPersistenceLocked()
	if c.RenamedStations == nil {
		c.RenamedStations = make(map[string]string)
	}
	if c.RenamedStationsByAddress == nil {
		c.RenamedStationsByAddress = make(map[string]string)
	}

	previousLegacy, legacyExisted := c.RenamedStations[originalName]
	// Snapshot every original value before mutating anything. An interleaved
	// snapshot/mutation loop would capture the value written by a previous
	// iteration when addresses repeat, and a failed save would then roll back
	// to that mutated value instead of the persisted one.
	previousAddresses := make(map[string]string, len(addresses))
	existingAddresses := make(map[string]bool, len(addresses))
	for _, address := range addresses {
		if _, snapshotted := existingAddresses[address]; snapshotted {
			continue
		}
		previousAddresses[address], existingAddresses[address] = c.RenamedStationsByAddress[address]
	}
	for _, address := range addresses {
		previousAddress, hasAddress := c.RenamedStationsByAddress[address]
		// An empty entry is by construction a user tombstone (an explicit
		// reset), including after the legacy entry it shadowed was cleared;
		// only entries mirroring the previous legacy alias follow the rename.
		explicitChoice := hasAddress && (previousAddress == "" || previousAddress != previousLegacy)
		if explicitChoice {
			continue
		}
		if newName == "" {
			delete(c.RenamedStationsByAddress, address)
		} else {
			c.RenamedStationsByAddress[address] = newName
		}
	}
	if newName == "" {
		delete(c.RenamedStations, originalName)
	} else {
		c.RenamedStations[originalName] = newName
	}

	if err := c.saveLocked(); err != nil {
		if legacyExisted {
			c.RenamedStations[originalName] = previousLegacy
		} else {
			delete(c.RenamedStations, originalName)
		}
		for _, address := range addresses {
			if existingAddresses[address] {
				c.RenamedStationsByAddress[address] = previousAddresses[address]
			} else {
				delete(c.RenamedStationsByAddress, address)
			}
		}
		return err
	}
	return nil
}

// GetStationDisplayName uses the stable BLE address first and falls back to
// legacy name-keyed entries so existing configurations continue to work.
func (c *Config) GetStationDisplayName(address, originalName string) (string, bool) {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	if renamedName, ok := c.RenamedStationsByAddress[address]; ok {
		if renamedName == "" {
			return originalName, false
		}
		return renamedName, true
	}
	renamedName, ok := c.RenamedStations[originalName]
	return renamedName, ok
}

func (c *Config) SetRenamedStationByAddress(address, originalName, newName string) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.recoverBlockedPersistenceLocked()
	if c.RenamedStationsByAddress == nil {
		c.RenamedStationsByAddress = make(map[string]string)
	}
	previousAddressName, addressExisted := c.RenamedStationsByAddress[address]
	_, legacyExisted := c.RenamedStations[originalName]
	if newName == "" {
		switch {
		case legacyExisted:
			// An empty address entry is a tombstone for this device only. It
			// prevents the shared legacy name from being applied again without
			// removing that alias from other devices with the same factory name.
			c.RenamedStationsByAddress[address] = ""
		case originalName == "":
			// The factory name is unknown (the station has not been scanned
			// this session), so a legacy alias may still apply to this device
			// even when this setter cannot see it. Keep the existing entry as
			// a tombstone: deleting it would silently resurrect the legacy
			// alias once a scan rediscovers the device. With no existing entry
			// there is nothing to suppress or remove; a blocked persistence
			// still reports the block like every other setter instead of
			// claiming success.
			if !addressExisted {
				if c.persistenceBlockedErr != nil {
					return blockedSaveError(c.persistenceBlockedErr)
				}
				return nil
			}
			c.RenamedStationsByAddress[address] = ""
		default:
			delete(c.RenamedStationsByAddress, address)
		}
	} else {
		c.RenamedStationsByAddress[address] = newName
	}
	if err := c.saveLocked(); err != nil {
		// The legacy map is read but never mutated here, so the rollback only
		// needs to restore the address entry.
		if addressExisted {
			c.RenamedStationsByAddress[address] = previousAddressName
		} else {
			delete(c.RenamedStationsByAddress, address)
		}
		return err
	}
	return nil
}

// GetAutoSleep returns the persisted automatic-sleep settings. The returned
// value is always valid; invalid persisted data is repaired at load time.
