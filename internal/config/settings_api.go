package config

import "strings"

func (c *Config) GetAPIListenAddress() string {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return sanitizeAPIListenAddress(c.APIListenAddress)
}

func (c *Config) SetAPIListenAddress(address string) error {
	address = strings.TrimSpace(address)
	if err := validateAPIListenAddress(address); err != nil {
		return err
	}
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.recoverBlockedPersistenceLocked()
	// Compare the stored value (not the sanitized getter) so a re-save also
	// repairs an invalid residual value that the getter masks with the
	// default. The short-circuit must not apply while a persistence error is
	// pending: rewriting the unchanged value is exactly what clears it.
	if c.APIListenAddress == address && c.lastPersistenceErr == nil && c.persistenceBlockedErr == nil {
		return nil
	}
	previous := c.APIListenAddress
	c.APIListenAddress = address
	if err := c.saveLocked(); err != nil {
		c.APIListenAddress = previous
		return err
	}
	return nil
}

// rangedGet reads an integer setting, repairing out-of-range values with the
// default without mutating the stored state.
