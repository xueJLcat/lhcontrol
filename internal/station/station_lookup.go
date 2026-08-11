package station

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"lhcontrol/internal/bluetooth"
)

func (m *Manager) PowerOnStation(address string) error {
	_, err := m.SetStationPower(address, "on")
	return err
}

func (m *Manager) PowerOffStation(address string) error {
	_, err := m.SetStationPower(address, "sleep")
	return err
}

func (m *Manager) stationByAddress(address string) (*bluetooth.BaseStation, error) {
	m.stationsMutex.RLock()
	stationPtr, ok := m.stations[address]
	if !ok {
		for stationAddress, candidate := range m.stations {
			if strings.EqualFold(stationAddress, address) {
				stationPtr, ok = candidate, true
				break
			}
		}
	}
	m.stationsMutex.RUnlock()
	if !ok || stationPtr == nil {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, address)
	}
	return stationPtr, nil
}

func (m *Manager) stationInfoByAddress(address string) (StationInfo, error) {
	for _, info := range m.GetStationInfo() {
		if strings.EqualFold(info.Address, address) {
			return info, nil
		}
	}
	return StationInfo{}, fmt.Errorf("%w: %s", ErrNotFound, address)
}

// SetStationPower sets one of the three stable target states. Confirmed is
// false when the firmware supports writing but does not expose power reads.
func (m *Manager) cachedPowerOutcome(stationPtr *bluetooth.BaseStation, target bluetooth.PowerState) (PowerActionResult, error, bool) {
	snapshot := stationPtr.Snapshot()
	switch classifyCachedPower(snapshot, target, time.Now()) {
	case cachedPowerBooting:
		return PowerActionResult{}, fmt.Errorf("station is booting; retry after transition: %w", ErrStationTransitioning), true
	case cachedPowerActionable:
		return PowerActionResult{}, nil, false
	}
	info, err := m.stationInfoByAddress(snapshot.Address)
	if err != nil {
		return PowerActionResult{}, err, true
	}
	return PowerActionResult{
		Station:   info,
		Skipped:   true,
		Reason:    ReasonAlreadyAtTarget,
		Confirmed: true,
	}, nil, true
}

// stationOperationContextError converts an expired per-station budget into the
// public timeout sentinel while keeping cancellation untouched so shutdown
// detection can still match context.Canceled upstream.
func stationOperationContextError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w: %w", ErrStationOperationTimeout, err)
	}
	return err
}

type cachedPowerDisposition uint8

const (
	cachedPowerActionable cachedPowerDisposition = iota
	cachedPowerBooting
	cachedPowerAtTarget
)

func isFreshBootingPower(snapshot bluetooth.BaseStationSnapshot, now time.Time) bool {
	return snapshot.PowerState == bluetooth.PowerStateBooting &&
		isOperationallyFresh(snapshot.LastPowerReadAt, now)
}

func classifyCachedPower(
	snapshot bluetooth.BaseStationSnapshot,
	target bluetooth.PowerState,
	now time.Time,
) cachedPowerDisposition {
	if isFreshBootingPower(snapshot, now) {
		return cachedPowerBooting
	}
	if !isOperationallyFresh(snapshot.LastPowerReadAt, now) {
		return cachedPowerActionable
	}
	if snapshot.PowerState == target &&
		bluetooth.IsPowerStateVerified(snapshot.PowerState, snapshot.RawPowerState) {
		return cachedPowerAtTarget
	}
	return cachedPowerActionable
}

func powerReadSucceeded(err error) bool {
	if err == nil {
		return true
	}
	var initialErr *bluetooth.InitialReadError
	return errors.As(err, &initialErr) && initialErr.Power == nil
}

// recordPowerVerificationResult tracks the power and channel observations made
// by a cache verification independently. Metadata errors are deliberately
// excluded because a connected fetch can surface an old discovery error rather
// than a fresh metadata attempt.
func (m *Manager) recordPowerVerificationResult(
	station *bluetooth.BaseStation,
	address string,
	before bluetooth.BaseStationSnapshot,
	err error,
) {
	after := station.Snapshot()
	powerObserved := after.LastPowerReadAt.After(before.LastPowerReadAt)
	channelObserved := after.LastChannelReadAt.After(before.LastChannelReadAt)
	var powerErr error
	var channelErr error
	var initialErr *bluetooth.InitialReadError
	if err != nil && !errors.As(err, &initialErr) {
		return
	}
	if initialErr != nil {
		powerErr = initialErr.Power
		channelErr = initialErr.Channel
		powerObserved = powerObserved || powerErr != nil
		channelObserved = channelObserved || channelErr != nil
		m.observeBluetoothError(errors.Join(powerErr, channelErr))
	}
	m.recordObservedReadResult(
		station,
		address,
		powerObserved,
		powerErr,
		channelObserved,
		channelErr,
	)
}
