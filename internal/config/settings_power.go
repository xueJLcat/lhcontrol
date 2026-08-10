package config

import (
	"fmt"
	"time"
)

func (c *Config) GetPowerConfirmAttemptsOn() int {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return sanitizeRangedInt(&c.PowerConfirmAttemptsOn, MinPowerConfirmAttempts, MaxPowerConfirmAttempts, DefaultPowerConfirmAttemptsOn)
}

func (c *Config) SetPowerConfirmAttemptsOn(attempts int) error {
	if attempts < MinPowerConfirmAttempts || attempts > MaxPowerConfirmAttempts {
		return fmt.Errorf(
			"power-on confirmation attempts must be between %d and %d, got %d",
			MinPowerConfirmAttempts,
			MaxPowerConfirmAttempts,
			attempts,
		)
	}
	c.mutex.Lock()
	defer c.mutex.Unlock()
	previous := c.PowerConfirmAttemptsOn
	c.PowerConfirmAttemptsOn = attempts
	if err := c.saveLocked(); err != nil {
		c.PowerConfirmAttemptsOn = previous
		return err
	}
	return nil
}

func (c *Config) GetPowerConfirmAttemptsOff() int {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return sanitizeRangedInt(&c.PowerConfirmAttemptsOff, MinPowerConfirmAttempts, MaxPowerConfirmAttempts, DefaultPowerConfirmAttemptsOff)
}

func (c *Config) SetPowerConfirmAttemptsOff(attempts int) error {
	if attempts < MinPowerConfirmAttempts || attempts > MaxPowerConfirmAttempts {
		return fmt.Errorf(
			"power-off confirmation attempts must be between %d and %d, got %d",
			MinPowerConfirmAttempts,
			MaxPowerConfirmAttempts,
			attempts,
		)
	}
	c.mutex.Lock()
	defer c.mutex.Unlock()
	previous := c.PowerConfirmAttemptsOff
	c.PowerConfirmAttemptsOff = attempts
	if err := c.saveLocked(); err != nil {
		c.PowerConfirmAttemptsOff = previous
		return err
	}
	return nil
}

func (c *Config) GetPowerConfirmPollIntervalMs() int {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return sanitizeRangedInt(&c.PowerConfirmPollIntervalMs, MinPowerConfirmPollIntervalMs, MaxPowerConfirmPollIntervalMs, DefaultPowerConfirmPollIntervalMs)
}

func (c *Config) PowerConfirmPollInterval() time.Duration {
	return time.Duration(c.GetPowerConfirmPollIntervalMs()) * time.Millisecond
}

func (c *Config) SetPowerConfirmPollIntervalMs(intervalMs int) error {
	if intervalMs < MinPowerConfirmPollIntervalMs || intervalMs > MaxPowerConfirmPollIntervalMs {
		return fmt.Errorf(
			"power confirmation poll interval must be between %d and %d milliseconds, got %d",
			MinPowerConfirmPollIntervalMs,
			MaxPowerConfirmPollIntervalMs,
			intervalMs,
		)
	}
	c.mutex.Lock()
	defer c.mutex.Unlock()
	previous := c.PowerConfirmPollIntervalMs
	c.PowerConfirmPollIntervalMs = intervalMs
	if err := c.saveLocked(); err != nil {
		c.PowerConfirmPollIntervalMs = previous
		return err
	}
	return nil
}

func (c *Config) GetBootFallbackSeconds() int {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return sanitizeRangedInt(&c.BootFallbackSeconds, MinBootFallbackSeconds, MaxBootFallbackSeconds, DefaultBootFallbackSeconds)
}

func (c *Config) BootFallback() time.Duration {
	return time.Duration(c.GetBootFallbackSeconds()) * time.Second
}

func (c *Config) SetBootFallbackSeconds(fallbackSeconds int) error {
	if fallbackSeconds < MinBootFallbackSeconds || fallbackSeconds > MaxBootFallbackSeconds {
		return fmt.Errorf(
			"boot fallback window must be between %d and %d seconds, got %d",
			MinBootFallbackSeconds,
			MaxBootFallbackSeconds,
			fallbackSeconds,
		)
	}
	c.mutex.Lock()
	defer c.mutex.Unlock()
	previous := c.BootFallbackSeconds
	c.BootFallbackSeconds = fallbackSeconds
	if err := c.saveLocked(); err != nil {
		c.BootFallbackSeconds = previous
		return err
	}
	return nil
}

func (c *Config) GetSleepFinalWriteTimeoutSeconds() int {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return sanitizeRangedInt(&c.SleepFinalWriteTimeoutSeconds, MinSleepFinalWriteTimeoutSeconds, MaxSleepFinalWriteTimeoutSeconds, DefaultSleepFinalWriteTimeoutSeconds)
}

func (c *Config) SleepFinalWriteTimeout() time.Duration {
	return time.Duration(c.GetSleepFinalWriteTimeoutSeconds()) * time.Second
}

func (c *Config) SetSleepFinalWriteTimeoutSeconds(timeoutSeconds int) error {
	if timeoutSeconds < MinSleepFinalWriteTimeoutSeconds || timeoutSeconds > MaxSleepFinalWriteTimeoutSeconds {
		return fmt.Errorf(
			"sleep final write timeout must be between %d and %d seconds, got %d",
			MinSleepFinalWriteTimeoutSeconds,
			MaxSleepFinalWriteTimeoutSeconds,
			timeoutSeconds,
		)
	}
	c.mutex.Lock()
	defer c.mutex.Unlock()
	previous := c.SleepFinalWriteTimeoutSeconds
	c.SleepFinalWriteTimeoutSeconds = timeoutSeconds
	if err := c.saveLocked(); err != nil {
		c.SleepFinalWriteTimeoutSeconds = previous
		return err
	}
	return nil
}

func (c *Config) GetSleepPrepareGapMs() int {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return sanitizeRangedInt(&c.SleepPrepareGapMs, MinSleepPrepareGapMs, MaxSleepPrepareGapMs, DefaultSleepPrepareGapMs)
}

func (c *Config) SleepPrepareGap() time.Duration {
	return time.Duration(c.GetSleepPrepareGapMs()) * time.Millisecond
}

func (c *Config) SetSleepPrepareGapMs(gapMs int) error {
	if gapMs < MinSleepPrepareGapMs || gapMs > MaxSleepPrepareGapMs {
		return fmt.Errorf(
			"sleep prepare gap must be between %d and %d milliseconds, got %d",
			MinSleepPrepareGapMs,
			MaxSleepPrepareGapMs,
			gapMs,
		)
	}
	c.mutex.Lock()
	defer c.mutex.Unlock()
	previous := c.SleepPrepareGapMs
	c.SleepPrepareGapMs = gapMs
	if err := c.saveLocked(); err != nil {
		c.SleepPrepareGapMs = previous
		return err
	}
	return nil
}
