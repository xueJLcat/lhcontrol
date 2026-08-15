package main

import (
	"fmt"
	"strings"
)

func (a *App) GetAPIListenAddress() string {

	return a.config.GetAPIListenAddress()

}

// SetAPIListenAddress validates and persists the HTTP API listen address and
// hot-restarts the listener loop so the change applies without app restart.

func (a *App) SetAPIListenAddress(address string) error {
	a.apiSettingsMutex.Lock()
	defer a.apiSettingsMutex.Unlock()

	normalized := strings.TrimSpace(address)

	// Re-saving the unchanged address must not restart the listener: the
	// restart tears down the live socket and drops in-flight responses for a
	// bind target that is already the configured one. The config setter still
	// runs so an invalid residual stored value (which the getter masks with
	// the default) is repaired by persisting its sanitized equivalent.
	if a.config.GetAPIListenAddress() == normalized {
		err := a.config.SetAPIListenAddress(normalized)
		a.setConfigPersistenceStatus()
		return err
	}

	previous := a.config.GetAPIListenAddress()
	if err := a.config.SetAPIListenAddress(normalized); err != nil {
		a.setConfigPersistenceStatus()
		return err
	}
	a.setConfigPersistenceStatus()

	if a.GetAPIStatus().Address != normalized {
		// Bind-probe the new address before tearing down the working listener
		// and restarting on it: persisting an address that can never bind
		// would leave the HTTP API down with no reachable endpoint left to
		// correct it. The probe is skipped when it would collide with this
		// app's own live socket on the same address.
		probe, probeErr := a.listen("tcp", normalized)
		if probeErr != nil {
			_ = a.config.SetAPIListenAddress(previous)
			a.setConfigPersistenceStatus()
			return fmt.Errorf("cannot listen on %s: %w", normalized, probeErr)
		}
		_ = probe.Close()
	}

	// The listener loop binds whatever address the status advertises, so
	// publish the new address before restarting the loop.

	a.setAPIAddress(normalized)

	a.restartAPIServer()

	return nil

}
