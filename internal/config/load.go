package config

import (
	"encoding/json"
	"fmt"
	"lhcontrol/internal/autosleep"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// Helper function to get the full path to the config file
func getConfigPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user config dir: %w", err)
	}
	appConfigDir := filepath.Join(configDir, "lhcontrol")
	err = os.MkdirAll(appConfigDir, 0755) // Ensure the directory exists
	if err != nil {
		return "", fmt.Errorf("failed to create app config dir '%s': %w", appConfigDir, err)
	}
	return filepath.Join(appConfigDir, "config.json"), nil
}

// Load reads the configuration from disk
func (c *Config) Load() error {
	configFilePath, err := getConfigPath()
	if err != nil {
		// The config directory could not be resolved or created, so the
		// on-disk state is unknown. Block persistence for the same reason as
		// an unreadable file: a later success must not overwrite a config
		// that was never read.
		loadErr := fmt.Errorf("failed to resolve config path: %w", err)
		c.mutex.Lock()
		c.persistenceBlockedErr = loadErr
		c.lastPersistenceErr = loadErr
		c.mutex.Unlock()
		return loadErr
	}

	log.Printf("Loading config from: %s", configFilePath)
	configFile, err := configFileReader(configFilePath)
	if err != nil {
		if os.IsNotExist(err) {
			c.mutex.Lock()
			defaults := NewConfig()
			c.RenamedStations = defaults.RenamedStations
			c.RenamedStationsByAddress = defaults.RenamedStationsByAddress
			c.AutoSleep = defaults.AutoSleep
			c.Language = defaults.Language
			c.ScanOnStartup = defaults.ScanOnStartup
			c.ScanDurationSeconds = defaults.ScanDurationSeconds
			c.StatusPollingEnabled = defaults.StatusPollingEnabled
			c.BulkPowerTimeoutSeconds = defaults.BulkPowerTimeoutSeconds
			c.StatusPollIntervalSeconds = defaults.StatusPollIntervalSeconds
			c.StationOperationTimeoutSeconds = defaults.StationOperationTimeoutSeconds
			c.PowerConfirmAttemptsOn = defaults.PowerConfirmAttemptsOn
			c.PowerConfirmAttemptsOff = defaults.PowerConfirmAttemptsOff
			c.PowerConfirmPollIntervalMs = defaults.PowerConfirmPollIntervalMs
			c.BootFallbackSeconds = defaults.BootFallbackSeconds
			c.SleepFinalWriteTimeoutSeconds = defaults.SleepFinalWriteTimeoutSeconds
			c.SleepPrepareGapMs = defaults.SleepPrepareGapMs
			c.DiscoveryAttempts = defaults.DiscoveryAttempts
			c.DiscoveryRetryDelayMs = defaults.DiscoveryRetryDelayMs
			c.APIListenAddress = defaults.APIListenAddress
			c.PowerWriteAttempts = defaults.PowerWriteAttempts
			c.OperationRetryDelayMs = defaults.OperationRetryDelayMs
			c.ChannelConfirmAttempts = defaults.ChannelConfirmAttempts
			c.ChannelConfirmIntervalMs = defaults.ChannelConfirmIntervalMs
			c.ConfirmReconnectThreshold = defaults.ConfirmReconnectThreshold
			c.ConfirmReconnectDelayMs = defaults.ConfirmReconnectDelayMs
			c.IdentifyAttempts = defaults.IdentifyAttempts
			c.PresenceMissThreshold = defaults.PresenceMissThreshold
			c.RecoveryRetryBaseSeconds = defaults.RecoveryRetryBaseSeconds
			c.RecoveryRetryMaxSeconds = defaults.RecoveryRetryMaxSeconds
			c.AbsentStationRetryLimit = defaults.AbsentStationRetryLimit
			c.InitialReadTimeoutSeconds = defaults.InitialReadTimeoutSeconds
			c.ScanReadPhaseTimeoutSeconds = defaults.ScanReadPhaseTimeoutSeconds
			c.StatusReadTimeoutSeconds = defaults.StatusReadTimeoutSeconds
			c.StatusRefreshTimeoutSeconds = defaults.StatusRefreshTimeoutSeconds
			c.ChannelScanFreshnessSeconds = defaults.ChannelScanFreshnessSeconds
			c.BluetoothInitRetrySeconds = defaults.BluetoothInitRetrySeconds
			c.persistenceBlockedErr = nil
			c.lastPersistenceErr = nil
			c.mutex.Unlock()
			return nil // No config file yet, which is fine
		}
		// The in-memory state is empty or stale here. Block persistence so
		// the first rename cannot overwrite the unreadable file with a
		// partial config and destroy previously stored aliases; a later
		// successful Load clears the block.
		loadErr := fmt.Errorf("error reading config file '%s': %w", configFilePath, err)
		c.mutex.Lock()
		c.persistenceBlockedErr = loadErr
		c.lastPersistenceErr = loadErr
		c.mutex.Unlock()
		return loadErr
	}

	var loaded persistedConfig
	err = json.Unmarshal(configFile, &loaded)
	if err != nil {
		invalidPath := fmt.Sprintf("%s.invalid-%s", configFilePath, time.Now().Format("20060102T150405.000000000"))
		renameErr := configFileRenamer(configFilePath, invalidPath)
		if renameErr != nil && os.IsNotExist(renameErr) {
			// A concurrent Load already quarantined the invalid file; the goal of
			// preserving it was achieved, so do not block persistence for it.
			renameErr = nil
		}
		if renameErr != nil {
			c.mutex.Lock()
			c.persistenceBlockedErr = fmt.Errorf("invalid config could not be preserved: %w", renameErr)
			c.lastPersistenceErr = c.persistenceBlockedErr
			c.mutex.Unlock()
			return fmt.Errorf("error unmarshalling config (failed to preserve invalid file: %v): %w", renameErr, err)
		}
		c.mutex.Lock()
		defaults := NewConfig()
		c.RenamedStations = defaults.RenamedStations
		c.RenamedStationsByAddress = defaults.RenamedStationsByAddress
		c.AutoSleep = defaults.AutoSleep
		c.Language = defaults.Language
		c.ScanOnStartup = defaults.ScanOnStartup
		c.ScanDurationSeconds = defaults.ScanDurationSeconds
		c.StatusPollingEnabled = defaults.StatusPollingEnabled
		c.BulkPowerTimeoutSeconds = defaults.BulkPowerTimeoutSeconds
		c.StatusPollIntervalSeconds = defaults.StatusPollIntervalSeconds
		c.StationOperationTimeoutSeconds = defaults.StationOperationTimeoutSeconds
		c.PowerConfirmAttemptsOn = defaults.PowerConfirmAttemptsOn
		c.PowerConfirmAttemptsOff = defaults.PowerConfirmAttemptsOff
		c.PowerConfirmPollIntervalMs = defaults.PowerConfirmPollIntervalMs
		c.BootFallbackSeconds = defaults.BootFallbackSeconds
		c.SleepFinalWriteTimeoutSeconds = defaults.SleepFinalWriteTimeoutSeconds
		c.SleepPrepareGapMs = defaults.SleepPrepareGapMs
		c.DiscoveryAttempts = defaults.DiscoveryAttempts
		c.DiscoveryRetryDelayMs = defaults.DiscoveryRetryDelayMs
		c.APIListenAddress = defaults.APIListenAddress
		c.PowerWriteAttempts = defaults.PowerWriteAttempts
		c.OperationRetryDelayMs = defaults.OperationRetryDelayMs
		c.ChannelConfirmAttempts = defaults.ChannelConfirmAttempts
		c.ChannelConfirmIntervalMs = defaults.ChannelConfirmIntervalMs
		c.ConfirmReconnectThreshold = defaults.ConfirmReconnectThreshold
		c.ConfirmReconnectDelayMs = defaults.ConfirmReconnectDelayMs
		c.IdentifyAttempts = defaults.IdentifyAttempts
		c.PresenceMissThreshold = defaults.PresenceMissThreshold
		c.RecoveryRetryBaseSeconds = defaults.RecoveryRetryBaseSeconds
		c.RecoveryRetryMaxSeconds = defaults.RecoveryRetryMaxSeconds
		c.AbsentStationRetryLimit = defaults.AbsentStationRetryLimit
		c.InitialReadTimeoutSeconds = defaults.InitialReadTimeoutSeconds
		c.ScanReadPhaseTimeoutSeconds = defaults.ScanReadPhaseTimeoutSeconds
		c.StatusReadTimeoutSeconds = defaults.StatusReadTimeoutSeconds
		c.StatusRefreshTimeoutSeconds = defaults.StatusRefreshTimeoutSeconds
		c.ChannelScanFreshnessSeconds = defaults.ChannelScanFreshnessSeconds
		c.BluetoothInitRetrySeconds = defaults.BluetoothInitRetrySeconds
		c.persistenceBlockedErr = nil
		c.lastPersistenceErr = nil
		c.mutex.Unlock()
		return fmt.Errorf("error unmarshalling config; invalid file preserved as '%s': %w", invalidPath, err)
	}
	if loaded.RenamedStations == nil {
		loaded.RenamedStations = make(map[string]string)
	}
	if loaded.RenamedStationsByAddress == nil {
		loaded.RenamedStationsByAddress = make(map[string]string)
	}
	c.mutex.Lock()
	c.RenamedStations = loaded.RenamedStations
	c.RenamedStationsByAddress = loaded.RenamedStationsByAddress
	c.AutoSleep = sanitizeAutoSleep(loaded.AutoSleep)
	c.Language = sanitizeLanguage(loaded.Language)
	c.ScanOnStartup = boolOrDefault(loaded.ScanOnStartup, true)
	c.ScanDurationSeconds = sanitizeScanDuration(loaded.ScanDurationSeconds)
	c.StatusPollingEnabled = boolOrDefault(loaded.StatusPollingEnabled, true)
	c.BulkPowerTimeoutSeconds = sanitizeBulkPowerTimeout(loaded.BulkPowerTimeoutSeconds)
	c.StatusPollIntervalSeconds = sanitizeStatusPollInterval(loaded.StatusPollIntervalSeconds)
	c.StationOperationTimeoutSeconds = sanitizeStationOperationTimeout(loaded.StationOperationTimeoutSeconds)
	c.PowerConfirmAttemptsOn = sanitizeRangedInt(loaded.PowerConfirmAttemptsOn, MinPowerConfirmAttempts, MaxPowerConfirmAttempts, DefaultPowerConfirmAttemptsOn)
	c.PowerConfirmAttemptsOff = sanitizeRangedInt(loaded.PowerConfirmAttemptsOff, MinPowerConfirmAttempts, MaxPowerConfirmAttempts, DefaultPowerConfirmAttemptsOff)
	c.PowerConfirmPollIntervalMs = sanitizeRangedInt(loaded.PowerConfirmPollIntervalMs, MinPowerConfirmPollIntervalMs, MaxPowerConfirmPollIntervalMs, DefaultPowerConfirmPollIntervalMs)
	c.BootFallbackSeconds = sanitizeRangedInt(loaded.BootFallbackSeconds, MinBootFallbackSeconds, MaxBootFallbackSeconds, DefaultBootFallbackSeconds)
	c.SleepFinalWriteTimeoutSeconds = sanitizeRangedInt(loaded.SleepFinalWriteTimeoutSeconds, MinSleepFinalWriteTimeoutSeconds, MaxSleepFinalWriteTimeoutSeconds, DefaultSleepFinalWriteTimeoutSeconds)
	c.SleepPrepareGapMs = sanitizeRangedInt(loaded.SleepPrepareGapMs, MinSleepPrepareGapMs, MaxSleepPrepareGapMs, DefaultSleepPrepareGapMs)
	c.DiscoveryAttempts = sanitizeRangedInt(loaded.DiscoveryAttempts, MinDiscoveryAttempts, MaxDiscoveryAttempts, DefaultDiscoveryAttempts)
	c.DiscoveryRetryDelayMs = sanitizeRangedInt(loaded.DiscoveryRetryDelayMs, MinDiscoveryRetryDelayMs, MaxDiscoveryRetryDelayMs, DefaultDiscoveryRetryDelayMs)
	c.APIListenAddress = sanitizeAPIListenAddress(loaded.APIListenAddress)
	c.PowerWriteAttempts = sanitizeRangedInt(loaded.PowerWriteAttempts, MinPowerWriteAttempts, MaxPowerWriteAttempts, DefaultPowerWriteAttempts)
	c.OperationRetryDelayMs = sanitizeRangedInt(loaded.OperationRetryDelayMs, MinOperationRetryDelayMs, MaxOperationRetryDelayMs, DefaultOperationRetryDelayMs)
	c.ChannelConfirmAttempts = sanitizeRangedInt(loaded.ChannelConfirmAttempts, MinChannelConfirmAttempts, MaxChannelConfirmAttempts, DefaultChannelConfirmAttempts)
	c.ChannelConfirmIntervalMs = sanitizeRangedInt(loaded.ChannelConfirmIntervalMs, MinChannelConfirmIntervalMs, MaxChannelConfirmIntervalMs, DefaultChannelConfirmIntervalMs)
	c.ConfirmReconnectThreshold = sanitizeRangedInt(loaded.ConfirmReconnectThreshold, MinConfirmReconnectThreshold, MaxConfirmReconnectThreshold, DefaultConfirmReconnectThreshold)
	c.ConfirmReconnectDelayMs = sanitizeRangedInt(loaded.ConfirmReconnectDelayMs, MinConfirmReconnectDelayMs, MaxConfirmReconnectDelayMs, DefaultConfirmReconnectDelayMs)
	c.IdentifyAttempts = sanitizeRangedInt(loaded.IdentifyAttempts, MinIdentifyAttempts, MaxIdentifyAttempts, DefaultIdentifyAttempts)
	c.PresenceMissThreshold = sanitizeRangedInt(loaded.PresenceMissThreshold, MinPresenceMissThreshold, MaxPresenceMissThreshold, DefaultPresenceMissThreshold)
	c.RecoveryRetryBaseSeconds = sanitizeRangedInt(loaded.RecoveryRetryBaseSeconds, MinRecoveryRetryBaseSeconds, MaxRecoveryRetryBaseSeconds, DefaultRecoveryRetryBaseSeconds)
	c.RecoveryRetryMaxSeconds = sanitizeRangedInt(loaded.RecoveryRetryMaxSeconds, MinRecoveryRetryMaxSeconds, MaxRecoveryRetryMaxSeconds, DefaultRecoveryRetryMaxSeconds)
	c.AbsentStationRetryLimit = sanitizeRangedInt(loaded.AbsentStationRetryLimit, MinAbsentStationRetryLimit, MaxAbsentStationRetryLimit, DefaultAbsentStationRetryLimit)
	c.InitialReadTimeoutSeconds = sanitizeRangedInt(loaded.InitialReadTimeoutSeconds, MinInitialReadTimeoutSeconds, MaxInitialReadTimeoutSeconds, DefaultInitialReadTimeoutSeconds)
	c.ScanReadPhaseTimeoutSeconds = sanitizeRangedInt(loaded.ScanReadPhaseTimeoutSeconds, MinScanReadPhaseTimeoutSeconds, MaxScanReadPhaseTimeoutSeconds, DefaultScanReadPhaseTimeoutSeconds)
	c.StatusReadTimeoutSeconds = sanitizeRangedInt(loaded.StatusReadTimeoutSeconds, MinStatusReadTimeoutSeconds, MaxStatusReadTimeoutSeconds, DefaultStatusReadTimeoutSeconds)
	c.StatusRefreshTimeoutSeconds = sanitizeRangedInt(loaded.StatusRefreshTimeoutSeconds, MinStatusRefreshTimeoutSeconds, MaxStatusRefreshTimeoutSeconds, DefaultStatusRefreshTimeoutSeconds)
	c.ChannelScanFreshnessSeconds = sanitizeRangedInt(loaded.ChannelScanFreshnessSeconds, MinChannelScanFreshnessSeconds, MaxChannelScanFreshnessSeconds, DefaultChannelScanFreshnessSeconds)
	c.BluetoothInitRetrySeconds = sanitizeRangedInt(loaded.BluetoothInitRetrySeconds, MinBluetoothInitRetrySeconds, MaxBluetoothInitRetrySeconds, DefaultBluetoothInitRetrySeconds)
	c.repairCrossItemInvariants()
	c.persistenceBlockedErr = nil
	c.lastPersistenceErr = nil
	c.mutex.Unlock()
	return nil
}

