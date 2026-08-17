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
	"strings"
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

	// Hold the lock across the whole read-parse-assign sequence: a concurrent
	// Set* that completes between reading the file and assigning the fields
	// would otherwise be overwritten in memory by the older file contents and
	// silently persisted back on the next save.
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.loadLocked(configFilePath)
}

// loadLocked reads, parses, and applies the persisted configuration. Callers
// must hold c.mutex. It is shared by the startup Load and the blocked-save
// recovery so both paths apply the exact same sanitizing and block handling.
func (c *Config) loadLocked(configFilePath string) error {
	// A previous run dying between CreateTemp and Rename leaves scratch files
	// behind; the single-instance guard means no other instance is mid-write,
	// so stale temporaries are safe to sweep here.
	removeStaleConfigTemporaries(filepath.Dir(configFilePath))

	log.Printf("Loading config from: %s", configFilePath)
	configFile, err := configFileReader(configFilePath)
	if err != nil {
		if os.IsNotExist(err) {
			c.applyDefaults()
			c.persistenceBlockedErr = nil
			c.lastPersistenceErr = nil
			return nil // No config file yet, which is fine
		}
		// The in-memory state is empty or stale here. Block persistence so
		// the first rename cannot overwrite the unreadable file with a
		// partial config and destroy previously stored aliases; a later
		// successful Load clears the block.
		loadErr := fmt.Errorf("error reading config file '%s': %w", configFilePath, err)
		c.persistenceBlockedErr = loadErr
		c.lastPersistenceErr = loadErr
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
			c.persistenceBlockedErr = fmt.Errorf("invalid config could not be preserved: %w", renameErr)
			c.lastPersistenceErr = c.persistenceBlockedErr
			return fmt.Errorf("error unmarshalling config (failed to preserve invalid file: %v): %w", renameErr, err)
		}
		c.applyDefaults()
		c.persistenceBlockedErr = nil
		c.lastPersistenceErr = nil
		return fmt.Errorf("error unmarshalling config; invalid file preserved as '%s': %w", invalidPath, err)
	}
	if loaded.RenamedStations == nil {
		loaded.RenamedStations = make(map[string]string)
	}
	// A legacy alias whose value is empty is a leftover from an external or
	// hand edit; the setter treats an empty name as a removal. Dropping such
	// entries here keeps the getter from returning ("", true), which would
	// otherwise blank the station's display name.
	for originalName, renamedName := range loaded.RenamedStations {
		if renamedName == "" {
			delete(loaded.RenamedStations, originalName)
		}
	}
	if loaded.RenamedStationsByAddress == nil {
		loaded.RenamedStationsByAddress = make(map[string]string)
	}
	c.RenamedStations = loaded.RenamedStations
	c.RenamedStationsByAddress = loaded.RenamedStationsByAddress
	c.AutoSleep = sanitizeAutoSleep(loaded.AutoSleep)
	c.Language = sanitizeLanguage(loaded.Language)
	c.ScanOnStartup = boolOrDefault(loaded.ScanOnStartup, true)
	c.StatusPollingEnabled = boolOrDefault(loaded.StatusPollingEnabled, true)
	c.APIListenAddress = sanitizeAPIListenAddress(loaded.APIListenAddress)
	c.applyPersisted(&loaded)
	c.repairCrossItemInvariants()
	c.persistenceBlockedErr = nil
	c.lastPersistenceErr = nil
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
	address = strings.TrimSpace(address)
	if validateAPIListenAddress(address) != nil {
		return DefaultAPIListenAddress
	}
	return address
}

func validateAPIListenAddress(address string) error {
	address = strings.TrimSpace(address)
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("listen address must be host:port, got %q", address)
	}
	if host == "" {
		return fmt.Errorf("listen address %q is missing a host", address)
	}
	// The bind target must be an IP literal: net.Listen performs an
	// uncancellable, context-free DNS resolution for hostnames, which could
	// block the listener loop (and every restart/shutdown waiting on it)
	// indefinitely. A local bind target never needs name resolution.
	if net.ParseIP(host) == nil {
		return fmt.Errorf("listen address %q must use an IP literal host", address)
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil || port < 1024 {
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
