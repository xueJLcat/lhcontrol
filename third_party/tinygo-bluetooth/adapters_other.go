//go:build !windows

package bluetooth

import "errors"

// AdapterInfo identifies a local Bluetooth radio. Only Windows exposes radio
// enumeration through the stack this fork targets.
type AdapterInfo struct {
	DeviceID string
	Name     string
}

// ListAdapters reports that radio enumeration is unavailable on this platform.
func ListAdapters() ([]AdapterInfo, error) {
	return nil, errors.New("bluetooth: adapter enumeration is not supported on this platform")
}
