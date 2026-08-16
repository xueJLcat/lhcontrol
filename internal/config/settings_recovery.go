package config

import (
	"fmt"
	"time"
)

func (c *Config) GetRecoveryRetryBaseSeconds() int {
	return c.rangedGet(&c.RecoveryRetryBaseSeconds, MinRecoveryRetryBaseSeconds, MaxRecoveryRetryBaseSeconds, DefaultRecoveryRetryBaseSeconds)
}

func (c *Config) RecoveryRetryBase() time.Duration {
	return time.Duration(c.GetRecoveryRetryBaseSeconds()) * time.Second
}

func (c *Config) SetRecoveryRetryBaseSeconds(baseSeconds int) error {
	return c.setRanged("recovery retry base", baseSeconds, MinRecoveryRetryBaseSeconds, MaxRecoveryRetryBaseSeconds, &c.RecoveryRetryBaseSeconds,
		func(c *Config, value int) error {
			retryMax := sanitizeRangedInt(&c.RecoveryRetryMaxSeconds, MinRecoveryRetryMaxSeconds, MaxRecoveryRetryMaxSeconds, DefaultRecoveryRetryMaxSeconds)
			if value > retryMax {
				return fmt.Errorf("recovery retry base must not exceed the recovery retry maximum of %d seconds, got %d", retryMax, value)
			}
			return nil
		})
}

func (c *Config) GetRecoveryRetryMaxSeconds() int {
	return c.rangedGet(&c.RecoveryRetryMaxSeconds, MinRecoveryRetryMaxSeconds, MaxRecoveryRetryMaxSeconds, DefaultRecoveryRetryMaxSeconds)
}

func (c *Config) RecoveryRetryMax() time.Duration {
	return time.Duration(c.GetRecoveryRetryMaxSeconds()) * time.Second
}

func (c *Config) SetRecoveryRetryMaxSeconds(maxSeconds int) error {
	return c.setRanged("recovery retry maximum", maxSeconds, MinRecoveryRetryMaxSeconds, MaxRecoveryRetryMaxSeconds, &c.RecoveryRetryMaxSeconds,
		func(c *Config, value int) error {
			retryBase := sanitizeRangedInt(&c.RecoveryRetryBaseSeconds, MinRecoveryRetryBaseSeconds, MaxRecoveryRetryBaseSeconds, DefaultRecoveryRetryBaseSeconds)
			if value < retryBase {
				return fmt.Errorf("recovery retry maximum must not fall below the recovery retry base of %d seconds, got %d", retryBase, value)
			}
			return nil
		})
}

func (c *Config) GetAbsentStationRetryLimit() int {
	return c.rangedGet(&c.AbsentStationRetryLimit, MinAbsentStationRetryLimit, MaxAbsentStationRetryLimit, DefaultAbsentStationRetryLimit)
}

func (c *Config) SetAbsentStationRetryLimit(limit int) error {
	return c.setRanged("absent station retry limit", limit, MinAbsentStationRetryLimit, MaxAbsentStationRetryLimit, &c.AbsentStationRetryLimit, nil)
}

func (c *Config) GetInitialReadTimeoutSeconds() int {
	return c.rangedGet(&c.InitialReadTimeoutSeconds, MinInitialReadTimeoutSeconds, MaxInitialReadTimeoutSeconds, DefaultInitialReadTimeoutSeconds)
}

func (c *Config) InitialReadTimeout() time.Duration {
	return time.Duration(c.GetInitialReadTimeoutSeconds()) * time.Second
}

func (c *Config) SetInitialReadTimeoutSeconds(timeoutSeconds int) error {
	return c.setRanged("initial read timeout", timeoutSeconds, MinInitialReadTimeoutSeconds, MaxInitialReadTimeoutSeconds, &c.InitialReadTimeoutSeconds,
		func(c *Config, value int) error {
			stationTimeout := sanitizeRangedInt(&c.StationOperationTimeoutSeconds, MinStationOperationTimeoutSeconds, MaxStationOperationTimeoutSeconds, DefaultStationOperationTimeoutSeconds)
			if value > stationTimeout {
				return fmt.Errorf("initial read timeout must not exceed the station operation timeout of %d seconds, got %d", stationTimeout, value)
			}
			scanReadPhaseTimeout := sanitizeRangedInt(&c.ScanReadPhaseTimeoutSeconds, MinScanReadPhaseTimeoutSeconds, MaxScanReadPhaseTimeoutSeconds, DefaultScanReadPhaseTimeoutSeconds)
			if value > scanReadPhaseTimeout {
				return fmt.Errorf("initial read timeout must not exceed the scan read phase timeout of %d seconds, got %d", scanReadPhaseTimeout, value)
			}
			return nil
		})
}

func (c *Config) GetScanReadPhaseTimeoutSeconds() int {
	return c.rangedGet(&c.ScanReadPhaseTimeoutSeconds, MinScanReadPhaseTimeoutSeconds, MaxScanReadPhaseTimeoutSeconds, DefaultScanReadPhaseTimeoutSeconds)
}

func (c *Config) ScanReadPhaseTimeout() time.Duration {
	return time.Duration(c.GetScanReadPhaseTimeoutSeconds()) * time.Second
}

