package config

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
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
	// MinStationOperationTimeoutSeconds stays at the initial-read budget so a
	// verification read can never starve the write phase of the same station.
	MinStationOperationTimeoutSeconds     = 30
	MaxStationOperationTimeoutSeconds     = 120
	DefaultStationOperationTimeoutSeconds = 30
	MinPowerConfirmAttempts               = 5
	MaxPowerConfirmAttempts               = 200
	DefaultPowerConfirmAttemptsOn         = 51
	DefaultPowerConfirmAttemptsOff        = 15
	MinPowerConfirmPollIntervalMs         = 50
	MaxPowerConfirmPollIntervalMs         = 2000
	DefaultPowerConfirmPollIntervalMs     = 200
	MinBootFallbackSeconds                = 2
	MaxBootFallbackSeconds                = 60
	DefaultBootFallbackSeconds            = 8
	MinSleepFinalWriteTimeoutSeconds      = 5
	MaxSleepFinalWriteTimeoutSeconds      = 120
	DefaultSleepFinalWriteTimeoutSeconds  = 30
	MinSleepPrepareGapMs                  = 0
	MaxSleepPrepareGapMs                  = 500
	DefaultSleepPrepareGapMs              = 50
	MinDiscoveryAttempts                  = 1
	MaxDiscoveryAttempts                  = 10
	DefaultDiscoveryAttempts              = 3
	MinDiscoveryRetryDelayMs              = 100
	MaxDiscoveryRetryDelayMs              = 5000
	DefaultDiscoveryRetryDelayMs          = 500
	DefaultAPIListenAddress               = "127.0.0.1:7575"
	MinPowerWriteAttempts                 = 1
	MaxPowerWriteAttempts                 = 5
	DefaultPowerWriteAttempts             = 2
	MinOperationRetryDelayMs              = 100
	MaxOperationRetryDelayMs              = 5000
	DefaultOperationRetryDelayMs          = 500
	MinChannelConfirmAttempts             = 1
	MaxChannelConfirmAttempts             = 20
	DefaultChannelConfirmAttempts         = 5
	MinChannelConfirmIntervalMs           = 50
	MaxChannelConfirmIntervalMs           = 2000
	DefaultChannelConfirmIntervalMs       = 250
	MinConfirmReconnectThreshold          = 1
	MaxConfirmReconnectThreshold          = 5
	DefaultConfirmReconnectThreshold      = 2
	MinConfirmReconnectDelayMs            = 50
	MaxConfirmReconnectDelayMs            = 2000
	DefaultConfirmReconnectDelayMs        = 250
	MinIdentifyAttempts                   = 1
	MaxIdentifyAttempts                   = 5
	DefaultIdentifyAttempts               = 2
	MinPresenceMissThreshold              = 1
	MaxPresenceMissThreshold              = 10
	DefaultPresenceMissThreshold          = 2
	MinRecoveryRetryBaseSeconds           = 5
	MaxRecoveryRetryBaseSeconds           = 120
	DefaultRecoveryRetryBaseSeconds       = 30
	MinRecoveryRetryMaxSeconds            = 60
	MaxRecoveryRetryMaxSeconds            = 1800
	DefaultRecoveryRetryMaxSeconds        = 300
	MinAbsentStationRetryLimit            = 1
	MaxAbsentStationRetryLimit            = 20
	DefaultAbsentStationRetryLimit        = 5
	MinInitialReadTimeoutSeconds          = 10
	MaxInitialReadTimeoutSeconds          = 60
	DefaultInitialReadTimeoutSeconds      = 30
	MinScanReadPhaseTimeoutSeconds        = 15
	MaxScanReadPhaseTimeoutSeconds        = 120
	DefaultScanReadPhaseTimeoutSeconds    = 45
	MinStatusReadTimeoutSeconds           = 5
	MaxStatusReadTimeoutSeconds           = 60
	DefaultStatusReadTimeoutSeconds       = 20
	MinStatusRefreshTimeoutSeconds        = 10
	MaxStatusRefreshTimeoutSeconds        = 120
	DefaultStatusRefreshTimeoutSeconds    = 30
	MinChannelScanFreshnessSeconds        = 30
	MaxChannelScanFreshnessSeconds        = 600
	DefaultChannelScanFreshnessSeconds    = 120
	MinBluetoothInitRetrySeconds          = 1
	MaxBluetoothInitRetrySeconds          = 30
	DefaultBluetoothInitRetrySeconds      = 2
)

