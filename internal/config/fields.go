package config

import "lhcontrol/internal/autosleep"

// intSettingBinding declares one ranged integer setting in a single place.
// Load sanitizes persisted values through the table, applyDefaults fills the
// fallbacks, and saveLocked builds the persisted snapshot from it, so adding
// a setting is one entry here instead of edits in three places.
type intSettingBinding struct {
	runtime   *int  // pointer into Config
	persisted **int // pointer to the persistedConfig *int field (nil for defaults)
	min       int
	max       int
	fallback  int
}

func binding(runtime *int, persisted **int, min, max, fallback int) intSettingBinding {
	return intSettingBinding{runtime: runtime, persisted: persisted, min: min, max: max, fallback: fallback}
}

// intSettingBindings is the single declaration of every ranged setting. The
// entry order matches the historical Load/save order. p may be nil when the
// table is used only to apply fallbacks to the runtime fields.
func (c *Config) intSettingBindings(p *persistedConfig) []intSettingBinding {
	if p == nil {
		return []intSettingBinding{
			binding(&c.ScanDurationSeconds, nil, MinScanDurationSeconds, MaxScanDurationSeconds, DefaultScanDurationSeconds),
			binding(&c.BulkPowerTimeoutSeconds, nil, MinBulkPowerTimeoutSeconds, MaxBulkPowerTimeoutSeconds, DefaultBulkPowerTimeoutSeconds),
			binding(&c.StatusPollIntervalSeconds, nil, MinStatusPollIntervalSeconds, MaxStatusPollIntervalSeconds, DefaultStatusPollIntervalSeconds),
			binding(&c.StationOperationTimeoutSeconds, nil, MinStationOperationTimeoutSeconds, MaxStationOperationTimeoutSeconds, DefaultStationOperationTimeoutSeconds),
			binding(&c.PowerConfirmAttemptsOn, nil, MinPowerConfirmAttempts, MaxPowerConfirmAttempts, DefaultPowerConfirmAttemptsOn),
			binding(&c.PowerConfirmAttemptsOff, nil, MinPowerConfirmAttempts, MaxPowerConfirmAttempts, DefaultPowerConfirmAttemptsOff),
			binding(&c.PowerConfirmPollIntervalMs, nil, MinPowerConfirmPollIntervalMs, MaxPowerConfirmPollIntervalMs, DefaultPowerConfirmPollIntervalMs),
			binding(&c.BootFallbackSeconds, nil, MinBootFallbackSeconds, MaxBootFallbackSeconds, DefaultBootFallbackSeconds),
			binding(&c.SleepFinalWriteTimeoutSeconds, nil, MinSleepFinalWriteTimeoutSeconds, MaxSleepFinalWriteTimeoutSeconds, DefaultSleepFinalWriteTimeoutSeconds),
			binding(&c.SleepPrepareGapMs, nil, MinSleepPrepareGapMs, MaxSleepPrepareGapMs, DefaultSleepPrepareGapMs),
			binding(&c.DiscoveryAttempts, nil, MinDiscoveryAttempts, MaxDiscoveryAttempts, DefaultDiscoveryAttempts),
			binding(&c.DiscoveryRetryDelayMs, nil, MinDiscoveryRetryDelayMs, MaxDiscoveryRetryDelayMs, DefaultDiscoveryRetryDelayMs),
			binding(&c.PowerWriteAttempts, nil, MinPowerWriteAttempts, MaxPowerWriteAttempts, DefaultPowerWriteAttempts),
			binding(&c.OperationRetryDelayMs, nil, MinOperationRetryDelayMs, MaxOperationRetryDelayMs, DefaultOperationRetryDelayMs),
			binding(&c.ChannelConfirmAttempts, nil, MinChannelConfirmAttempts, MaxChannelConfirmAttempts, DefaultChannelConfirmAttempts),
			binding(&c.ChannelConfirmIntervalMs, nil, MinChannelConfirmIntervalMs, MaxChannelConfirmIntervalMs, DefaultChannelConfirmIntervalMs),
			binding(&c.ConfirmReconnectThreshold, nil, MinConfirmReconnectThreshold, MaxConfirmReconnectThreshold, DefaultConfirmReconnectThreshold),
			binding(&c.ConfirmReconnectDelayMs, nil, MinConfirmReconnectDelayMs, MaxConfirmReconnectDelayMs, DefaultConfirmReconnectDelayMs),
			binding(&c.IdentifyAttempts, nil, MinIdentifyAttempts, MaxIdentifyAttempts, DefaultIdentifyAttempts),
			binding(&c.PresenceMissThreshold, nil, MinPresenceMissThreshold, MaxPresenceMissThreshold, DefaultPresenceMissThreshold),
			binding(&c.RecoveryRetryBaseSeconds, nil, MinRecoveryRetryBaseSeconds, MaxRecoveryRetryBaseSeconds, DefaultRecoveryRetryBaseSeconds),
			binding(&c.RecoveryRetryMaxSeconds, nil, MinRecoveryRetryMaxSeconds, MaxRecoveryRetryMaxSeconds, DefaultRecoveryRetryMaxSeconds),
			binding(&c.AbsentStationRetryLimit, nil, MinAbsentStationRetryLimit, MaxAbsentStationRetryLimit, DefaultAbsentStationRetryLimit),
			binding(&c.InitialReadTimeoutSeconds, nil, MinInitialReadTimeoutSeconds, MaxInitialReadTimeoutSeconds, DefaultInitialReadTimeoutSeconds),
			binding(&c.ScanReadPhaseTimeoutSeconds, nil, MinScanReadPhaseTimeoutSeconds, MaxScanReadPhaseTimeoutSeconds, DefaultScanReadPhaseTimeoutSeconds),
			binding(&c.StatusReadTimeoutSeconds, nil, MinStatusReadTimeoutSeconds, MaxStatusReadTimeoutSeconds, DefaultStatusReadTimeoutSeconds),
			binding(&c.StatusRefreshTimeoutSeconds, nil, MinStatusRefreshTimeoutSeconds, MaxStatusRefreshTimeoutSeconds, DefaultStatusRefreshTimeoutSeconds),
			binding(&c.ChannelScanFreshnessSeconds, nil, MinChannelScanFreshnessSeconds, MaxChannelScanFreshnessSeconds, DefaultChannelScanFreshnessSeconds),
			binding(&c.BluetoothInitRetrySeconds, nil, MinBluetoothInitRetrySeconds, MaxBluetoothInitRetrySeconds, DefaultBluetoothInitRetrySeconds),
		}
	}
	return []intSettingBinding{
		binding(&c.ScanDurationSeconds, &p.ScanDurationSeconds, MinScanDurationSeconds, MaxScanDurationSeconds, DefaultScanDurationSeconds),
		binding(&c.BulkPowerTimeoutSeconds, &p.BulkPowerTimeoutSeconds, MinBulkPowerTimeoutSeconds, MaxBulkPowerTimeoutSeconds, DefaultBulkPowerTimeoutSeconds),
		binding(&c.StatusPollIntervalSeconds, &p.StatusPollIntervalSeconds, MinStatusPollIntervalSeconds, MaxStatusPollIntervalSeconds, DefaultStatusPollIntervalSeconds),
		binding(&c.StationOperationTimeoutSeconds, &p.StationOperationTimeoutSeconds, MinStationOperationTimeoutSeconds, MaxStationOperationTimeoutSeconds, DefaultStationOperationTimeoutSeconds),
		binding(&c.PowerConfirmAttemptsOn, &p.PowerConfirmAttemptsOn, MinPowerConfirmAttempts, MaxPowerConfirmAttempts, DefaultPowerConfirmAttemptsOn),
		binding(&c.PowerConfirmAttemptsOff, &p.PowerConfirmAttemptsOff, MinPowerConfirmAttempts, MaxPowerConfirmAttempts, DefaultPowerConfirmAttemptsOff),
		binding(&c.PowerConfirmPollIntervalMs, &p.PowerConfirmPollIntervalMs, MinPowerConfirmPollIntervalMs, MaxPowerConfirmPollIntervalMs, DefaultPowerConfirmPollIntervalMs),
		binding(&c.BootFallbackSeconds, &p.BootFallbackSeconds, MinBootFallbackSeconds, MaxBootFallbackSeconds, DefaultBootFallbackSeconds),
		binding(&c.SleepFinalWriteTimeoutSeconds, &p.SleepFinalWriteTimeoutSeconds, MinSleepFinalWriteTimeoutSeconds, MaxSleepFinalWriteTimeoutSeconds, DefaultSleepFinalWriteTimeoutSeconds),
		binding(&c.SleepPrepareGapMs, &p.SleepPrepareGapMs, MinSleepPrepareGapMs, MaxSleepPrepareGapMs, DefaultSleepPrepareGapMs),
		binding(&c.DiscoveryAttempts, &p.DiscoveryAttempts, MinDiscoveryAttempts, MaxDiscoveryAttempts, DefaultDiscoveryAttempts),
		binding(&c.DiscoveryRetryDelayMs, &p.DiscoveryRetryDelayMs, MinDiscoveryRetryDelayMs, MaxDiscoveryRetryDelayMs, DefaultDiscoveryRetryDelayMs),
		binding(&c.PowerWriteAttempts, &p.PowerWriteAttempts, MinPowerWriteAttempts, MaxPowerWriteAttempts, DefaultPowerWriteAttempts),
		binding(&c.OperationRetryDelayMs, &p.OperationRetryDelayMs, MinOperationRetryDelayMs, MaxOperationRetryDelayMs, DefaultOperationRetryDelayMs),
		binding(&c.ChannelConfirmAttempts, &p.ChannelConfirmAttempts, MinChannelConfirmAttempts, MaxChannelConfirmAttempts, DefaultChannelConfirmAttempts),
		binding(&c.ChannelConfirmIntervalMs, &p.ChannelConfirmIntervalMs, MinChannelConfirmIntervalMs, MaxChannelConfirmIntervalMs, DefaultChannelConfirmIntervalMs),
		binding(&c.ConfirmReconnectThreshold, &p.ConfirmReconnectThreshold, MinConfirmReconnectThreshold, MaxConfirmReconnectThreshold, DefaultConfirmReconnectThreshold),
		binding(&c.ConfirmReconnectDelayMs, &p.ConfirmReconnectDelayMs, MinConfirmReconnectDelayMs, MaxConfirmReconnectDelayMs, DefaultConfirmReconnectDelayMs),
		binding(&c.IdentifyAttempts, &p.IdentifyAttempts, MinIdentifyAttempts, MaxIdentifyAttempts, DefaultIdentifyAttempts),
		binding(&c.PresenceMissThreshold, &p.PresenceMissThreshold, MinPresenceMissThreshold, MaxPresenceMissThreshold, DefaultPresenceMissThreshold),
		binding(&c.RecoveryRetryBaseSeconds, &p.RecoveryRetryBaseSeconds, MinRecoveryRetryBaseSeconds, MaxRecoveryRetryBaseSeconds, DefaultRecoveryRetryBaseSeconds),
		binding(&c.RecoveryRetryMaxSeconds, &p.RecoveryRetryMaxSeconds, MinRecoveryRetryMaxSeconds, MaxRecoveryRetryMaxSeconds, DefaultRecoveryRetryMaxSeconds),
		binding(&c.AbsentStationRetryLimit, &p.AbsentStationRetryLimit, MinAbsentStationRetryLimit, MaxAbsentStationRetryLimit, DefaultAbsentStationRetryLimit),
		binding(&c.InitialReadTimeoutSeconds, &p.InitialReadTimeoutSeconds, MinInitialReadTimeoutSeconds, MaxInitialReadTimeoutSeconds, DefaultInitialReadTimeoutSeconds),
		binding(&c.ScanReadPhaseTimeoutSeconds, &p.ScanReadPhaseTimeoutSeconds, MinScanReadPhaseTimeoutSeconds, MaxScanReadPhaseTimeoutSeconds, DefaultScanReadPhaseTimeoutSeconds),
		binding(&c.StatusReadTimeoutSeconds, &p.StatusReadTimeoutSeconds, MinStatusReadTimeoutSeconds, MaxStatusReadTimeoutSeconds, DefaultStatusReadTimeoutSeconds),
		binding(&c.StatusRefreshTimeoutSeconds, &p.StatusRefreshTimeoutSeconds, MinStatusRefreshTimeoutSeconds, MaxStatusRefreshTimeoutSeconds, DefaultStatusRefreshTimeoutSeconds),
		binding(&c.ChannelScanFreshnessSeconds, &p.ChannelScanFreshnessSeconds, MinChannelScanFreshnessSeconds, MaxChannelScanFreshnessSeconds, DefaultChannelScanFreshnessSeconds),
		binding(&c.BluetoothInitRetrySeconds, &p.BluetoothInitRetrySeconds, MinBluetoothInitRetrySeconds, MaxBluetoothInitRetrySeconds, DefaultBluetoothInitRetrySeconds),
	}
}