// repairCrossItemInvariants realigns coupled settings loaded from disk so a
// hand-edited or future-version file cannot leave the runtime in a state where
// budgets contradict each other. Callers must hold c.mutex.
func (c *Config) repairCrossItemInvariants() {
	repairCrossItemValues(
		&c.BulkPowerTimeoutSeconds,
		&c.StationOperationTimeoutSeconds,
		&c.InitialReadTimeoutSeconds,
		&c.ScanReadPhaseTimeoutSeconds,
		&c.StatusReadTimeoutSeconds,
		&c.StatusRefreshTimeoutSeconds,
		&c.RecoveryRetryBaseSeconds,
		&c.RecoveryRetryMaxSeconds,
	)
}

func repairCrossItemValues(
	bulkPowerTimeoutSeconds *int,
	stationOperationTimeoutSeconds *int,
	initialReadTimeoutSeconds *int,
	scanReadPhaseTimeoutSeconds *int,
	statusReadTimeoutSeconds *int,
	statusRefreshTimeoutSeconds *int,
	recoveryRetryBaseSeconds *int,
	recoveryRetryMaxSeconds *int,
) {
	if *bulkPowerTimeoutSeconds < *stationOperationTimeoutSeconds {
		*bulkPowerTimeoutSeconds = *stationOperationTimeoutSeconds
	}
	if *initialReadTimeoutSeconds > *stationOperationTimeoutSeconds {
		*initialReadTimeoutSeconds = *stationOperationTimeoutSeconds
	}
	if *scanReadPhaseTimeoutSeconds < *initialReadTimeoutSeconds {
		*scanReadPhaseTimeoutSeconds = *initialReadTimeoutSeconds
	}
	if *statusRefreshTimeoutSeconds < *statusReadTimeoutSeconds {
		*statusRefreshTimeoutSeconds = *statusReadTimeoutSeconds
	}
	if *recoveryRetryBaseSeconds > *recoveryRetryMaxSeconds {
		*recoveryRetryBaseSeconds = *recoveryRetryMaxSeconds
	}
}

