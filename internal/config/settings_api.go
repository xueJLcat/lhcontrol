package config

func (c *Config) GetAPIListenAddress() string {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return sanitizeAPIListenAddress(c.APIListenAddress)
}

func (c *Config) SetAPIListenAddress(address string) error {
	if err := validateAPIListenAddress(address); err != nil {
		return err
	}
	c.mutex.Lock()
	defer c.mutex.Unlock()
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