type Config struct {
	RenamedStations                map[string]string  `json:"renamedStations"`
	RenamedStationsByAddress       map[string]string  `json:"renamedStationsByAddress"`
	AutoSleep                      autosleep.Settings `json:"autoSleep"`
	Language                       string             `json:"language,omitempty"`
	ScanOnStartup                  bool               `json:"scanOnStartup"`
	ScanDurationSeconds            int                `json:"scanDurationSeconds"`
	StatusPollingEnabled           bool               `json:"statusPollingEnabled"`
	BulkPowerTimeoutSeconds        int                `json:"bulkPowerTimeoutSeconds"`
	StatusPollIntervalSeconds      int                `json:"statusPollIntervalSeconds"`
	StationOperationTimeoutSeconds int                `json:"stationOperationTimeoutSeconds"`
	PowerConfirmAttemptsOn         int                `json:"powerConfirmAttemptsOn"`
	PowerConfirmAttemptsOff        int                `json:"powerConfirmAttemptsOff"`
	PowerConfirmPollIntervalMs     int                `json:"powerConfirmPollIntervalMs"`
	BootFallbackSeconds            int                `json:"bootFallbackSeconds"`
	SleepFinalWriteTimeoutSeconds  int                `json:"sleepFinalWriteTimeoutSeconds"`
	SleepPrepareGapMs              int                `json:"sleepPrepareGapMs"`
	DiscoveryAttempts              int                `json:"discoveryAttempts"`
	DiscoveryRetryDelayMs          int                `json:"discoveryRetryDelayMs"`
	APIListenAddress               string             `json:"apiListenAddress"`
	PowerWriteAttempts             int                `json:"powerWriteAttempts"`
	OperationRetryDelayMs          int                `json:"operationRetryDelayMs"`
	ChannelConfirmAttempts         int                `json:"channelConfirmAttempts"`
	ChannelConfirmIntervalMs       int                `json:"channelConfirmIntervalMs"`
	ConfirmReconnectThreshold      int                `json:"confirmReconnectThreshold"`
	ConfirmReconnectDelayMs        int                `json:"confirmReconnectDelayMs"`
	IdentifyAttempts               int                `json:"identifyAttempts"`
	PresenceMissThreshold          int                `json:"presenceMissThreshold"`
	RecoveryRetryBaseSeconds       int                `json:"recoveryRetryBaseSeconds"`
	RecoveryRetryMaxSeconds        int                `json:"recoveryRetryMaxSeconds"`
	AbsentStationRetryLimit        int                `json:"absentStationRetryLimit"`
	InitialReadTimeoutSeconds      int                `json:"initialReadTimeoutSeconds"`
	ScanReadPhaseTimeoutSeconds    int                `json:"scanReadPhaseTimeoutSeconds"`
	StatusReadTimeoutSeconds       int                `json:"statusReadTimeoutSeconds"`
	StatusRefreshTimeoutSeconds    int                `json:"statusRefreshTimeoutSeconds"`
	ChannelScanFreshnessSeconds    int                `json:"channelScanFreshnessSeconds"`
	BluetoothInitRetrySeconds      int                `json:"bluetoothInitRetrySeconds"`
	persistenceBlockedErr          error
	lastPersistenceErr             error
	mutex                          sync.RWMutex
}