func boolOrDefault(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}

func sanitizeScanDuration(durationSeconds *int) int {
	if durationSeconds == nil || *durationSeconds < MinScanDurationSeconds || *durationSeconds > MaxScanDurationSeconds {
		return DefaultScanDurationSeconds
	}
	return *durationSeconds
}

func sanitizeBulkPowerTimeout(timeoutSeconds *int) int {
	if timeoutSeconds == nil || *timeoutSeconds < MinBulkPowerTimeoutSeconds || *timeoutSeconds > MaxBulkPowerTimeoutSeconds {
		return DefaultBulkPowerTimeoutSeconds
	}
	return *timeoutSeconds
}

func sanitizeStatusPollInterval(intervalSeconds *int) int {
	if intervalSeconds == nil || *intervalSeconds < MinStatusPollIntervalSeconds || *intervalSeconds > MaxStatusPollIntervalSeconds {
		return DefaultStatusPollIntervalSeconds
	}
	return *intervalSeconds
}

func sanitizeStationOperationTimeout(timeoutSeconds *int) int {
	if timeoutSeconds == nil || *timeoutSeconds < MinStationOperationTimeoutSeconds || *timeoutSeconds > MaxStationOperationTimeoutSeconds {
		return DefaultStationOperationTimeoutSeconds
	}
	return *timeoutSeconds
}

