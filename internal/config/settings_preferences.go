package config

import (
	"fmt"
	"lhcontrol/internal/autosleep"
	"time"
)

func (c *Config) GetAutoSleep() autosleep.Settings {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return c.AutoSleep
}

// SetAutoSleep validates and persists the automatic-sleep settings, rolling
// the value back if persistence fails.
func (c *Config) SetAutoSleep(settings autosleep.Settings) error {
	if err := settings.Validate(); err != nil {
		return err
	}
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.recoverBlockedPersistenceLocked()
	previous := c.AutoSleep
	c.AutoSleep = settings
	if err := c.saveLocked(); err != nil {
		c.AutoSleep = previous
		return err
	}
	return nil
}

// GetLanguage returns the explicitly persisted UI language. An empty value
// means that the frontend should follow the operating-system language.
func (c *Config) GetLanguage() string {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return c.Language
}

// SetLanguage validates and persists the UI language, rolling the in-memory
// value back when the atomic configuration write fails.
func (c *Config) SetLanguage(language string) error {
	if language != "" && language != LanguageEnglish && language != LanguageSimplifiedChinese {
		return fmt.Errorf("unsupported language %q", language)
	}
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.recoverBlockedPersistenceLocked()
	previous := c.Language
	c.Language = language
	if err := c.saveLocked(); err != nil {
		c.Language = previous
		return err
	}
	return nil
}

func (c *Config) GetScanOnStartup() bool {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return c.ScanOnStartup
}

func (c *Config) SetScanOnStartup(enabled bool) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.recoverBlockedPersistenceLocked()
	previous := c.ScanOnStartup
	c.ScanOnStartup = enabled
	if err := c.saveLocked(); err != nil {
		c.ScanOnStartup = previous
		return err
	}
	return nil
}

func (c *Config) GetScanDurationSeconds() int {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	if c.ScanDurationSeconds < MinScanDurationSeconds || c.ScanDurationSeconds > MaxScanDurationSeconds {
		return DefaultScanDurationSeconds
	}
	return c.ScanDurationSeconds
}

func (c *Config) ScanDuration() time.Duration {
	return time.Duration(c.GetScanDurationSeconds()) * time.Second
}

func (c *Config) SetScanDurationSeconds(durationSeconds int) error {
	if durationSeconds < MinScanDurationSeconds || durationSeconds > MaxScanDurationSeconds {
		return fmt.Errorf(
			"scan duration must be between %d and %d seconds, got %d",
			MinScanDurationSeconds,
			MaxScanDurationSeconds,
			durationSeconds,
		)
	}
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.recoverBlockedPersistenceLocked()
	previous := c.ScanDurationSeconds
	c.ScanDurationSeconds = durationSeconds
	if err := c.saveLocked(); err != nil {
		c.ScanDurationSeconds = previous
		return err
	}
	return nil
}

func (c *Config) GetStatusPollingEnabled() bool {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return c.StatusPollingEnabled
}

func (c *Config) SetStatusPollingEnabled(enabled bool) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.recoverBlockedPersistenceLocked()
	previous := c.StatusPollingEnabled
	c.StatusPollingEnabled = enabled
	if err := c.saveLocked(); err != nil {
		c.StatusPollingEnabled = previous
		return err
	}
	return nil
}

func (c *Config) GetBulkPowerTimeoutSeconds() int {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	if c.BulkPowerTimeoutSeconds < MinBulkPowerTimeoutSeconds || c.BulkPowerTimeoutSeconds > MaxBulkPowerTimeoutSeconds {
		return DefaultBulkPowerTimeoutSeconds
	}
	return c.BulkPowerTimeoutSeconds
}

func (c *Config) BulkPowerTimeout() time.Duration {
	return time.Duration(c.GetBulkPowerTimeoutSeconds()) * time.Second
}

