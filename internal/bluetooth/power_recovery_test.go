package bluetooth

import (
	"testing"
	"time"
)

// TestPowerOnConfirmationCoversLongBootFallbackWindow covers firmware that
// keeps reporting boot-like raw values while already awake: the configured
// attempt count alone would exhaust before the boot fallback window fires,
// so the power-on poll must cover the window and still confirm.
func TestPowerOnConfirmationCoversLongBootFallbackWindow(t *testing.T) {
	ConfigureTiming(TimingPolicy{
		ConfirmAttemptsOn:   2,
		ConfirmPollInterval: 2 * time.Millisecond,
		BootFallbackAfter:   40 * time.Millisecond,
	})
	t.Cleanup(func() { ConfigureTiming(TimingPolicy{}) })

	power := &fakeCharacteristic{value: []byte{0x01}, ignoreWrite: true}
	station := connectedFakeStation(power, nil, nil, Capabilities{PowerRead: true, PowerWrite: true})

	start := time.Now()
	result, err := SetPowerState(station, PowerStateOn)
	if err != nil || !result.Confirmed || result.State != PowerStateOn {
		t.Fatalf("SetPowerState() result=%+v error=%v, want confirmation after the boot fallback window", result, err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("power-on confirmation took %v, want the bounded fallback window", elapsed)
	}
	if len(power.writes) != 1 {
		t.Fatalf("writes = %d, want a single power-on command", len(power.writes))
	}
}

// TestStandbyRefusedAfterDowngradeWithoutRewrite covers a station whose
// standby write was previously rejected with Value Not Allowed: the command
// must be refused up front instead of replaying the rejected write.
func TestStandbyRefusedAfterDowngradeWithoutRewrite(t *testing.T) {
	power := &fakeCharacteristic{value: []byte{0x09}, powerSemantics: true}
	station := connectedFakeStation(power, nil, nil, Capabilities{PowerRead: true, PowerWrite: true, Standby: false})

	result, err := SetPowerState(station, PowerStateStandby)
	if !IsUnsupportedCapabilityError(err) {
		t.Fatalf("SetPowerState(standby) error = %v, want an unsupported capability error", err)
	}
	if result.Confirmed {
		t.Fatalf("SetPowerState(standby) result = %+v, want no confirmation", result)
	}
	if len(power.writes) != 0 {
		t.Fatalf("writes = %d, want none after the standby downgrade", len(power.writes))
	}
}
