package config

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"lhcontrol/internal/autosleep"
)

const (
	LanguageEnglish                  = "en"
	LanguageSimplifiedChinese        = "zh-CN"
	MinScanDurationSeconds           = 2
	MaxScanDurationSeconds           = 30
	DefaultScanDurationSeconds       = 5
	MinBulkPowerTimeoutSeconds       = 30
	MaxBulkPowerTimeoutSeconds       = 600
	DefaultBulkPowerTimeoutSeconds   = 120
	MinStatusPollIntervalSeconds     = 5
	MaxStatusPollIntervalSeconds     = 300
	DefaultStatusPollIntervalSeconds = 15
	MinimumDisplayFreshnessSeconds   = 45
	StatusPollJitterSeconds          = 5
)

type Config struct {
	RenamedStations           map[string]string  `json:"renamedStations"`
	RenamedStationsByAddress  map[string]string  `json:"renamedStationsByAddress"`
	AutoSleep                 autosleep.Settings `json:"autoSleep"`
	Language                  string             `json:"language,omitempty"`
	ScanOnStartup             bool               `json:"scanOnStartup"`
	ScanDurationSeconds       int                `json:"scanDurationSeconds"`
	StatusPollingEnabled      bool               `json:"statusPollingEnabled"`
	BulkPowerTimeoutSeconds   int                `json:"bulkPowerTimeoutSeconds"`
	StatusPollIntervalSeconds int                `json:"statusPollIntervalSeconds"`
	persistenceBlockedErr     error
	lastPersistenceErr        error
	mutex                     sync.RWMutex
}

type persistedConfig struct {
	RenamedStations           map[string]string   `json:"renamedStations"`
	RenamedStationsByAddress  map[string]string   `json:"renamedStationsByAddress,omitempty"`
	AutoSleep                 *autosleep.Settings `json:"autoSleep,omitempty"`
	Language                  string              `json:"language,omitempty"`
	ScanOnStartup             *bool               `json:"scanOnStartup,omitempty"`
	ScanDurationSeconds       *int                `json:"scanDurationSeconds,omitempty"`
	StatusPollingEnabled      *bool               `json:"statusPollingEnabled,omitempty"`
	BulkPowerTimeoutSeconds   *int                `json:"bulkPowerTimeoutSeconds,omitempty"`
	StatusPollIntervalSeconds *int                `json:"statusPollIntervalSeconds,omitempty"`
}

var (
	configFileReader  = os.ReadFile
	configFileWriter  = writeFileAtomically
	configFileRenamer = os.Rename
)

