package bluetooth

import (
	"fmt"
	"strings"
)

// PowerState values preserve the legacy Unknown/Sleep/On numeric values.
type PowerState int

const (
	PowerStateUnknown PowerState = -1
	PowerStateSleep   PowerState = 0
	PowerStateOn      PowerState = 1
	PowerStateStandby PowerState = 2
	PowerStateBooting PowerState = 3

	// PowerStateOff is kept as an alias for older callers.
	PowerStateOff = PowerStateSleep

	RawPowerStateUnknown = -1
	ChannelUnknown       = 0
)

func (state PowerState) String() string {
	switch state {
	case PowerStateSleep:
		return "sleep"
	case PowerStateOn:
		return "on"
	case PowerStateStandby:
		return "standby"
	case PowerStateBooting:
		return "booting"
	default:
		return "unknown"
	}
}

func ParsePowerTarget(value string) (PowerState, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "on":
		return PowerStateOn, nil
	case "standby":
		return PowerStateStandby, nil
	case "sleep", "off":
		return PowerStateSleep, nil
	default:
		return PowerStateUnknown, fmt.Errorf("invalid power state %q; expected on, standby, or sleep", value)
	}
}

func DecodePowerState(raw byte) PowerState {
	switch raw {
	case 0x00:
		return PowerStateSleep
	case 0x02:
		return PowerStateStandby
	case 0x01, 0x08:
		return PowerStateBooting
	case 0x09, 0x0B:
		return PowerStateOn
	default:
		return PowerStateUnknown
	}
}

func DecodeChannel(data []byte) (int, error) {
	if len(data) < 1 || len(data) > 4 {
		return ChannelUnknown, fmt.Errorf("channel value must contain 1 to 4 bytes, got %d", len(data))
	}
	value := uint32(0)
	for _, part := range data {
		value = value<<8 | uint32(part)
	}
	if value < 1 || value > 16 {
		return ChannelUnknown, fmt.Errorf("channel value %d is outside the supported range 1-16", value)
	}
	return int(value), nil
}

type Capabilities struct {
	PowerRead         bool `json:"powerRead"`
	PowerWrite        bool `json:"powerWrite"`
	PowerNotify       bool `json:"powerNotify"`
	Standby           bool `json:"standby"`
	ChannelRead       bool `json:"channelRead"`
	ChannelWrite      bool `json:"channelWrite"`
	ChannelNotify     bool `json:"channelNotify"`
	Identify          bool `json:"identify"`
	DeviceInformation bool `json:"deviceInformation"`
}

type DeviceMetadata struct {
	Manufacturer     string `json:"manufacturer"`
	Model            string `json:"model"`
	SerialNumber     string `json:"serialNumber"`
	HardwareRevision string `json:"hardwareRevision"`
	FirmwareRevision string `json:"firmwareRevision"`
}
