package config

import "strings"

func (c *Config) GetAPIListenAddress() string {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return sanitizeAPIListenAddress(c.APIListenAddress)
}

func (c *Config) SetAPIListenAddress(address string) error {
	_, err := c.SetAPIListenAddressWithPrevious(address)
	return err
}

// SetAPIListenAddressWithPrevious persists the address like
// SetAPIListenAddress and also reports the effective value in place
// immediately before the write, captured after any blocked-save recovery and
// under the same lock. A recovery inside the setter can restore a different
// persisted address after the caller last read it; a caller rolling back a
// failed listener switch must restore that restored baseline — not a stale
// pre-recovery read — or it would overwrite the address the recovery just
// surfaced.
func (c *Config) SetAPIListenAddressWithPrevious(address string) (string, error) {
	address = strings.TrimSpace(address)
	if err := validateAPIListenAddress(address); err != nil {
		return "", err
	}
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.recoverBlockedPersistenceLocked()
	// The effective baseline the caller would observe through the getter: the
	// sanitized value, masking any invalid residual with the default.
	previous := sanitizeAPIListenAddress(c.APIListenAddress)
	// Compare the stored value (not the sanitized getter) so a re-save also
	// repairs an invalid residual value that the getter masks with the
	// default. The short-circuit must not apply while a persistence error is
	// pending: rewriting the unchanged value is exactly what clears it.
	if c.APIListenAddress == address && c.lastPersistenceErr == nil && c.persistenceBlockedErr == nil {
		return previous, nil
	}
	rawPrevious := c.APIListenAddress
	c.APIListenAddress = address
	if err := c.saveLocked(); err != nil {
		c.APIListenAddress = rawPrevious
		return "", err
	}
	return previous, nil
}

// rangedGet reads an integer setting, repairing out-of-range values with the
// default without mutating the stored state.
