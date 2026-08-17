package config

import (
	"os"
	"sync"

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
	// recoveryGeneration advances every time a blocked-save recovery replaces
	// the startup defaults with the persisted file contents. Runtime layers
	// that derive state from the configuration at startup (Bluetooth timing,
	// the auto-sleep watcher, the API listener) observe it to re-apply their
	// side effects after a recovery.
	recoveryGeneration uint64
	mutex              sync.RWMutex
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

// NewConfig creates a new Config with defaults. The default set itself lives
// in applyDefaults (fields.go) so Load's fallback paths share it.
func NewConfig() *Config {
	config := &Config{}
	config.applyDefaults()
	return config
}