type persistedConfig struct {
	RenamedStations                map[string]string   `json:"renamedStations"`
	RenamedStationsByAddress       map[string]string   `json:"renamedStationsByAddress,omitempty"`
	AutoSleep                      *autosleep.Settings `json:"autoSleep,omitempty"`
	Language                       string              `json:"language,omitempty"`
	ScanOnStartup                  *bool               `json:"scanOnStartup,omitempty"`
	ScanDurationSeconds            *int                `json:"scanDurationSeconds,omitempty"`
	StatusPollingEnabled           *bool               `json:"statusPollingEnabled,omitempty"`
	BulkPowerTimeoutSeconds        *int                `json:"bulkPowerTimeoutSeconds,omitempty"`
	StatusPollIntervalSeconds      *int                `json:"statusPollIntervalSeconds,omitempty"`
	StationOperationTimeoutSeconds *int                `json:"stationOperationTimeoutSeconds,omitempty"`
	PowerConfirmAttemptsOn         *int                `json:"powerConfirmAttemptsOn,omitempty"`
	PowerConfirmAttemptsOff        *int                `json:"powerConfirmAttemptsOff,omitempty"`
	PowerConfirmPollIntervalMs     *int                `json:"powerConfirmPollIntervalMs,omitempty"`
	BootFallbackSeconds            *int                `json:"bootFallbackSeconds,omitempty"`
	SleepFinalWriteTimeoutSeconds  *int                `json:"sleepFinalWriteTimeoutSeconds,omitempty"`
	SleepPrepareGapMs              *int                `json:"sleepPrepareGapMs,omitempty"`
	DiscoveryAttempts              *int                `json:"discoveryAttempts,omitempty"`
	DiscoveryRetryDelayMs          *int                `json:"discoveryRetryDelayMs,omitempty"`
	APIListenAddress               string              `json:"apiListenAddress,omitempty"`
	PowerWriteAttempts             *int                `json:"powerWriteAttempts,omitempty"`
	OperationRetryDelayMs          *int                `json:"operationRetryDelayMs,omitempty"`
	ChannelConfirmAttempts         *int                `json:"channelConfirmAttempts,omitempty"`
	ChannelConfirmIntervalMs       *int                `json:"channelConfirmIntervalMs,omitempty"`
	ConfirmReconnectThreshold      *int                `json:"confirmReconnectThreshold,omitempty"`
	ConfirmReconnectDelayMs        *int                `json:"confirmReconnectDelayMs,omitempty"`
	IdentifyAttempts               *int                `json:"identifyAttempts,omitempty"`
	PresenceMissThreshold          *int                `json:"presenceMissThreshold,omitempty"`
	RecoveryRetryBaseSeconds       *int                `json:"recoveryRetryBaseSeconds,omitempty"`
	RecoveryRetryMaxSeconds        *int                `json:"recoveryRetryMaxSeconds,omitempty"`
	AbsentStationRetryLimit        *int                `json:"absentStationRetryLimit,omitempty"`
	InitialReadTimeoutSeconds      *int                `json:"initialReadTimeoutSeconds,omitempty"`
	ScanReadPhaseTimeoutSeconds    *int                `json:"scanReadPhaseTimeoutSeconds,omitempty"`
	StatusReadTimeoutSeconds       *int                `json:"statusReadTimeoutSeconds,omitempty"`
	StatusRefreshTimeoutSeconds    *int                `json:"statusRefreshTimeoutSeconds,omitempty"`
	ChannelScanFreshnessSeconds    *int                `json:"channelScanFreshnessSeconds,omitempty"`
	BluetoothInitRetrySeconds      *int                `json:"bluetoothInitRetrySeconds,omitempty"`
}

var (
	configFileReader  = os.ReadFile
	configFileWriter  = writeFileAtomically
	configFileRenamer = os.Rename
)

