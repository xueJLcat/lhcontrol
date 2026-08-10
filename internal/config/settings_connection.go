package config

import (
	"fmt"
	"time"
)

func (c *Config) GetDiscoveryAttempts() int {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return sanitizeRangedInt(&c.DiscoveryAttempts, MinDiscoveryAttempts, MaxDiscoveryAttempts, DefaultDiscoveryAttempts)
}

func (c *Config) SetDiscoveryAttempts(attempts int) error {
	if attempts < MinDiscoveryAttempts || attempts > MaxDiscoveryAttempts {
		return fmt.Errorf(
			"discovery attempts must be between %d and %d, got %d",
			MinDiscoveryAttempts,
			MaxDiscoveryAttempts,
			attempts,
		)
	}
	c.mutex.Lock()
	defer c.mutex.Unlock()
	previous := c.DiscoveryAttempts
	c.DiscoveryAttempts = attempts
	if err := c.saveLocked(); err != nil {
		c.DiscoveryAttempts = previous
		return err
	}
	return nil
}

func (c *Config) GetDiscoveryRetryDelayMs() int {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return sanitizeRangedInt(&c.DiscoveryRetryDelayMs, MinDiscoveryRetryDelayMs, MaxDiscoveryRetryDelayMs, DefaultDiscoveryRetryDelayMs)
}

func (c *Config) DiscoveryRetryDelay() time.Duration {
	return time.Duration(c.GetDiscoveryRetryDelayMs()) * time.Millisecond
}

func (c *Config) SetDiscoveryRetryDelayMs(delayMs int) error {
	if delayMs < MinDiscoveryRetryDelayMs || delayMs > MaxDiscoveryRetryDelayMs {
		return fmt.Errorf(
			"discovery retry delay must be between %d and %d milliseconds, got %d",
			MinDiscoveryRetryDelayMs,
			MaxDiscoveryRetryDelayMs,
			delayMs,
		)
	}
	c.mutex.Lock()
	defer c.mutex.Unlock()
	previous := c.DiscoveryRetryDelayMs
	c.DiscoveryRetryDelayMs = delayMs
	if err := c.saveLocked(); err != nil {
		c.DiscoveryRetryDelayMs = previous
		return err
	}
	return nil
}
