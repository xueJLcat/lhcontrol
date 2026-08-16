package config

import (
	"fmt"
	"time"
)

func (c *Config) rangedGet(field *int, min, max, fallback int) int {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return sanitizeRangedInt(field, min, max, fallback)
}

// setRanged validates and persists an integer setting, rolling back the
// in-memory value when saving fails. The optional crossCheck runs under the
// write lock before mutation and receives the proposed value.
func (c *Config) setRanged(name string, value, min, max int, field *int, crossCheck func(c *Config, value int) error) error {
	if value < min || value > max {
		return fmt.Errorf("%s must be between %d and %d, got %d", name, min, max, value)
	}
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.recoverBlockedPersistenceLocked()
	if crossCheck != nil {
		if err := crossCheck(c, value); err != nil {
			return err
		}
	}
	previous := *field
	*field = value
	if err := c.saveLocked(); err != nil {
		*field = previous
		return err
	}
	return nil
}

func (c *Config) GetPowerWriteAttempts() int {
	return c.rangedGet(&c.PowerWriteAttempts, MinPowerWriteAttempts, MaxPowerWriteAttempts, DefaultPowerWriteAttempts)
}

func (c *Config) SetPowerWriteAttempts(attempts int) error {
	return c.setRanged("power write attempts", attempts, MinPowerWriteAttempts, MaxPowerWriteAttempts, &c.PowerWriteAttempts, nil)
}

func (c *Config) GetOperationRetryDelayMs() int {
	return c.rangedGet(&c.OperationRetryDelayMs, MinOperationRetryDelayMs, MaxOperationRetryDelayMs, DefaultOperationRetryDelayMs)
}

func (c *Config) OperationRetryDelay() time.Duration {
	return time.Duration(c.GetOperationRetryDelayMs()) * time.Millisecond
}

func (c *Config) SetOperationRetryDelayMs(delayMs int) error {
	return c.setRanged("operation retry delay", delayMs, MinOperationRetryDelayMs, MaxOperationRetryDelayMs, &c.OperationRetryDelayMs, nil)
}

func (c *Config) GetChannelConfirmAttempts() int {
	return c.rangedGet(&c.ChannelConfirmAttempts, MinChannelConfirmAttempts, MaxChannelConfirmAttempts, DefaultChannelConfirmAttempts)
}

func (c *Config) SetChannelConfirmAttempts(attempts int) error {
	return c.setRanged("channel confirmation attempts", attempts, MinChannelConfirmAttempts, MaxChannelConfirmAttempts, &c.ChannelConfirmAttempts, nil)
}

func (c *Config) GetChannelConfirmIntervalMs() int {
	return c.rangedGet(&c.ChannelConfirmIntervalMs, MinChannelConfirmIntervalMs, MaxChannelConfirmIntervalMs, DefaultChannelConfirmIntervalMs)
}

func (c *Config) ChannelConfirmInterval() time.Duration {
	return time.Duration(c.GetChannelConfirmIntervalMs()) * time.Millisecond
}

func (c *Config) SetChannelConfirmIntervalMs(intervalMs int) error {
	return c.setRanged("channel confirmation interval", intervalMs, MinChannelConfirmIntervalMs, MaxChannelConfirmIntervalMs, &c.ChannelConfirmIntervalMs, nil)
}

func (c *Config) GetConfirmReconnectThreshold() int {
	return c.rangedGet(&c.ConfirmReconnectThreshold, MinConfirmReconnectThreshold, MaxConfirmReconnectThreshold, DefaultConfirmReconnectThreshold)
}

func (c *Config) SetConfirmReconnectThreshold(threshold int) error {
	return c.setRanged("confirmation reconnect threshold", threshold, MinConfirmReconnectThreshold, MaxConfirmReconnectThreshold, &c.ConfirmReconnectThreshold, nil)
}

func (c *Config) GetConfirmReconnectDelayMs() int {
	return c.rangedGet(&c.ConfirmReconnectDelayMs, MinConfirmReconnectDelayMs, MaxConfirmReconnectDelayMs, DefaultConfirmReconnectDelayMs)
}

func (c *Config) ConfirmReconnectDelay() time.Duration {
	return time.Duration(c.GetConfirmReconnectDelayMs()) * time.Millisecond
}

func (c *Config) SetConfirmReconnectDelayMs(delayMs int) error {
	return c.setRanged("confirmation reconnect delay", delayMs, MinConfirmReconnectDelayMs, MaxConfirmReconnectDelayMs, &c.ConfirmReconnectDelayMs, nil)
}

func (c *Config) GetIdentifyAttempts() int {
	return c.rangedGet(&c.IdentifyAttempts, MinIdentifyAttempts, MaxIdentifyAttempts, DefaultIdentifyAttempts)
}

func (c *Config) SetIdentifyAttempts(attempts int) error {
	return c.setRanged("identify attempts", attempts, MinIdentifyAttempts, MaxIdentifyAttempts, &c.IdentifyAttempts, nil)
}

func (c *Config) GetPresenceMissThreshold() int {
	return c.rangedGet(&c.PresenceMissThreshold, MinPresenceMissThreshold, MaxPresenceMissThreshold, DefaultPresenceMissThreshold)
}

func (c *Config) SetPresenceMissThreshold(threshold int) error {
	return c.setRanged("presence miss threshold", threshold, MinPresenceMissThreshold, MaxPresenceMissThreshold, &c.PresenceMissThreshold, nil)
}