// NewConfig creates a new Config with defaults
func NewConfig() *Config {
	return &Config{
		RenamedStations:                make(map[string]string),
		RenamedStationsByAddress:       make(map[string]string),
		AutoSleep:                      autosleep.DefaultSettings(),
		ScanOnStartup:                  true,
		ScanDurationSeconds:            DefaultScanDurationSeconds,
		StatusPollingEnabled:           true,
		BulkPowerTimeoutSeconds:        DefaultBulkPowerTimeoutSeconds,
		StatusPollIntervalSeconds:      DefaultStatusPollIntervalSeconds,
		StationOperationTimeoutSeconds: DefaultStationOperationTimeoutSeconds,
		PowerConfirmAttemptsOn:         DefaultPowerConfirmAttemptsOn,
		PowerConfirmAttemptsOff:        DefaultPowerConfirmAttemptsOff,
		PowerConfirmPollIntervalMs:     DefaultPowerConfirmPollIntervalMs,
		BootFallbackSeconds:            DefaultBootFallbackSeconds,
		SleepFinalWriteTimeoutSeconds:  DefaultSleepFinalWriteTimeoutSeconds,
		SleepPrepareGapMs:              DefaultSleepPrepareGapMs,
		DiscoveryAttempts:              DefaultDiscoveryAttempts,
		DiscoveryRetryDelayMs:          DefaultDiscoveryRetryDelayMs,
		APIListenAddress:               DefaultAPIListenAddress,
		PowerWriteAttempts:             DefaultPowerWriteAttempts,
		OperationRetryDelayMs:          DefaultOperationRetryDelayMs,
		ChannelConfirmAttempts:         DefaultChannelConfirmAttempts,
		ChannelConfirmIntervalMs:       DefaultChannelConfirmIntervalMs,
		ConfirmReconnectThreshold:      DefaultConfirmReconnectThreshold,
		ConfirmReconnectDelayMs:        DefaultConfirmReconnectDelayMs,
		IdentifyAttempts:               DefaultIdentifyAttempts,
		PresenceMissThreshold:          DefaultPresenceMissThreshold,
		RecoveryRetryBaseSeconds:       DefaultRecoveryRetryBaseSeconds,
		RecoveryRetryMaxSeconds:        DefaultRecoveryRetryMaxSeconds,
		AbsentStationRetryLimit:        DefaultAbsentStationRetryLimit,
		InitialReadTimeoutSeconds:      DefaultInitialReadTimeoutSeconds,
		ScanReadPhaseTimeoutSeconds:    DefaultScanReadPhaseTimeoutSeconds,
		StatusReadTimeoutSeconds:       DefaultStatusReadTimeoutSeconds,
		StatusRefreshTimeoutSeconds:    DefaultStatusRefreshTimeoutSeconds,
		ChannelScanFreshnessSeconds:    DefaultChannelScanFreshnessSeconds,
		BluetoothInitRetrySeconds:      DefaultBluetoothInitRetrySeconds,
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
		if renameErr := configFileRenamer(configFilePath, invalidPath); renameErr != nil {
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
	if c.BulkPowerTimeoutSeconds < c.StationOperationTimeoutSeconds {
		c.BulkPowerTimeoutSeconds = c.StationOperationTimeoutSeconds
	}
	if c.InitialReadTimeoutSeconds > c.StationOperationTimeoutSeconds {
		c.InitialReadTimeoutSeconds = c.StationOperationTimeoutSeconds
	}
	if c.ScanReadPhaseTimeoutSeconds < c.InitialReadTimeoutSeconds {
		c.ScanReadPhaseTimeoutSeconds = c.InitialReadTimeoutSeconds
	}
	if c.StatusRefreshTimeoutSeconds < c.StatusReadTimeoutSeconds {
		c.StatusRefreshTimeoutSeconds = c.StatusReadTimeoutSeconds
	}
	if c.RecoveryRetryBaseSeconds > c.RecoveryRetryMaxSeconds {
		c.RecoveryRetryBaseSeconds = c.RecoveryRetryMaxSeconds
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
	if c.AutoSleep != (autosleep.Settings{}) {
		autoSleep := sanitizeAutoSleep(&c.AutoSleep)
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
	if timeoutSeconds < c.StationOperationTimeoutSeconds {
		return fmt.Errorf(
			"bulk power timeout must cover the per-station operation timeout of %d seconds, got %d",
			c.StationOperationTimeoutSeconds,
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
	if timeoutSeconds > c.BulkPowerTimeoutSeconds {
		return fmt.Errorf(
			"station operation timeout cannot exceed the bulk power timeout of %d seconds, got %d",
			c.BulkPowerTimeoutSeconds,
			timeoutSeconds,
		)
	}
	if timeoutSeconds < c.InitialReadTimeoutSeconds {
		return fmt.Errorf(
			"station operation timeout must cover the initial read timeout of %d seconds, got %d",
			c.InitialReadTimeoutSeconds,
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

func (c *Config) GetAPIListenAddress() string {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return sanitizeAPIListenAddress(c.APIListenAddress)
}

func (c *Config) SetAPIListenAddress(address string) error {
	if err := validateAPIListenAddress(address); err != nil {
		return err
	}
	c.mutex.Lock()
	defer c.mutex.Unlock()
	previous := c.APIListenAddress
	c.APIListenAddress = address
	if err := c.saveLocked(); err != nil {
		c.APIListenAddress = previous
		return err
	}
	return nil
}

// rangedGet reads an integer setting, repairing out-of-range values with the
// default without mutating the stored state.
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

func (c *Config) GetRecoveryRetryBaseSeconds() int {
	return c.rangedGet(&c.RecoveryRetryBaseSeconds, MinRecoveryRetryBaseSeconds, MaxRecoveryRetryBaseSeconds, DefaultRecoveryRetryBaseSeconds)
}

func (c *Config) RecoveryRetryBase() time.Duration {
	return time.Duration(c.GetRecoveryRetryBaseSeconds()) * time.Second
}

func (c *Config) SetRecoveryRetryBaseSeconds(baseSeconds int) error {
	return c.setRanged("recovery retry base", baseSeconds, MinRecoveryRetryBaseSeconds, MaxRecoveryRetryBaseSeconds, &c.RecoveryRetryBaseSeconds,
		func(c *Config, value int) error {
			if value > c.RecoveryRetryMaxSeconds {
				return fmt.Errorf("recovery retry base must not exceed the recovery retry maximum of %d seconds, got %d", c.RecoveryRetryMaxSeconds, value)
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
			if value < c.RecoveryRetryBaseSeconds {
				return fmt.Errorf("recovery retry maximum must not fall below the recovery retry base of %d seconds, got %d", c.RecoveryRetryBaseSeconds, value)
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
			if value > c.StationOperationTimeoutSeconds {
				return fmt.Errorf("initial read timeout must not exceed the station operation timeout of %d seconds, got %d", c.StationOperationTimeoutSeconds, value)
			}
			if value > c.ScanReadPhaseTimeoutSeconds {
				return fmt.Errorf("initial read timeout must not exceed the scan read phase timeout of %d seconds, got %d", c.ScanReadPhaseTimeoutSeconds, value)
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
			if value < c.InitialReadTimeoutSeconds {
				return fmt.Errorf("scan read phase timeout must cover the initial read timeout of %d seconds, got %d", c.InitialReadTimeoutSeconds, value)
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
			if value > c.StatusRefreshTimeoutSeconds {
				return fmt.Errorf("status read timeout must not exceed the status refresh timeout of %d seconds, got %d", c.StatusRefreshTimeoutSeconds, value)
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
			if value < c.StatusReadTimeoutSeconds {
				return fmt.Errorf("status refresh timeout must cover the status read timeout of %d seconds, got %d", c.StatusReadTimeoutSeconds, value)
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
