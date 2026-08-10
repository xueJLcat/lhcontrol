package config

import (
	"encoding/json"
	"fmt"
	"lhcontrol/internal/autosleep"
	"log"
	"os"
	"path/filepath"
)

func (c *Config) Save() error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.saveLocked()
}

// PersistenceError reports whether saves are intentionally blocked because an
// unreadable config file could not be preserved. It is safe to call while the
// application is exposing status to another goroutine.
func (c *Config) PersistenceError() error {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	if c.persistenceBlockedErr != nil {
		return c.persistenceBlockedErr
	}
	return c.lastPersistenceErr
}

// saveLocked persists exactly the state protected by mutex. Keeping mutation
// and persistence under one exclusive lock prevents an older Save from
// overwriting a newer rename.
func (c *Config) saveLocked() error {
	if c.persistenceBlockedErr != nil {
		return fmt.Errorf("config save blocked to preserve the unreadable file: %w", c.persistenceBlockedErr)
	}
	configFilePath, err := getConfigPath()
	if err != nil {
		c.lastPersistenceErr = err
		return err
	}

	// Sanitize while persisting so a directly-assigned or zero-value field can
	// never write an out-of-range value that Load would later silently replace.
	snapshot := persistedConfig{
		RenamedStations:          make(map[string]string, len(c.RenamedStations)),
		RenamedStationsByAddress: make(map[string]string, len(c.RenamedStationsByAddress)),
		Language:                 sanitizeLanguage(c.Language),
	}
	scanOnStartup := c.ScanOnStartup
	snapshot.ScanOnStartup = &scanOnStartup
	scanDurationSeconds := sanitizeScanDuration(&c.ScanDurationSeconds)
	snapshot.ScanDurationSeconds = &scanDurationSeconds
	statusPollingEnabled := c.StatusPollingEnabled
	snapshot.StatusPollingEnabled = &statusPollingEnabled
	bulkPowerTimeoutSeconds := sanitizeBulkPowerTimeout(&c.BulkPowerTimeoutSeconds)
	snapshot.BulkPowerTimeoutSeconds = &bulkPowerTimeoutSeconds
	statusPollIntervalSeconds := sanitizeStatusPollInterval(&c.StatusPollIntervalSeconds)
	snapshot.StatusPollIntervalSeconds = &statusPollIntervalSeconds
	stationOperationTimeoutSeconds := sanitizeStationOperationTimeout(&c.StationOperationTimeoutSeconds)
	snapshot.StationOperationTimeoutSeconds = &stationOperationTimeoutSeconds
	powerConfirmAttemptsOn := sanitizeRangedInt(&c.PowerConfirmAttemptsOn, MinPowerConfirmAttempts, MaxPowerConfirmAttempts, DefaultPowerConfirmAttemptsOn)
	snapshot.PowerConfirmAttemptsOn = &powerConfirmAttemptsOn
	powerConfirmAttemptsOff := sanitizeRangedInt(&c.PowerConfirmAttemptsOff, MinPowerConfirmAttempts, MaxPowerConfirmAttempts, DefaultPowerConfirmAttemptsOff)
	snapshot.PowerConfirmAttemptsOff = &powerConfirmAttemptsOff
	powerConfirmPollIntervalMs := sanitizeRangedInt(&c.PowerConfirmPollIntervalMs, MinPowerConfirmPollIntervalMs, MaxPowerConfirmPollIntervalMs, DefaultPowerConfirmPollIntervalMs)
	snapshot.PowerConfirmPollIntervalMs = &powerConfirmPollIntervalMs
	bootFallbackSeconds := sanitizeRangedInt(&c.BootFallbackSeconds, MinBootFallbackSeconds, MaxBootFallbackSeconds, DefaultBootFallbackSeconds)
	snapshot.BootFallbackSeconds = &bootFallbackSeconds
	sleepFinalWriteTimeoutSeconds := sanitizeRangedInt(&c.SleepFinalWriteTimeoutSeconds, MinSleepFinalWriteTimeoutSeconds, MaxSleepFinalWriteTimeoutSeconds, DefaultSleepFinalWriteTimeoutSeconds)
	snapshot.SleepFinalWriteTimeoutSeconds = &sleepFinalWriteTimeoutSeconds
	sleepPrepareGapMs := sanitizeRangedInt(&c.SleepPrepareGapMs, MinSleepPrepareGapMs, MaxSleepPrepareGapMs, DefaultSleepPrepareGapMs)
	snapshot.SleepPrepareGapMs = &sleepPrepareGapMs
	discoveryAttempts := sanitizeRangedInt(&c.DiscoveryAttempts, MinDiscoveryAttempts, MaxDiscoveryAttempts, DefaultDiscoveryAttempts)
	snapshot.DiscoveryAttempts = &discoveryAttempts
	discoveryRetryDelayMs := sanitizeRangedInt(&c.DiscoveryRetryDelayMs, MinDiscoveryRetryDelayMs, MaxDiscoveryRetryDelayMs, DefaultDiscoveryRetryDelayMs)
	snapshot.DiscoveryRetryDelayMs = &discoveryRetryDelayMs
	snapshot.APIListenAddress = sanitizeAPIListenAddress(c.APIListenAddress)
	powerWriteAttempts := sanitizeRangedInt(&c.PowerWriteAttempts, MinPowerWriteAttempts, MaxPowerWriteAttempts, DefaultPowerWriteAttempts)
	snapshot.PowerWriteAttempts = &powerWriteAttempts
	operationRetryDelayMs := sanitizeRangedInt(&c.OperationRetryDelayMs, MinOperationRetryDelayMs, MaxOperationRetryDelayMs, DefaultOperationRetryDelayMs)
	snapshot.OperationRetryDelayMs = &operationRetryDelayMs
	channelConfirmAttempts := sanitizeRangedInt(&c.ChannelConfirmAttempts, MinChannelConfirmAttempts, MaxChannelConfirmAttempts, DefaultChannelConfirmAttempts)
	snapshot.ChannelConfirmAttempts = &channelConfirmAttempts
	channelConfirmIntervalMs := sanitizeRangedInt(&c.ChannelConfirmIntervalMs, MinChannelConfirmIntervalMs, MaxChannelConfirmIntervalMs, DefaultChannelConfirmIntervalMs)
	snapshot.ChannelConfirmIntervalMs = &channelConfirmIntervalMs
	confirmReconnectThreshold := sanitizeRangedInt(&c.ConfirmReconnectThreshold, MinConfirmReconnectThreshold, MaxConfirmReconnectThreshold, DefaultConfirmReconnectThreshold)
	snapshot.ConfirmReconnectThreshold = &confirmReconnectThreshold
	confirmReconnectDelayMs := sanitizeRangedInt(&c.ConfirmReconnectDelayMs, MinConfirmReconnectDelayMs, MaxConfirmReconnectDelayMs, DefaultConfirmReconnectDelayMs)
	snapshot.ConfirmReconnectDelayMs = &confirmReconnectDelayMs
	identifyAttempts := sanitizeRangedInt(&c.IdentifyAttempts, MinIdentifyAttempts, MaxIdentifyAttempts, DefaultIdentifyAttempts)
	snapshot.IdentifyAttempts = &identifyAttempts
	presenceMissThreshold := sanitizeRangedInt(&c.PresenceMissThreshold, MinPresenceMissThreshold, MaxPresenceMissThreshold, DefaultPresenceMissThreshold)
	snapshot.PresenceMissThreshold = &presenceMissThreshold
	recoveryRetryBaseSeconds := sanitizeRangedInt(&c.RecoveryRetryBaseSeconds, MinRecoveryRetryBaseSeconds, MaxRecoveryRetryBaseSeconds, DefaultRecoveryRetryBaseSeconds)
	snapshot.RecoveryRetryBaseSeconds = &recoveryRetryBaseSeconds
	recoveryRetryMaxSeconds := sanitizeRangedInt(&c.RecoveryRetryMaxSeconds, MinRecoveryRetryMaxSeconds, MaxRecoveryRetryMaxSeconds, DefaultRecoveryRetryMaxSeconds)
	snapshot.RecoveryRetryMaxSeconds = &recoveryRetryMaxSeconds
	absentStationRetryLimit := sanitizeRangedInt(&c.AbsentStationRetryLimit, MinAbsentStationRetryLimit, MaxAbsentStationRetryLimit, DefaultAbsentStationRetryLimit)
	snapshot.AbsentStationRetryLimit = &absentStationRetryLimit
	initialReadTimeoutSeconds := sanitizeRangedInt(&c.InitialReadTimeoutSeconds, MinInitialReadTimeoutSeconds, MaxInitialReadTimeoutSeconds, DefaultInitialReadTimeoutSeconds)
	snapshot.InitialReadTimeoutSeconds = &initialReadTimeoutSeconds
	scanReadPhaseTimeoutSeconds := sanitizeRangedInt(&c.ScanReadPhaseTimeoutSeconds, MinScanReadPhaseTimeoutSeconds, MaxScanReadPhaseTimeoutSeconds, DefaultScanReadPhaseTimeoutSeconds)
	snapshot.ScanReadPhaseTimeoutSeconds = &scanReadPhaseTimeoutSeconds
	statusReadTimeoutSeconds := sanitizeRangedInt(&c.StatusReadTimeoutSeconds, MinStatusReadTimeoutSeconds, MaxStatusReadTimeoutSeconds, DefaultStatusReadTimeoutSeconds)
	snapshot.StatusReadTimeoutSeconds = &statusReadTimeoutSeconds
	statusRefreshTimeoutSeconds := sanitizeRangedInt(&c.StatusRefreshTimeoutSeconds, MinStatusRefreshTimeoutSeconds, MaxStatusRefreshTimeoutSeconds, DefaultStatusRefreshTimeoutSeconds)
	snapshot.StatusRefreshTimeoutSeconds = &statusRefreshTimeoutSeconds
	channelScanFreshnessSeconds := sanitizeRangedInt(&c.ChannelScanFreshnessSeconds, MinChannelScanFreshnessSeconds, MaxChannelScanFreshnessSeconds, DefaultChannelScanFreshnessSeconds)
	snapshot.ChannelScanFreshnessSeconds = &channelScanFreshnessSeconds
	bluetoothInitRetrySeconds := sanitizeRangedInt(&c.BluetoothInitRetrySeconds, MinBluetoothInitRetrySeconds, MaxBluetoothInitRetrySeconds, DefaultBluetoothInitRetrySeconds)
	snapshot.BluetoothInitRetrySeconds = &bluetoothInitRetrySeconds
	repairCrossItemValues(
		&bulkPowerTimeoutSeconds,
		&stationOperationTimeoutSeconds,
		&initialReadTimeoutSeconds,
		&scanReadPhaseTimeoutSeconds,
		&statusReadTimeoutSeconds,
		&statusRefreshTimeoutSeconds,
		&recoveryRetryBaseSeconds,
		&recoveryRetryMaxSeconds,
	)
	autoSleep := sanitizeAutoSleep(&c.AutoSleep)
	if c.AutoSleep != (autosleep.Settings{}) {
		snapshot.AutoSleep = &autoSleep
	}
	for originalName, renamedName := range c.RenamedStations {
		snapshot.RenamedStations[originalName] = renamedName
	}
	for address, renamedName := range c.RenamedStationsByAddress {
		snapshot.RenamedStationsByAddress[address] = renamedName
	}

	configFile, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		saveErr := fmt.Errorf("error marshalling config: %w", err)
		c.lastPersistenceErr = saveErr
		return saveErr
	}

	log.Printf("Saving config to: %s", configFilePath)
	err = configFileWriter(configFilePath, configFile, 0644)
	if err != nil {
		saveErr := fmt.Errorf("failed to write config file '%s': %w", configFilePath, err)
		c.lastPersistenceErr = saveErr
		return saveErr
	}
	// Save is also a normalization boundary for callers that populated the
	// exported fields directly. Once the atomic write succeeds, keep runtime
	// getters and the just-written file on the same repaired values instead of
	// changing behavior only after the next process restart.
	c.AutoSleep = autoSleep
	c.Language = snapshot.Language
	c.ScanOnStartup = scanOnStartup
	c.ScanDurationSeconds = scanDurationSeconds
	c.StatusPollingEnabled = statusPollingEnabled
	c.BulkPowerTimeoutSeconds = bulkPowerTimeoutSeconds
	c.StatusPollIntervalSeconds = statusPollIntervalSeconds
	c.StationOperationTimeoutSeconds = stationOperationTimeoutSeconds
	c.PowerConfirmAttemptsOn = powerConfirmAttemptsOn
	c.PowerConfirmAttemptsOff = powerConfirmAttemptsOff
	c.PowerConfirmPollIntervalMs = powerConfirmPollIntervalMs
	c.BootFallbackSeconds = bootFallbackSeconds
	c.SleepFinalWriteTimeoutSeconds = sleepFinalWriteTimeoutSeconds
	c.SleepPrepareGapMs = sleepPrepareGapMs
	c.DiscoveryAttempts = discoveryAttempts
	c.DiscoveryRetryDelayMs = discoveryRetryDelayMs
	c.APIListenAddress = snapshot.APIListenAddress
	c.PowerWriteAttempts = powerWriteAttempts
	c.OperationRetryDelayMs = operationRetryDelayMs
	c.ChannelConfirmAttempts = channelConfirmAttempts
	c.ChannelConfirmIntervalMs = channelConfirmIntervalMs
	c.ConfirmReconnectThreshold = confirmReconnectThreshold
	c.ConfirmReconnectDelayMs = confirmReconnectDelayMs
	c.IdentifyAttempts = identifyAttempts
	c.PresenceMissThreshold = presenceMissThreshold
	c.RecoveryRetryBaseSeconds = recoveryRetryBaseSeconds
	c.RecoveryRetryMaxSeconds = recoveryRetryMaxSeconds
	c.AbsentStationRetryLimit = absentStationRetryLimit
	c.InitialReadTimeoutSeconds = initialReadTimeoutSeconds
	c.ScanReadPhaseTimeoutSeconds = scanReadPhaseTimeoutSeconds
	c.StatusReadTimeoutSeconds = statusReadTimeoutSeconds
	c.StatusRefreshTimeoutSeconds = statusRefreshTimeoutSeconds
	c.ChannelScanFreshnessSeconds = channelScanFreshnessSeconds
	c.BluetoothInitRetrySeconds = bluetoothInitRetrySeconds
	c.lastPersistenceErr = nil
	return nil
}

func writeFileAtomically(path string, data []byte, permissions os.FileMode) (returnErr error) {
	tempFile, err := os.CreateTemp(filepath.Dir(path), ".lhcontrol-config-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary config: %w", err)
	}
	tempPath := tempFile.Name()
	closed := false
	defer func() {
		if !closed {
			if closeErr := tempFile.Close(); returnErr == nil && closeErr != nil {
				returnErr = fmt.Errorf("close temporary config: %w", closeErr)
			}
		}
		_ = os.Remove(tempPath)
	}()

	if err := tempFile.Chmod(permissions); err != nil {
		return fmt.Errorf("set temporary config permissions: %w", err)
	}
	if _, err := tempFile.Write(data); err != nil {
		return fmt.Errorf("write temporary config: %w", err)
	}
	if err := tempFile.Sync(); err != nil {
		return fmt.Errorf("flush temporary config: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("close temporary config before replacement: %w", err)
	}
	closed = true
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}
	return nil
}

// GetRenamedStation returns the local display name for a station.
