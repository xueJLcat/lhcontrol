package bluetooth

import (
	tinygobluetooth "tinygo.org/x/bluetooth"
)

// AdapterInfo is the frontend-facing description of a local Bluetooth radio.
type AdapterInfo struct {
	DeviceID string `json:"deviceId"`
	Name     string `json:"name"`
}

// ListAdapters enumerates the Bluetooth radios known to the operating system.
func ListAdapters() ([]AdapterInfo, error) {
	systemAdapters, err := tinygobluetooth.ListAdapters()
	if err != nil {
		return nil, err
	}
	adapters := make([]AdapterInfo, 0, len(systemAdapters))
	for _, systemAdapter := range systemAdapters {
		adapters = append(adapters, AdapterInfo{
			DeviceID: systemAdapter.DeviceID,
			Name:     systemAdapter.Name,
		})
	}
	return adapters, nil
}
