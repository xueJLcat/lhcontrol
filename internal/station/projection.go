package station

import (
	"context"
	"fmt"
	"lhcontrol/internal/bluetooth"
	"log"
	"runtime/debug"
	"sort"
	"strings"
	"time"
)

func (m *Manager) GetStationInfo() []StationInfo {
	// Copy the station pointers under the read lock and snapshot them after
	// releasing it: Snapshot takes each station's mutex, which a slow GATT
	// operation can hold for seconds, and holding stationsMutex meanwhile would
	// queue every scan merge and other reader behind the whole fleet. Station
	// pointers are never removed from the map, so using them after the unlock
	// is safe.
	m.stationsMutex.RLock()
	stationPtrs := make([]*bluetooth.BaseStation, 0, len(m.stations))
	for _, stationPtr := range m.stations {
		if stationPtr == nil {
			continue
		}
		stationPtrs = append(stationPtrs, stationPtr)
	}
	m.stationsMutex.RUnlock()
	snapshots := make([]bluetooth.BaseStationSnapshot, 0, len(stationPtrs))
	for _, stationPtr := range stationPtrs {
		snapshots = append(snapshots, stationPtr.Snapshot())
	}
	channelCounts := make(map[int]int)
	now := time.Now()
	displayFreshnessWindow := m.config.StatusDisplayFreshnessWindow()
	for _, snapshot := range snapshots {
		if snapshot.Present &&
			snapshot.MissedScans == 0 &&
			!snapshot.PresenceUncertain &&
			isRecent(snapshot.LastSeenAt, now, m.channelScanFreshnessWindowDuration()) &&
			snapshot.Channel != bluetooth.ChannelUnknown &&
			isRecent(snapshot.LastChannelReadAt, now, displayFreshnessWindow) {
			channelCounts[snapshot.Channel]++
		}
	}
	stationInfos := make([]StationInfo, 0, len(snapshots))
	for _, snapshot := range snapshots {
		name := snapshot.Name
		if renamedName, ok := m.config.GetStationDisplayName(snapshot.Address, snapshot.Name); ok {
			name = renamedName
		}
		connectionState := "disconnected"
		if snapshot.Connected {
			connectionState = "connected"
		}
		powerFresh := snapshot.RawPowerState != bluetooth.RawPowerStateUnknown && isRecent(snapshot.LastPowerReadAt, now, displayFreshnessWindow)
		powerOperationallyFresh := snapshot.RawPowerState != bluetooth.RawPowerStateUnknown &&
			isOperationallyFresh(snapshot.LastPowerReadAt, now)
		powerOperationalFreshUntil := ""
		if snapshot.RawPowerState != bluetooth.RawPowerStateUnknown && !snapshot.LastPowerReadAt.IsZero() {
			powerOperationalFreshUntil = formatTimestamp(snapshot.LastPowerReadAt.Add(operationSafetyFreshnessWindow))
		}
		channelFresh := snapshot.Channel != bluetooth.ChannelUnknown && isRecent(snapshot.LastChannelReadAt, now, displayFreshnessWindow)
		channelOperationallyFresh := snapshot.Channel != bluetooth.ChannelUnknown &&
			isOperationallyFresh(snapshot.LastChannelReadAt, now)
		channelOperationalFreshUntil := ""
		if snapshot.Channel != bluetooth.ChannelUnknown && !snapshot.LastChannelReadAt.IsZero() {
			channelOperationalFreshUntil = formatTimestamp(snapshot.LastChannelReadAt.Add(operationSafetyFreshnessWindow))
		}
		seenInLatestScan := snapshot.MissedScans == 0 &&
			!snapshot.PresenceUncertain &&
			!snapshot.LastSeenAt.IsZero()
		scanFresh := seenInLatestScan &&
			isRecent(snapshot.LastSeenAt, now, m.channelScanFreshnessWindowDuration())
		metadataFresh := isRecent(snapshot.MetadataReadAt, now, metadataFreshnessWindow)
		stationInfos = append(stationInfos, StationInfo{
			Name:                name,
			OriginalName:        snapshot.Name,
			Address:             snapshot.Address,
			PowerState:          int(snapshot.PowerState),
			PowerStateName:      snapshot.PowerState.String(),
			PowerStateConfirmed: powerFresh && bluetooth.IsPowerStateVerified(snapshot.PowerState, snapshot.RawPowerState),
			RawPowerState:       snapshot.RawPowerState,
			Channel:             snapshot.Channel,
			ChannelConflict: snapshot.Present && scanFresh && channelFresh &&
				channelCounts[snapshot.Channel] > 1,
			IsPresent:                    snapshot.Present,
			PresenceUncertain:            snapshot.PresenceUncertain,
			SeenInLatestScan:             seenInLatestScan,
			ScanFresh:                    scanFresh,
			MissedScans:                  snapshot.MissedScans,
			LastSeenAt:                   formatTimestamp(snapshot.LastSeenAt),
			LastReadAt:                   formatTimestamp(snapshot.LastReadAt),
			LastPowerReadAt:              formatTimestamp(snapshot.LastPowerReadAt),
			LastChannelReadAt:            formatTimestamp(snapshot.LastChannelReadAt),
			MetadataReadAt:               formatTimestamp(snapshot.MetadataReadAt),
			LastError:                    snapshot.LastError,
			StatusFresh:                  powerFresh || channelFresh,
			PowerFresh:                   powerFresh,
			PowerOperationallyFresh:      powerOperationallyFresh,
			PowerOperationalFreshUntil:   powerOperationalFreshUntil,
			ChannelFresh:                 channelFresh,
			ChannelOperationallyFresh:    channelOperationallyFresh,
			ChannelOperationalFreshUntil: channelOperationalFreshUntil,
			MetadataFresh:                metadataFresh,
			ConnectionState:              connectionState,
			CapabilitiesKnown:            snapshot.CapabilitiesKnown,
			Capabilities:                 snapshot.Capabilities,
			Metadata:                     snapshot.Metadata,
		})
	}
	sort.Slice(stationInfos, func(i, j int) bool {
		return stationValuesLess(
			stationInfos[i].Channel, stationInfos[i].Name, stationInfos[i].Address,
			stationInfos[j].Channel, stationInfos[j].Name, stationInfos[j].Address,
		)
	})
	return stationInfos
}
func stationValuesLess(leftChannel int, leftName, leftAddress string, rightChannel int, rightName, rightAddress string) bool {
	if leftChannel <= bluetooth.ChannelUnknown {
		leftChannel = int(^uint(0) >> 1)
	}
	if rightChannel <= bluetooth.ChannelUnknown {
		rightChannel = int(^uint(0) >> 1)
	}
	if leftChannel != rightChannel {
		return leftChannel < rightChannel
	}
	if left, right := strings.ToLower(leftName), strings.ToLower(rightName); left != right {
		return left < right
	}
	return strings.ToLower(leftAddress) < strings.ToLower(rightAddress)
}
func isOperationallyFresh(value, now time.Time) bool {
	return isRecent(value, now, operationSafetyFreshnessWindow)
}
func isRecent(value, now time.Time, window time.Duration) bool {
	age := now.Sub(value)
	return !value.IsZero() && age >= 0 && age <= window
}
func formatTimestamp(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Format(time.RFC3339Nano)
}
func runSafely(scope string, operation func() error) (returnErr error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			returnErr = fmt.Errorf("%s panicked: %v\n%s", scope, recovered, debug.Stack())
			log.Printf("Recovered panic: %v", returnErr)
		}
	}()
	return operation()
}
func (m *Manager) scanAndFetchStationsSafely(ctx context.Context) (stations []StationInfo, found int, returnErr error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			returnErr = fmt.Errorf("scan workflow panicked: %v\n%s", recovered, debug.Stack())
			stations = m.GetStationInfo()
			log.Printf("Recovered panic: %v", returnErr)
		}
	}()
	return m.scanAndFetchStations(ctx)
}
