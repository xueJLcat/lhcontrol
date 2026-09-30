package station

import (
	"context"
	"errors"
	"testing"
	"time"

	"lhcontrol/internal/bluetooth"
	"lhcontrol/internal/config"
)

func TestPowerVerificationInterruptionKeepsObservedConnectionFailure(t *testing.T) {
	for _, bulk := range []bool{false, true} {
		name := "single"
		if bulk {
			name = "bulk"
		}
		t.Run(name, func(t *testing.T) {
			for _, transportFailure := range []bool{false, true} {
				name := "pure deadline"
				if transportFailure {
					name = "connection failure and deadline"
				}
				t.Run(name, func(t *testing.T) {
					m := NewManager(config.NewConfig())
					defer m.Shutdown()
					m.statusRecoveryStart.Do(func() {})
					m.stationOperationTimeout = 5 * time.Millisecond
					address := "11:22:33:44:55:E1"
					s := &bluetooth.BaseStation{
						Name: "LHB-VERIFY", Address: mustAddress(t, address), Present: true,
						CapabilitiesKnown: true, Capabilities: bluetooth.Capabilities{PowerWrite: true},
					}
					m.stations[address] = s
					failure := &bluetooth.DeviceTransportError{Operation: "connect for verification", Err: errors.New("link lost")}
					m.bluetoothOps.fetchInitialPowerState = func(ctx context.Context, _ *bluetooth.BaseStation) error {
						<-ctx.Done()
						if transportFailure {
							return failure
						}
						return ctx.Err()
					}
					writes := 0
					m.bluetoothOps.setPowerState = func(ctx context.Context, _ *bluetooth.BaseStation, _ bluetooth.PowerState) (bluetooth.PowerControlResult, error) {
						writes++
						return bluetooth.PowerControlResult{}, ctx.Err()
					}
					disconnects := 0
					m.bluetoothOps.disconnectStation = func(*bluetooth.BaseStation) error { disconnects++; return nil }
					if bulk {
						result, err := m.SetAllStationsPowerDetailed("on")
						if err != nil || len(result.Results) != 1 {
							t.Fatalf("bulk = %+v, %v", result, err)
						}
						entry := result.Results[0]
						if transportFailure && (entry.Skipped || entry.Success || entry.Error == "") {
							t.Errorf("observed fault reported as interruption: %+v", entry)
						}
						if !transportFailure && (!entry.Skipped || entry.Error != "" || entry.Reason != ReasonStationOperationTimeout) {
							t.Errorf("pure deadline = %+v, want timeout skip", entry)
						}
					} else {
						_, err := m.SetStationPower(address, "on")
						if !errors.Is(err, ErrStationOperationTimeout) {
							t.Errorf("error = %v, want station timeout", err)
						}
						if transportFailure && !errors.Is(err, failure) {
							t.Errorf("error = %v, lost observed fault", err)
						}
					}
					if writes != 0 {
						t.Errorf("started %d write phases after verification exhausted the budget", writes)
					}
					retry := m.statusRetrySnapshot(address)
					wantFailures := 0
					if transportFailure {
						wantFailures = 1
					}
					if retry.failures != wantFailures || disconnects != wantFailures {
						t.Errorf("failures=%d disconnects=%d, want %d each", retry.failures, disconnects, wantFailures)
					}
				})
			}
		})
	}
}
