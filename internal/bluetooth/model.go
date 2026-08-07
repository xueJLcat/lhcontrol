package bluetooth

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"tinygo.org/x/bluetooth"
)

type BaseStation struct {
	Name              string
	Address           bluetooth.Address
	PowerState        PowerState
	RawPowerState     int
	Channel           int
	Present           bool
	Capabilities      Capabilities
	CapabilitiesKnown bool
	Metadata          DeviceMetadata
	// Fields for storing handles and state
	device                 bluetooth.GAPDevice
	pendingCleanup         bluetooth.GAPDevice
	characteristic         characteristicIO
	modeCharacteristic     characteristicIO
	identifyCharacteristic characteristicIO
	isConnected            bool
	// Add Mutex for thread-safe access
	mutex             sync.RWMutex
	invalidationMutex sync.Mutex
	// pendingInvalidation coalesces OS disconnect notifications into at most
	// one in-flight invalidation goroutine per station; only the latest
	// disconnected device is kept because invalidation is identity-checked.
	pendingInvalidation *bluetooth.Device
	invalidationRunning bool
	LastStateUpdate     time.Time // Track when state was last read
	LastSeenAt          time.Time
	LastReadAt          time.Time
	LastPowerReadAt     time.Time
	LastChannelReadAt   time.Time
	MetadataReadAt      time.Time
	LastError           string
	MissedScans         int
	bootingSince        time.Time
	// bootRawTrustedOn remembers that this connection has continuously
	// reported a boot-like raw value for long enough to use the compatibility
	// fallback. It is cleared on disconnect and before a power command so a
	// genuine reconnect/reboot gets a fresh transition window.
	bootRawTrustedOn  bool
	presenceUncertain bool
	connectionError   string
	powerError        string
	channelError      string
	metadataError     string
	metadataReadError error
	operationError    string
}

// PossiblySentError reports that a write failed after the transport may have
// accepted the command. Such commands must be confirmed, never replayed.
type PossiblySentError struct {
	Err error
}

func (e *PossiblySentError) Error() string {
	return fmt.Sprintf("command may have been sent: %v", e.Err)
}

func (e *PossiblySentError) Unwrap() error {
	return e.Err
}

func (e *PossiblySentError) PossiblySent() bool {
	return true
}

// IsPossiblySent accepts the application marker and compatible transport
// markers if tinygo-bluetooth adds its own typed error.
func IsPossiblySent(err error) bool {
	possiblySent, classified := possiblySentClassification(err)
	return classified && possiblySent
}

func possiblySentClassification(err error) (possiblySent, classified bool) {
	var marker interface{ PossiblySent() bool }
	if errors.As(err, &marker) {
		return marker.PossiblySent(), true
	}
	var alternate interface{ MayHaveBeenSent() bool }
	if errors.As(err, &alternate) {
		return alternate.MayHaveBeenSent(), true
	}
	return false, false
}

// DiscoveredStation contains only immutable scan data. Keeping the mutex-bearing
// BaseStation out of scan results avoids copying a sync.RWMutex.
type DiscoveredStation struct {
	Name    string
	Address bluetooth.Address
}

func (bs *BaseStation) IsConnected() bool {
	bs.mutex.Lock()
	defer bs.mutex.Unlock()
	if !bs.isConnected || bs.device == nil {
		return false
	}
	connected, err := bs.device.Connected()
	if err == nil && connected {
		return true
	}
	if err != nil {
		bs.setConnectionErrorInternal(err)
	}
	_ = disconnectInternal(bs)
	return false
}

// setPowerStateInternal updates the power state and timestamp safely.
// Assumes caller holds the write lock (bs.mutex.Lock()).
func (bs *BaseStation) setPowerStateInternal(state PowerState, raw int) {
	if state == PowerStateBooting {
		if bs.PowerState != PowerStateBooting || bs.bootingSince.IsZero() {
			bs.bootingSince = time.Now()
		}
	} else {
		bs.bootingSince = time.Time{}
	}
	bs.PowerState = state
	bs.RawPowerState = raw
	bs.LastStateUpdate = time.Now()
}

func (bs *BaseStation) updateLastReadInternal(readAt time.Time) {
	if readAt.After(bs.LastReadAt) {
		bs.LastReadAt = readAt
	}
}

