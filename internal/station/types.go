package station

import (
	"context"
	"errors"
	"lhcontrol/internal/bluetooth"
	"lhcontrol/internal/config"
	"sync"
	"sync/atomic"
	"time"
)

var ErrOperationInProgress = errors.New("another Bluetooth operation is already in progress")
var ErrStationTransitioning = errors.New("station is transitioning between power states")
var ErrNotFound = errors.New("station not found")
var ErrInvalidArgument = errors.New("invalid argument")
var ErrUnsupported = errors.New("operation is not supported")
var ErrChannelConflict = errors.New("channel conflicts with another visible station")
var ErrScanRequired = errors.New("a recent successful scan is required")
var ErrShuttingDown = errors.New("application is shutting down")

const (
	statusFreshnessWindow          = 45 * time.Second
	channelScanFreshnessWindow     = 2 * time.Minute
	defaultInitialReadTimeout      = 30 * time.Second
	defaultStatusReadTimeout       = 20 * time.Second
	defaultInitialReadPhaseTimeout = 45 * time.Second
	defaultStatusRefreshTimeout    = 30 * time.Second
	defaultStationOperationTimeout = 30 * time.Second
	metadataFreshnessWindow        = 24 * time.Hour
)

// StationInfo is a simplified representation of a BaseStation for the frontend.
type StationInfo struct {
	Name                string                   `json:"name"`
	OriginalName        string                   `json:"originalName"`
	Address             string                   `json:"address"`
	PowerState          int                      `json:"powerState"`
	PowerStateName      string                   `json:"powerStateName"`
	PowerStateConfirmed bool                     `json:"powerStateConfirmed"`
	RawPowerState       int                      `json:"rawPowerState"`
	Channel             int                      `json:"channel"`
	ChannelConflict     bool                     `json:"channelConflict"`
	IsPresent           bool                     `json:"isPresent"`
	PresenceUncertain   bool                     `json:"presenceUncertain"`
	SeenInLatestScan    bool                     `json:"seenInLatestScan"`
	ScanFresh           bool                     `json:"scanFresh"`
	MissedScans         int                      `json:"missedScans"`
	LastSeenAt          string                   `json:"lastSeenAt"`
	LastReadAt          string                   `json:"lastReadAt"`
	LastPowerReadAt     string                   `json:"lastPowerReadAt"`
	LastChannelReadAt   string                   `json:"lastChannelReadAt"`
	MetadataReadAt      string                   `json:"metadataReadAt"`
	LastError           string                   `json:"lastError"`
	StatusFresh         bool                     `json:"statusFresh"`
	PowerFresh          bool                     `json:"powerFresh"`
	ChannelFresh        bool                     `json:"channelFresh"`
	MetadataFresh       bool                     `json:"metadataFresh"`
	ConnectionState     string                   `json:"connectionState"`
	CapabilitiesKnown   bool                     `json:"capabilitiesKnown"`
	Capabilities        bluetooth.Capabilities   `json:"capabilities"`
	Metadata            bluetooth.DeviceMetadata `json:"metadata"`
}
type PowerActionResult struct {
	Station           StationInfo `json:"station"`
	CommandSent       bool        `json:"commandSent"`
	Skipped           bool        `json:"skipped"`
	Reason            string      `json:"reason,omitempty"`
	Confirmed         bool        `json:"confirmed"`
	ConfirmationError string      `json:"confirmationError"`
}
type BulkPowerStationResult struct {
	Address     string      `json:"address"`
	Name        string      `json:"name"`
	Skipped     bool        `json:"skipped"`
	Reason      string      `json:"reason"`
	CommandSent bool        `json:"commandSent"`
	Success     bool        `json:"success"`
	Confirmed   bool        `json:"confirmed"`
	Error       string      `json:"error"`
	Station     StationInfo `json:"station"`
}
type BulkPowerResult struct {
	Target    string                   `json:"target"`
	Results   []BulkPowerStationResult `json:"results"`
	Cancelled bool                     `json:"cancelled"`
	TimedOut  bool                     `json:"timedOut"`
}
type ChannelChangeResult struct {
	Address           string      `json:"address"`
	PreviousChannel   int         `json:"previousChannel"`
	Channel           int         `json:"channel"`
	CommandSent       bool        `json:"commandSent"`
	Confirmed         bool        `json:"confirmed"`
	ConfirmationError string      `json:"confirmationError"`
	Warnings          []string    `json:"warnings"`
	Station           StationInfo `json:"station"`
}
type ScanStatus struct {
	State       string   `json:"state"`
	StartedAt   string   `json:"startedAt"`
	CompletedAt string   `json:"completedAt"`
	Error       string   `json:"error"`
	Warnings    []string `json:"warnings"`
	Found       int      `json:"found"`
}
type ScanCallbacks struct {
	Started   func()
	Completed func([]StationInfo)
	Failed    func(error)
	Cancelled func()
}
type bluetoothOperations struct {
	scanForDurationContext func(context.Context, time.Duration) ([]bluetooth.DiscoveredStation, error)
	readPowerStateContext  func(context.Context, *bluetooth.BaseStation) error
	fetchInitialPowerState func(context.Context, *bluetooth.BaseStation) error
	ensureCapabilities     func(context.Context, *bluetooth.BaseStation) (bluetooth.Capabilities, error)
	refreshCapabilities    func(context.Context, *bluetooth.BaseStation) (bluetooth.Capabilities, error)
	setPowerState          func(context.Context, *bluetooth.BaseStation, bluetooth.PowerState) (bluetooth.PowerControlResult, error)
	identify               func(context.Context, *bluetooth.BaseStation) error
	setChannel             func(context.Context, *bluetooth.BaseStation, int) (bluetooth.ChannelWriteResult, error)
	disconnectStation      func(*bluetooth.BaseStation) error
	releaseStationForScan  func(*bluetooth.BaseStation) error
	stationConnected       func(*bluetooth.BaseStation) bool
}
type scanLifecycle struct {
	ctx         context.Context
	cancel      context.CancelFunc
	done        chan struct{}
	startedDone chan struct{}
}