func (c *Config) SetBulkPowerTimeoutSeconds(timeoutSeconds int) error {
	if timeoutSeconds < MinBulkPowerTimeoutSeconds || timeoutSeconds > MaxBulkPowerTimeoutSeconds {
		return fmt.Errorf(
			"bulk power timeout must be between %d and %d seconds, got %d",
			MinBulkPowerTimeoutSeconds,
			MaxBulkPowerTimeoutSeconds,
			timeoutSeconds,
		)
	}
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.recoverBlockedPersistenceLocked()
	stationTimeout := sanitizeRangedInt(&c.StationOperationTimeoutSeconds, MinStationOperationTimeoutSeconds, MaxStationOperationTimeoutSeconds, DefaultStationOperationTimeoutSeconds)
	if timeoutSeconds < stationTimeout {
		return fmt.Errorf(
			"bulk power timeout must cover the per-station operation timeout of %d seconds, got %d",
			stationTimeout,
			timeoutSeconds,
		)
	}
	previous := c.BulkPowerTimeoutSeconds
	c.BulkPowerTimeoutSeconds = timeoutSeconds
	if err := c.saveLocked(); err != nil {
		c.BulkPowerTimeoutSeconds = previous
		return err
	}
	return nil
}

func (c *Config) GetStatusPollIntervalSeconds() int {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	if c.StatusPollIntervalSeconds < MinStatusPollIntervalSeconds || c.StatusPollIntervalSeconds > MaxStatusPollIntervalSeconds {
		return DefaultStatusPollIntervalSeconds
	}
	return c.StatusPollIntervalSeconds
}

func (c *Config) SetStatusPollIntervalSeconds(intervalSeconds int) error {
	if intervalSeconds < MinStatusPollIntervalSeconds || intervalSeconds > MaxStatusPollIntervalSeconds {
		return fmt.Errorf(
			"status poll interval must be between %d and %d seconds, got %d",
			MinStatusPollIntervalSeconds,
			MaxStatusPollIntervalSeconds,
			intervalSeconds,
		)
	}
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.recoverBlockedPersistenceLocked()
	previous := c.StatusPollIntervalSeconds
	c.StatusPollIntervalSeconds = intervalSeconds
	if err := c.saveLocked(); err != nil {
		c.StatusPollIntervalSeconds = previous
		return err
	}
	return nil
}

func (c *Config) GetStationOperationTimeoutSeconds() int {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	if c.StationOperationTimeoutSeconds < MinStationOperationTimeoutSeconds || c.StationOperationTimeoutSeconds > MaxStationOperationTimeoutSeconds {
		return DefaultStationOperationTimeoutSeconds
	}
	return c.StationOperationTimeoutSeconds
}

func (c *Config) StationOperationTimeout() time.Duration {
	return time.Duration(c.GetStationOperationTimeoutSeconds()) * time.Second
}

func (c *Config) SetStationOperationTimeoutSeconds(timeoutSeconds int) error {
	if timeoutSeconds < MinStationOperationTimeoutSeconds || timeoutSeconds > MaxStationOperationTimeoutSeconds {
		return fmt.Errorf(
			"station operation timeout must be between %d and %d seconds, got %d",
			MinStationOperationTimeoutSeconds,
			MaxStationOperationTimeoutSeconds,
			timeoutSeconds,
		)
	}
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.recoverBlockedPersistenceLocked()
	bulkTimeout := sanitizeRangedInt(&c.BulkPowerTimeoutSeconds, MinBulkPowerTimeoutSeconds, MaxBulkPowerTimeoutSeconds, DefaultBulkPowerTimeoutSeconds)
	if timeoutSeconds > bulkTimeout {
		return fmt.Errorf(
			"station operation timeout cannot exceed the bulk power timeout of %d seconds, got %d",
			bulkTimeout,
			timeoutSeconds,
		)
	}
	initialReadTimeout := sanitizeRangedInt(&c.InitialReadTimeoutSeconds, MinInitialReadTimeoutSeconds, MaxInitialReadTimeoutSeconds, DefaultInitialReadTimeoutSeconds)
	if timeoutSeconds < initialReadTimeout {
		return fmt.Errorf(
			"station operation timeout must cover the initial read timeout of %d seconds, got %d",
			initialReadTimeout,
			timeoutSeconds,
		)
	}
	previous := c.StationOperationTimeoutSeconds
	c.StationOperationTimeoutSeconds = timeoutSeconds
	if err := c.saveLocked(); err != nil {
		c.StationOperationTimeoutSeconds = previous
		return err
	}
	return nil
}