// NewConfig creates a new Config with defaults
func NewConfig() *Config {
	return &Config{
		RenamedStations:           make(map[string]string),
		RenamedStationsByAddress:  make(map[string]string),
		AutoSleep:                 autosleep.DefaultSettings(),
		ScanOnStartup:             true,
		ScanDurationSeconds:       DefaultScanDurationSeconds,
		StatusPollingEnabled:      true,
		BulkPowerTimeoutSeconds:   DefaultBulkPowerTimeoutSeconds,
		StatusPollIntervalSeconds: DefaultStatusPollIntervalSeconds,
	}
}

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
		loadErr := fmt.Errorf("failed to resolve config path: %w", err)
		c.mutex.Lock()
		c.lastPersistenceErr = loadErr
		c.mutex.Unlock()
		return loadErr
	}

	log.Printf("Loading config from: %s", configFilePath)
	configFile, err := configFileReader(configFilePath)
	if err != nil {
		if os.IsNotExist(err) {
			c.mutex.Lock()
			c.RenamedStations = make(map[string]string)
			c.RenamedStationsByAddress = make(map[string]string)
			c.AutoSleep = autosleep.DefaultSettings()
			c.Language = ""
			c.ScanOnStartup = true
			c.ScanDurationSeconds = DefaultScanDurationSeconds
			c.StatusPollingEnabled = true
			c.BulkPowerTimeoutSeconds = DefaultBulkPowerTimeoutSeconds
			c.StatusPollIntervalSeconds = DefaultStatusPollIntervalSeconds
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
		if renameErr := configFileRenamer(configFilePath, invalidPath); renameErr != nil {
			c.mutex.Lock()
			c.persistenceBlockedErr = fmt.Errorf("invalid config could not be preserved: %w", renameErr)
			c.lastPersistenceErr = c.persistenceBlockedErr
			c.mutex.Unlock()
			return fmt.Errorf("error unmarshalling config (failed to preserve invalid file: %v): %w", renameErr, err)
		}
		c.mutex.Lock()
		c.RenamedStations = make(map[string]string)
		c.RenamedStationsByAddress = make(map[string]string)
		c.AutoSleep = autosleep.DefaultSettings()
		c.Language = ""
		c.ScanOnStartup = true
		c.ScanDurationSeconds = DefaultScanDurationSeconds
		c.StatusPollingEnabled = true
		c.BulkPowerTimeoutSeconds = DefaultBulkPowerTimeoutSeconds
		c.StatusPollIntervalSeconds = DefaultStatusPollIntervalSeconds
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
	c.persistenceBlockedErr = nil
	c.lastPersistenceErr = nil
	c.mutex.Unlock()
	return nil
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

	snapshot := persistedConfig{
		RenamedStations:          make(map[string]string, len(c.RenamedStations)),
		RenamedStationsByAddress: make(map[string]string, len(c.RenamedStationsByAddress)),
		Language:                 c.Language,
	}
	scanOnStartup := c.ScanOnStartup
	snapshot.ScanOnStartup = &scanOnStartup
	scanDurationSeconds := c.ScanDurationSeconds
	snapshot.ScanDurationSeconds = &scanDurationSeconds
	statusPollingEnabled := c.StatusPollingEnabled
	snapshot.StatusPollingEnabled = &statusPollingEnabled
	bulkPowerTimeoutSeconds := c.BulkPowerTimeoutSeconds
	snapshot.BulkPowerTimeoutSeconds = &bulkPowerTimeoutSeconds
	statusPollIntervalSeconds := c.StatusPollIntervalSeconds
	snapshot.StatusPollIntervalSeconds = &statusPollIntervalSeconds
	if c.AutoSleep != (autosleep.Settings{}) {
		autoSleep := c.AutoSleep
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
func (c *Config) GetRenamedStation(originalName string) (string, bool) {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	renamedName, ok := c.RenamedStations[originalName]
	return renamedName, ok
}

// SetRenamedStation updates a local display name and persists the config.
func (c *Config) SetRenamedStation(originalName string, newName string) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	previous, existed := c.RenamedStations[originalName]
	if newName == "" {
		delete(c.RenamedStations, originalName)
	} else {
		c.RenamedStations[originalName] = newName
	}
	if err := c.saveLocked(); err != nil {
		if existed {
			c.RenamedStations[originalName] = previous
		} else {
			delete(c.RenamedStations, originalName)
		}
		return err
	}
	return nil
}

// SetRenamedStationForAddresses keeps the legacy name-based API effective for
// stations that already have a more specific address alias.
func (c *Config) SetRenamedStationForAddresses(originalName, newName string, addresses []string) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	previousLegacy, legacyExisted := c.RenamedStations[originalName]
	previousAddresses := make(map[string]string, len(addresses))
	existingAddresses := make(map[string]bool, len(addresses))
	for _, address := range addresses {
		previousAddresses[address], existingAddresses[address] = c.RenamedStationsByAddress[address]
		if newName == "" {
			delete(c.RenamedStationsByAddress, address)
		} else {
			c.RenamedStationsByAddress[address] = newName
		}
	}
	if newName == "" {
		delete(c.RenamedStations, originalName)
	} else {
		c.RenamedStations[originalName] = newName
	}

	if err := c.saveLocked(); err != nil {
		if legacyExisted {
			c.RenamedStations[originalName] = previousLegacy
		} else {
			delete(c.RenamedStations, originalName)
		}
		for _, address := range addresses {
			if existingAddresses[address] {
				c.RenamedStationsByAddress[address] = previousAddresses[address]
			} else {
				delete(c.RenamedStationsByAddress, address)
			}
		}
		return err
	}
	return nil
}

// GetStationDisplayName uses the stable BLE address first and falls back to
// legacy name-keyed entries so existing configurations continue to work.
func (c *Config) GetStationDisplayName(address, originalName string) (string, bool) {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	if renamedName, ok := c.RenamedStationsByAddress[address]; ok {
		if renamedName == "" {
			return originalName, false
		}
		return renamedName, true
	}
	renamedName, ok := c.RenamedStations[originalName]
	return renamedName, ok
}

func (c *Config) SetRenamedStationByAddress(address, originalName, newName string) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if c.RenamedStationsByAddress == nil {
		c.RenamedStationsByAddress = make(map[string]string)
	}
	previousAddressName, addressExisted := c.RenamedStationsByAddress[address]
	previousLegacyName, legacyExisted := c.RenamedStations[originalName]
	if newName == "" {
		if legacyExisted {
			// An empty address entry is a tombstone for this device only. It
			// prevents the shared legacy name from being applied again without
			// removing that alias from other devices with the same factory name.
			c.RenamedStationsByAddress[address] = ""
		} else {
			delete(c.RenamedStationsByAddress, address)
		}
	} else {
		c.RenamedStationsByAddress[address] = newName
	}
	if err := c.saveLocked(); err != nil {
		if addressExisted {
			c.RenamedStationsByAddress[address] = previousAddressName
		} else {
			delete(c.RenamedStationsByAddress, address)
		}
		if legacyExisted {
			c.RenamedStations[originalName] = previousLegacyName
		} else {
			delete(c.RenamedStations, originalName)
		}
		return err
	}
	return nil
}

// GetAutoSleep returns the persisted automatic-sleep settings. The returned
// value is always valid; invalid persisted data is repaired at load time.
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
	previous := c.StatusPollIntervalSeconds
	c.StatusPollIntervalSeconds = intervalSeconds
	if err := c.saveLocked(); err != nil {
		c.StatusPollIntervalSeconds = previous
		return err
	}
	return nil
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