// applyDefaults resets every setting to its default. Callers must hold
// c.mutex. Both Load fallback paths and NewConfig share it so the default
// set exists in exactly one place.
func (c *Config) applyDefaults() {
	c.RenamedStations = make(map[string]string)
	c.RenamedStationsByAddress = make(map[string]string)
	c.AutoSleep = autosleep.DefaultSettings()
	c.Language = ""
	c.ScanOnStartup = true
	c.StatusPollingEnabled = true
	c.APIListenAddress = DefaultAPIListenAddress
	for _, binding := range c.intSettingBindings(nil) {
		*binding.runtime = binding.fallback
	}
}

// applyPersisted sanitizes every ranged runtime field from a loaded snapshot.
// Callers must hold c.mutex.
func (c *Config) applyPersisted(loaded *persistedConfig) {
	for _, binding := range c.intSettingBindings(loaded) {
		*binding.runtime = sanitizeRangedInt(*binding.persisted, binding.min, binding.max, binding.fallback)
	}
}

// sanitizeRuntimeInPlace repairs directly-assigned or zero-value runtime
// fields so saveLocked can never persist an out-of-range value. Callers must
// hold c.mutex.
func (c *Config) sanitizeRuntimeInPlace() {
	for _, binding := range c.intSettingBindings(nil) {
		*binding.runtime = sanitizeRangedInt(binding.runtime, binding.min, binding.max, binding.fallback)
	}
}

// populatePersistedInts writes the (already sanitized and repaired) runtime
// values into the persisted snapshot pointers.
func (c *Config) populatePersistedInts(p *persistedConfig) {
	for _, binding := range c.intSettingBindings(p) {
		value := *binding.runtime
		*binding.persisted = &value
	}
}
