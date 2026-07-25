package bluetooth

import "testing"

func TestDecodePowerState(t *testing.T) {
	tests := []struct {
		raw  byte
		want PowerState
	}{
		{0x00, PowerStateSleep},
		{0x02, PowerStateStandby},
		{0x01, PowerStateBooting},
		{0x08, PowerStateBooting},
		{0x09, PowerStateOn},
		{0x0B, PowerStateOn},
		{0x03, PowerStateUnknown},
		{0xFF, PowerStateUnknown},
	}
	for _, test := range tests {
		if got := DecodePowerState(test.raw); got != test.want {
			t.Errorf("DecodePowerState(0x%02X) = %v, want %v", test.raw, got, test.want)
		}
	}
}

func TestPowerStateCompatibilityValues(t *testing.T) {
	if PowerStateUnknown != -1 || PowerStateSleep != 0 || PowerStateOn != 1 {
		t.Fatalf("legacy state values changed: unknown=%d sleep=%d on=%d", PowerStateUnknown, PowerStateSleep, PowerStateOn)
	}
	if PowerStateStandby != 2 || PowerStateBooting != 3 {
		t.Fatalf("extended state values changed: standby=%d booting=%d", PowerStateStandby, PowerStateBooting)
	}
}

func TestDecodeChannel(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		want    int
		wantErr bool
	}{
		{"one byte lower bound", []byte{0x01}, 1, false},
		{"one byte upper bound", []byte{0x10}, 16, false},
		{"two bytes", []byte{0x00, 0x05}, 5, false},
		{"three bytes", []byte{0x00, 0x00, 0x0F}, 15, false},
		{"four bytes", []byte{0x00, 0x00, 0x00, 0x08}, 8, false},
		{"empty", nil, ChannelUnknown, true},
		{"zero", []byte{0x00}, ChannelUnknown, true},
		{"too high", []byte{0x11}, ChannelUnknown, true},
		{"too long", []byte{0, 0, 0, 0, 1}, ChannelUnknown, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := DecodeChannel(test.data)
			if (err != nil) != test.wantErr {
				t.Fatalf("DecodeChannel(%v) error = %v, wantErr %v", test.data, err, test.wantErr)
			}
			if got != test.want {
				t.Errorf("DecodeChannel(%v) = %d, want %d", test.data, got, test.want)
			}
		})
	}
}

func TestParsePowerTarget(t *testing.T) {
	for value, want := range map[string]PowerState{
		"on": PowerStateOn, "standby": PowerStateStandby, "sleep": PowerStateSleep, "off": PowerStateSleep,
	} {
		got, err := ParsePowerTarget(value)
		if err != nil || got != want {
			t.Errorf("ParsePowerTarget(%q) = %v, %v; want %v", value, got, err, want)
		}
	}
	if _, err := ParsePowerTarget("booting"); err == nil {
		t.Error("booting must not be accepted as a stable target")
	}
}