func (bs *BaseStation) refreshLastErrorInternal() {
	parts := make([]string, 0, 5)
	for _, item := range []struct {
		label string
		value string
	}{
		{label: "connection", value: bs.connectionError},
		{label: "power", value: bs.powerError},
		{label: "channel", value: bs.channelError},
		{label: "metadata", value: bs.metadataError},
		{label: "operation", value: bs.operationError},
	} {
		if item.value != "" {
			parts = append(parts, fmt.Sprintf("%s: %s", item.label, item.value))
		}
	}
	bs.LastError = strings.Join(parts, "; ")
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (bs *BaseStation) setConnectionErrorInternal(err error) {
	bs.connectionError = errorText(err)
	bs.refreshLastErrorInternal()
}

func (bs *BaseStation) setPowerErrorInternal(err error) {
	bs.powerError = errorText(err)
	bs.refreshLastErrorInternal()
}

func (bs *BaseStation) setChannelErrorInternal(err error) {
	bs.channelError = errorText(err)
	bs.refreshLastErrorInternal()
}

func (bs *BaseStation) setMetadataErrorInternal(err error) {
	bs.metadataError = errorText(err)
	bs.metadataReadError = err
	bs.refreshLastErrorInternal()
}

func (bs *BaseStation) setOperationErrorInternal(err error) {
	bs.operationError = errorText(err)
	bs.refreshLastErrorInternal()
}

type BaseStationSnapshot struct {
	Name              string
	Address           string
	PowerState        PowerState
	RawPowerState     int
	Channel           int
	Present           bool
	Capabilities      Capabilities
	CapabilitiesKnown bool
	Metadata          DeviceMetadata
	LastStateUpdate   time.Time
	LastSeenAt        time.Time
	LastReadAt        time.Time
	LastPowerReadAt   time.Time
	LastChannelReadAt time.Time
	MetadataReadAt    time.Time
	LastError         string
	MissedScans       int
	Connected         bool
	PresenceUncertain bool
}

func (bs *BaseStation) Snapshot() BaseStationSnapshot {
	bs.mutex.RLock()
	defer bs.mutex.RUnlock()
	return BaseStationSnapshot{
		Name:              bs.Name,
		Address:           bs.Address.String(),
		PowerState:        bs.PowerState,
		RawPowerState:     bs.RawPowerState,
		Channel:           bs.Channel,
		Present:           bs.Present,
		Capabilities:      bs.Capabilities,
		CapabilitiesKnown: bs.CapabilitiesKnown,
		Metadata:          bs.Metadata,
		LastStateUpdate:   bs.LastStateUpdate,
		LastSeenAt:        bs.LastSeenAt,
		LastReadAt:        bs.LastReadAt,
		LastPowerReadAt:   bs.LastPowerReadAt,
		LastChannelReadAt: bs.LastChannelReadAt,
		MetadataReadAt:    bs.MetadataReadAt,
		LastError:         bs.LastError,
		MissedScans:       bs.MissedScans,
		Connected:         bs.isConnected && bs.device != nil,
		PresenceUncertain: bs.presenceUncertain,
	}
}

func (bs *BaseStation) SetPresent(present bool) {
	bs.mutex.Lock()
	bs.Present = present
	bs.mutex.Unlock()
}

func (bs *BaseStation) MarkSeen(now time.Time) {
	bs.mutex.Lock()
	bs.Present = true
	bs.MissedScans = 0
	bs.LastSeenAt = now
	bs.presenceUncertain = false
	bs.mutex.Unlock()
}

// MarkMissed requires two consecutive successful scans to mark a station
// absent. A single Windows BLE scan can miss a station while its GATT session
// is still being released.
func (bs *BaseStation) MarkMissed() {
	bs.mutex.Lock()
	bs.presenceUncertain = false
	bs.MissedScans++
	if bs.MissedScans >= 2 {
		bs.Present = false
	}
	bs.mutex.Unlock()
}

// MarkPresenceUncertain records that a completed scan could not reliably
// determine whether this station advertised. It does not count as a miss and
// therefore cannot move a known station closer to the absence threshold.
func (bs *BaseStation) MarkPresenceUncertain() {
	bs.mutex.Lock()
	bs.presenceUncertain = true
	bs.mutex.Unlock()
}

func (bs *BaseStation) UpdateName(name string) {
	bs.mutex.Lock()
	bs.Name = name
	bs.mutex.Unlock()
}

// Initialize sets up the Bluetooth adapter and parses UUIDs.
