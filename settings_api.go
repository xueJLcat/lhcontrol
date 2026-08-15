package main

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

func (a *App) GetAPIListenAddress() string {

	return a.config.GetAPIListenAddress()

}

// defaultAPIBindVerifyWait bounds the bind verification after a same-port
// listener switch. The loop binds immediately on startup, so this only waits
// out scheduling; a permanently failing bind is detected through the recorded
// error long before the deadline.
const defaultAPIBindVerifyWait = 3 * time.Second

func (a *App) apiBindVerifyWindow() time.Duration {
	if a.apiBindVerifyWait > 0 {
		return a.apiBindVerifyWait
	}
	return defaultAPIBindVerifyWait
}

func listenerPort(address string) string {
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		return ""
	}
	return port
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
		// Verify that the new address can bind before the change is treated
		// as applied: persisting an address that can never bind would leave the
		// HTTP API down with no reachable endpoint left to correct it. On
		// failure roll back both the persisted address and the listener target,
		// and report a rollback failure explicitly instead of silently letting
		// the configuration diverge from the running server.
		if verifyErr := a.verifyAndSwitchListenAddress(normalized); verifyErr != nil {
			if rollbackErr := a.config.SetAPIListenAddress(previous); rollbackErr != nil {
				a.setConfigPersistenceStatus()
				return fmt.Errorf(
					"cannot listen on %s: %v; additionally, rolling back to %s failed: %v",
					normalized, verifyErr, previous, rollbackErr,
				)
			}
			a.setConfigPersistenceStatus()
			return fmt.Errorf("cannot listen on %s: %v", normalized, verifyErr)
		}
		// The same-port branch already published and restarted onto the new
		// address; finish the switch for the probe branch.
		if a.GetAPIStatus().Address != normalized {
			a.setAPIAddress(normalized)
			a.restartAPIServer()
		}
	}

	return nil

}

// verifyAndSwitchListenAddress proves that the new address can actually bind.
// A different-port target is verified with a direct bind probe that leaves the
// running listener untouched. A target sharing the listener's current port
// cannot be probed without colliding with this app's own socket (the probe
// would blame the app's healthy listener for the occupied port), so the
// listener is restarted onto the new address and the bind outcome is
// observed; a failing bind restores the address the server was actually
// published on before the switch, not the previously configured value (the two
// can differ while an earlier change is still settling).
func (a *App) verifyAndSwitchListenAddress(normalized string) error {
	published := a.GetAPIStatus().Address
	if listenerPort(published) != listenerPort(normalized) {
		probe, probeErr := a.listen("tcp", normalized)
		if probeErr != nil {
			return probeErr
		}
		return probe.Close()
	}
	a.setAPIAddress(normalized)
	a.restartAPIServer()
	bound, bindErr := a.waitForAPIBind()
	if bound {
		return nil
	}
	a.setAPIAddress(published)
	a.restartAPIServer()
	if bindErr != nil {
		return bindErr
	}
	return fmt.Errorf("%s did not come up within %s", normalized, a.apiBindVerifyWindow())
}

// waitForAPIBind polls the freshly restarted listener until it reports a
// successful bind, records a bind error, or the verification window expires.
// restartAPIServer waits for the old loop to finish (which clears the status),
// so the first observed non-empty outcome belongs to the new loop.
func (a *App) waitForAPIBind() (bool, error) {
	deadline := time.Now().Add(a.apiBindVerifyWindow())
	for {
		status := a.GetAPIStatus()
		if status.Running {
			return true, nil
		}
		if status.Error != "" {
			return false, errors.New(status.Error)
		}
		if time.Now().After(deadline) {
			return false, nil
		}
		time.Sleep(10 * time.Millisecond)
	}
}