// sanitizeRangedInt repairs a persisted integer against its allowed range,
// falling back to the provided default. It keeps the Load path readable when
// several settings share the same nil/range pattern.
func sanitizeRangedInt(value *int, min, max, fallback int) int {
	if value == nil || *value < min || *value > max {
		return fallback
	}
	return *value
}

func sanitizeAPIListenAddress(address string) string {
	if validateAPIListenAddress(address) != nil {
		return DefaultAPIListenAddress
	}
	return address
}

func validateAPIListenAddress(address string) error {
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("listen address must be host:port, got %q", address)
	}
	if host == "" {
		return fmt.Errorf("listen address %q is missing a host", address)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1024 || port > 65535 {
		return fmt.Errorf("listen address %q must use a port between 1024 and 65535", address)
	}
	return nil
}

func sanitizeLanguage(language string) string {
	if language == LanguageEnglish || language == LanguageSimplifiedChinese {
		return language
	}
	return ""
}

// sanitizeAutoSleep repairs a persisted value that fails validation (for
// example after a future version stored something this build rejects) by
// falling back to individual defaults, keeping every valid part intact.
func sanitizeAutoSleep(settings *autosleep.Settings) autosleep.Settings {
	if settings == nil {
		return autosleep.DefaultSettings()
	}
	result := *settings
	if _, err := autosleep.Target(result.Target).ProcessName(); err != nil {
		result.Target = string(autosleep.DefaultTarget)
	}
	if result.DelaySeconds < autosleep.MinDelaySeconds || result.DelaySeconds > autosleep.MaxDelaySeconds {
		result.DelaySeconds = autosleep.DefaultDelaySeconds
	}
	return result
}

// Save writes the configuration to disk
