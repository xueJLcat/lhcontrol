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

// ErrScanStopTimeout reports that the scan cancellation was delivered but scan
// processing had not finished within the bounded wait. The cancellation stays
// in effect; status polling observes the terminal state once the blocked
// adapter call returns.
var ErrScanStopTimeout = errors.New("scan stop timed out waiting for scan processing to finish")

// ErrBulkOperationTimeout reports that the whole bulk operation exceeded its
// configured timeout. ErrStationOperationTimeout reports that a single
// station's operation budget expired while the bulk deadline had not.
var ErrBulkOperationTimeout = errors.New(ReasonBulkOperationTimeout)
var ErrStationOperationTimeout = errors.New(ReasonStationOperationTimeout)

// Result Reason values are part of the public result contract; consumers must
// compare them against these constants instead of duplicating the strings.
const (
	ReasonBulkOperationTimeout    = "bulk operation timed out"
	ReasonStationOperationTimeout = "station operation timed out"
	ReasonOperationCancelled      = "operation cancelled"
	ReasonShuttingDown            = "application is shutting down"
	ReasonStationBooting          = "station is booting"
	ReasonAlreadyAtTarget         = "already at target state"
	ReasonUnsupportedCapability   = "power control is not supported"
	ReasonUnsupportedStandby      = "standby is not supported"
	ReasonStationBusy             = "station is busy"
)

// Timing budgets live in config (user-tunable) and are read through the
// Manager accessors; only the non-tunable safety windows stay as constants.
const (
	operationSafetyFreshnessWindow = 45 * time.Second
	metadataFreshnessWindow        = 24 * time.Hour
)

// StationInfo is a simplified representation of a BaseStation for the frontend.
type StationInfo struct {
	Name                string `json:"name"`
	OriginalName        string `json:"originalName"`
	Address             string `json:"address"`
	PowerState          int    `json:"powerState"`
	PowerStateName      string `json:"powerStateName"`
	PowerStateConfirmed bool   `json:"powerStateConfirmed"`
	RawPowerState       int    `json:"rawPowerState"`
	Channel             int    `json:"channel"`
	ChannelConflict     bool   `json:"channelConflict"`
	IsPresent           bool   `json:"isPresent"`
	PresenceUncertain   bool   `json:"presenceUncertain"`
	SeenInLatestScan    bool   `json:"seenInLatestScan"`
	ScanFresh           bool   `json:"scanFresh"`
	MissedScans         int    `json:"missedScans"`
	LastSeenAt          string `json:"lastSeenAt"`
	LastReadAt          string `json:"lastReadAt"`
	LastPowerReadAt     string `json:"lastPowerReadAt"`
	LastChannelReadAt   string `json:"lastChannelReadAt"`
	MetadataReadAt      string `json:"metadataReadAt"`
	LastError           string `json:"lastError"`
	StatusFresh         bool   `json:"statusFresh"`
	PowerFresh          bool   `json:"powerFresh"`
	// PowerOperationallyFresh is the fixed-window counterpart of PowerFresh
	// used before commands. It can be false while a slow polling cadence still
	// permits the same observation to remain visible.
	PowerOperationallyFresh    bool   `json:"powerOperationallyFresh"`
	PowerOperationalFreshUntil string `json:"powerOperationalFreshUntil"`
	ChannelFresh               bool   `json:"channelFresh"`
	// ChannelOperationallyFresh is intentionally stricter than ChannelFresh.
	// ChannelFresh follows the configured polling cadence for display, while
	// this flag mirrors the fixed write-safety window used by conflict checks.
	ChannelOperationallyFresh    bool                     `json:"channelOperationallyFresh"`
	ChannelOperationalFreshUntil string                   `json:"channelOperationalFreshUntil"`
	MetadataFresh                bool                     `json:"metadataFresh"`
	ConnectionState              string                   `json:"connectionState"`
	CapabilitiesKnown            bool                     `json:"capabilitiesKnown"`
	Capabilities                 bluetooth.Capabilities   `json:"capabilities"`
	Metadata                     bluetooth.DeviceMetadata `json:"metadata"`
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
	// ID identifies the scan this status describes. Every scan start assigns
	// a fresh value, so consumers recovering a finished scan can reject a
	// terminal status that a newer scan already overwrote.
	ID          uint64   `json:"id,omitempty"`
	State       string   `json:"state"`
	StartedAt   string   `json:"startedAt"`
	CompletedAt string   `json:"completedAt"`
	Error       string   `json:"error"`
	Warnings    []string `json:"warnings"`
	Found       int      `json:"found"`
}
type ScanCallbacks struct {
	Started   func()
	Completed func(statusID uint64, stations []StationInfo)
	Failed    func(statusID uint64, err error)
	Cancelled func(statusID uint64)
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
	// closeStartedOnce guarantees startedDone closes at most once even if the
	// synchronous scan path and a stop/shutdown teardown ever race on it; a
	// double close would panic the scan subsystem.
	closeStartedOnce sync.Once
	// statusID is the ScanStatus identity assigned when this scan reserved
	// the transition lock; terminal callbacks carry it so consumers can match
	// the status record they read back to this exact scan.
	statusID uint64
}

// closeStarted publishes the Started milestone exactly once.
func (l *scanLifecycle) closeStarted() {
	l.closeStartedOnce.Do(func() { close(l.startedDone) })
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
	operationMutex           sync.RWMutex
	globalOperationMutex     sync.Mutex
	exclusiveOperationActive bool
	foregroundGlobalActive   bool
	foregroundSharedActive   int
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
	scanStatusID            uint64
	scanLifecycleMutex      sync.Mutex
	scanLifecycle           *scanLifecycle
	initializeMutex    sync.Mutex
	initializeErr      error
	initializeFailedAt time.Time
	nextInitializeAt   time.Time
	// initializeAttempted marks that at least one adapter-enable attempt has
	// run. A nil initializeErr is ambiguous without it: it means either "the
	// adapter is initialized" or "no attempt has happened yet", and only the
	// former may skip a new attempt.
	initializeAttempted bool
	// initializePending is non-nil while a bounded initialization attempt runs.
	// Closed once the attempt's outcome is recorded; later ensureReady calls
	// adopt the outcome instead of starting a concurrent adapter.Enable.
	initializePending chan struct{}
	// initializeWg tracks in-flight initialization attempts so shutdown joins
	// them before the fleet disconnect; an attempt an ensureReady waiter
	// abandoned on a timeout keeps running and must not race the drain.
	initializeWg        sync.WaitGroup
	initializeBluetooth func() error
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
	shutdownDrainTimeout    time.Duration
	// Tunable wait limits; tests pin them directly. Production runs leave
	// them at zero and follow the package defaults.
	stopScanTimeout        time.Duration
	adapterCleanupWait     time.Duration
	initializeWait         time.Duration
	foregroundDrainWait    time.Duration
	statusRefreshJoinWait  time.Duration
	shuttingDown            atomic.Bool
	shutdownOnce            sync.Once
	shutdownCh              chan struct{}
	shutdownDraining        chan struct{}
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
	// has its own due time so it cannot reset an active connection backoff;
	// while a backoff is active the marker is clamped to it so recovery does
	// not re-run the failed read ahead of schedule.
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
)

// metadataRetryLimit caps metadata recovery attempts per backoff window. It is
// a plain count, declared outside the retry-kind bitmask block above so it is
// never mistaken for a bit flag.
const metadataRetryLimit = 5