func (c *Config) SetScanReadPhaseTimeoutSeconds(timeoutSeconds int) error {
	return c.setRanged("scan read phase timeout", timeoutSeconds, MinScanReadPhaseTimeoutSeconds, MaxScanReadPhaseTimeoutSeconds, &c.ScanReadPhaseTimeoutSeconds,
		func(c *Config, value int) error {
			initialReadTimeout := sanitizeRangedInt(&c.InitialReadTimeoutSeconds, MinInitialReadTimeoutSeconds, MaxInitialReadTimeoutSeconds, DefaultInitialReadTimeoutSeconds)
			if value < initialReadTimeout {
				return fmt.Errorf("scan read phase timeout must cover the initial read timeout of %d seconds, got %d", initialReadTimeout, value)
			}
			return nil
		})
}

func (c *Config) GetStatusReadTimeoutSeconds() int {
	return c.rangedGet(&c.StatusReadTimeoutSeconds, MinStatusReadTimeoutSeconds, MaxStatusReadTimeoutSeconds, DefaultStatusReadTimeoutSeconds)
}

func (c *Config) StatusReadTimeout() time.Duration {
	return time.Duration(c.GetStatusReadTimeoutSeconds()) * time.Second
}

func (c *Config) SetStatusReadTimeoutSeconds(timeoutSeconds int) error {
	return c.setRanged("status read timeout", timeoutSeconds, MinStatusReadTimeoutSeconds, MaxStatusReadTimeoutSeconds, &c.StatusReadTimeoutSeconds,
		func(c *Config, value int) error {
			statusRefreshTimeout := sanitizeRangedInt(&c.StatusRefreshTimeoutSeconds, MinStatusRefreshTimeoutSeconds, MaxStatusRefreshTimeoutSeconds, DefaultStatusRefreshTimeoutSeconds)
			if value > statusRefreshTimeout {
				return fmt.Errorf("status read timeout must not exceed the status refresh timeout of %d seconds, got %d", statusRefreshTimeout, value)
			}
			return nil
		})
}

func (c *Config) GetStatusRefreshTimeoutSeconds() int {
	return c.rangedGet(&c.StatusRefreshTimeoutSeconds, MinStatusRefreshTimeoutSeconds, MaxStatusRefreshTimeoutSeconds, DefaultStatusRefreshTimeoutSeconds)
}

func (c *Config) StatusRefreshTimeout() time.Duration {
	return time.Duration(c.GetStatusRefreshTimeoutSeconds()) * time.Second
}

func (c *Config) SetStatusRefreshTimeoutSeconds(timeoutSeconds int) error {
	return c.setRanged("status refresh timeout", timeoutSeconds, MinStatusRefreshTimeoutSeconds, MaxStatusRefreshTimeoutSeconds, &c.StatusRefreshTimeoutSeconds,
		func(c *Config, value int) error {
			statusReadTimeout := sanitizeRangedInt(&c.StatusReadTimeoutSeconds, MinStatusReadTimeoutSeconds, MaxStatusReadTimeoutSeconds, DefaultStatusReadTimeoutSeconds)
			if value < statusReadTimeout {
				return fmt.Errorf("status refresh timeout must cover the status read timeout of %d seconds, got %d", statusReadTimeout, value)
			}
			return nil
		})
}

func (c *Config) GetChannelScanFreshnessSeconds() int {
	return c.rangedGet(&c.ChannelScanFreshnessSeconds, MinChannelScanFreshnessSeconds, MaxChannelScanFreshnessSeconds, DefaultChannelScanFreshnessSeconds)
}

func (c *Config) ChannelScanFreshness() time.Duration {
	return time.Duration(c.GetChannelScanFreshnessSeconds()) * time.Second
}

func (c *Config) SetChannelScanFreshnessSeconds(freshnessSeconds int) error {
	return c.setRanged("channel scan freshness", freshnessSeconds, MinChannelScanFreshnessSeconds, MaxChannelScanFreshnessSeconds, &c.ChannelScanFreshnessSeconds, nil)
}

func (c *Config) GetBluetoothInitRetrySeconds() int {
	return c.rangedGet(&c.BluetoothInitRetrySeconds, MinBluetoothInitRetrySeconds, MaxBluetoothInitRetrySeconds, DefaultBluetoothInitRetrySeconds)
}

func (c *Config) BluetoothInitRetry() time.Duration {
	return time.Duration(c.GetBluetoothInitRetrySeconds()) * time.Second
}

func (c *Config) SetBluetoothInitRetrySeconds(retrySeconds int) error {
	return c.setRanged("Bluetooth init retry", retrySeconds, MinBluetoothInitRetrySeconds, MaxBluetoothInitRetrySeconds, &c.BluetoothInitRetrySeconds, nil)
}

// StatusDisplayFreshnessWindow keeps displayed state valid across slow poll
// schedules without weakening the fixed freshness rule used before writes.
func (c *Config) StatusDisplayFreshnessWindow() time.Duration {
	intervalSeconds := c.GetStatusPollIntervalSeconds()
	seconds := 2*intervalSeconds + StatusPollJitterSeconds
	if seconds < MinimumDisplayFreshnessSeconds {
		seconds = MinimumDisplayFreshnessSeconds
	}
	return time.Duration(seconds) * time.Second
}