type bulkPowerLifecycle struct {
	cancel context.CancelFunc
	done   chan struct{}
}
type deviceOperationKind uint8

const (
	deviceOperationForeground deviceOperationKind = iota
	deviceOperationStatus
	deviceOperationRecovery
)

type activeDeviceOperation struct {
	kind   deviceOperationKind
	done   chan struct{}
	cancel context.CancelFunc
}
type deviceOperationBusyError struct {
	backgroundDone   <-chan struct{}
	cancelBackground context.CancelFunc
}

func (e *deviceOperationBusyError) Error() string {
	return ErrOperationInProgress.Error()
}
func (e *deviceOperationBusyError) Unwrap() error {
	return ErrOperationInProgress
}

type Manager struct {
	stations                map[string]*bluetooth.BaseStation
	stationsMutex           sync.RWMutex
	config                  *config.Config
	operationMutex          sync.RWMutex
	globalOperationMutex    sync.Mutex
	foregroundGlobalActive  bool
	foregroundSharedActive  int
	scanTransitionMutex     sync.Mutex
	statusOperationMutex    sync.Mutex
	statusLifecycleMutex    sync.Mutex
	statusOperationDone     chan struct{}
	cancelStatusOperation   context.CancelFunc
	channelOperationMutex   sync.Mutex
	bulkLifecycleMutex      sync.Mutex
	bulkLifecycle           *bulkPowerLifecycle
	deviceOperationMutex    sync.Mutex
	activeDeviceOperations  map[string]activeDeviceOperation
	deviceOperationSlots    chan struct{}
	recoveryOperationMutex  sync.Mutex
	recoveryOperationDone   chan struct{}
	recoveryContext         context.Context
	cancelRecovery          context.CancelFunc
	recoveryGeneration      uint64
	foregroundSlotMissHook  func()
	scanLifecycleStartHook  func()
	scanReadyHook           func()
	isScanning              atomic.Bool
	scanStatusMutex         sync.RWMutex
	scanStatus              ScanStatus
	scanLifecycleMutex      sync.Mutex
	scanLifecycle           *scanLifecycle
	initializeMutex         sync.Mutex
	initializeErr           error
	nextInitializeAt        time.Time
	initializeBluetooth     func() error
	asyncScanWg             sync.WaitGroup
	scanCallbackWg          sync.WaitGroup
	statusRetryMutex        sync.Mutex
	statusRetries           map[string]statusRetry
	statusRecoveryRunning   atomic.Bool
	statusRecoveryStart     sync.Once
	statusRecoveryWake      chan struct{}
	statusRecoveryWg        sync.WaitGroup
	statusRetryBase         time.Duration
	statusRetryMax          time.Duration
	statusBusyRetry         time.Duration
	initialReadTimeout      time.Duration
	initialReadPhaseTimeout time.Duration
	statusReadTimeout       time.Duration
	statusRefreshTimeout    time.Duration
	stationOperationTimeout time.Duration
	shuttingDown            atomic.Bool
	shutdownOnce            sync.Once
	shutdownCh              chan struct{}
	lifecycleContext        context.Context
	cancelLifecycle         context.CancelFunc
	lifecycleMutex          sync.Mutex
	lifecycleCond           *sync.Cond
	activeOperations        int
	bluetoothOps            bluetoothOperations
}
type statusRetry struct {
	// The original fields are the connection retry schedule. Keeping them
	// separate from channel retry state prevents an optional channel failure
	// from accelerating or delaying connection recovery. Refresh pending work
	// has its own due time so it cannot reset an active connection backoff
	// or be delayed by one.
	failures            int
	lastAttempt         time.Time
	nextAt              time.Time
	channelFailures     int
	channelLastAttempt  time.Time
	channelNextAt       time.Time
	metadataFailures    int
	metadataLastAttempt time.Time
	metadataNextAt      time.Time
	refreshNextAt       time.Time
	kinds               statusRetryKind
}
type statusRetryKind uint8

const (
	statusRetryConnection statusRetryKind = 1 << iota
	statusRetryChannel
	statusRetryMetadata
	statusRetryRefresh
	statusAbsentRetryLimit = 5
	metadataRetryLimit     = 5
)
