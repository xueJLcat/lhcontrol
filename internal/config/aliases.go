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

// SetRenamedStationForAddresses keeps the legacy name-based API effective for
// stations that already have a more specific address alias.
func (c *Config) SetRenamedStationForAddresses(originalName, newName string, addresses []string) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
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
	if c.RenamedStationsByAddress == nil {
		c.RenamedStationsByAddress = make(map[string]string)
	}
	previousAddressName, addressExisted := c.RenamedStationsByAddress[address]
	previousLegacyName, legacyExisted := c.RenamedStations[originalName]
	if newName == "" {
		if legacyExisted {
			// An empty address entry is a tombstone for this device only. It
			// prevents the shared legacy name from being applied again without
			// removing that alias from other devices with the same factory name.
			c.RenamedStationsByAddress[address] = ""
		} else {
			delete(c.RenamedStationsByAddress, address)
		}
	} else {
		c.RenamedStationsByAddress[address] = newName
	}
	if err := c.saveLocked(); err != nil {
		if addressExisted {
			c.RenamedStationsByAddress[address] = previousAddressName
		} else {
			delete(c.RenamedStationsByAddress, address)
		}
		if legacyExisted {
			c.RenamedStations[originalName] = previousLegacyName
		} else {
			delete(c.RenamedStations, originalName)
		}
		return err
	}
	return nil
}

// GetAutoSleep returns the persisted automatic-sleep settings. The returned
// value is always valid; invalid persisted data is repaired at load time.
