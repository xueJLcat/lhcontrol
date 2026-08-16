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
// listener switch. The loop binds immediately on startup, so a healthy
// address verifies at once; the remaining window covers one retry cycle of
// the listener loop so a transient bind failure can still recover.
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

	// Re-saving the unchanged address must not restart the listener when the
	// listener already serves it: the restart tears down the live socket and
	// drops in-flight responses for a bind target that is already the
	// configured one. The config setter still runs so an invalid residual
	// stored value (which the getter masks with the default) is repaired by
	// persisting its sanitized equivalent. A listener that diverged from the
	// configured address (for example retrying elsewhere after a failed
	// switch) converges again on the explicit re-save instead of requiring a
	// different address and back.
	if a.config.GetAPIListenAddress() == normalized {
		err := a.config.SetAPIListenAddress(normalized)
		a.setConfigPersistenceStatus()
		if err != nil {
			return err
		}
		published := a.GetAPIStatus().Address
		if published != normalized && !a.shuttingDown.Load() {
			a.setAPIAddress(normalized)
			a.restartAPIServer()
			bound, bindErr := a.waitForAPIBind()
			if !bound {
				if bindErr == nil {
					bindErr = fmt.Errorf("%s did not come up within %s", normalized, a.apiBindVerifyWindow())
				}
				// Restore the previously serving listener instead of leaving
				// the API down on an address that cannot bind; the persisted
				// address stays the configured one so the listener target and
				// the configuration converge again on the next change.
				a.rollbackListener(published, normalized)
				return fmt.Errorf("cannot listen on %s: %v", normalized, bindErr)
			}
		} else if published == normalized && !a.shuttingDown.Load() && !a.GetAPIStatus().Running {
			// The listener already targets the configured address but is not
			// serving it (a failed bind stuck in its retry loop). Re-saving
			// must converge it the same way the changed-address branch does
			// instead of reporting success while the HTTP API stays down.
			a.restartAPIServer()
			bound, bindErr := a.waitForAPIBind()
			if !bound {
				if bindErr == nil {
					bindErr = fmt.Errorf("%s did not come up within %s", normalized, a.apiBindVerifyWindow())
				}
				return fmt.Errorf("cannot listen on %s: %v", normalized, bindErr)
			}
		}
		return nil
	}

	previous := a.config.GetAPIListenAddress()
	// The address the listener currently serves can differ from the persisted
	// one while an earlier change is still settling; a failed switch restores
	// each layer to its own previous value.
	published := a.GetAPIStatus().Address
	if err := a.config.SetAPIListenAddress(normalized); err != nil {
		a.setConfigPersistenceStatus()
		return err
	}
	a.setConfigPersistenceStatus()

	if published != normalized {
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
			// The probe only proved the address could bind while the old
			// listener was up. Wait for the restarted listener to actually
			// bind before reporting success: the port could be grabbed between
			// the probe and the rebind, which would otherwise leave the API
			// down while the settings call claimed success. On failure restore
			// the previously serving listener and the persisted address.
			// During shutdown the restart is intentionally skipped, so there is
			// no server to bind and waiting would only time out spuriously.
			if !a.shuttingDown.Load() {
				bound, bindErr := a.waitForAPIBind()
				if !bound {
					if bindErr == nil {
						bindErr = fmt.Errorf("%s did not come up within %s", normalized, a.apiBindVerifyWindow())
					}
					a.rollbackListener(published, previous)
					if rollbackErr := a.config.SetAPIListenAddress(previous); rollbackErr != nil {
						a.setConfigPersistenceStatus()
						return fmt.Errorf(
							"cannot listen on %s: %v; additionally, rolling back to %s failed: %v",
							normalized, bindErr, previous, rollbackErr,
						)
					}
					a.setConfigPersistenceStatus()
					return fmt.Errorf("cannot listen on %s: %v", normalized, bindErr)
				}
			}
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
	a.rollbackListener(published, published)
	if bindErr != nil {
		return bindErr
	}
	return fmt.Errorf("%s did not come up within %s", normalized, a.apiBindVerifyWindow())
}

// rollbackListener restores the listener onto restoreAddress and verifies the
// bind. If that restoration cannot bind either (the port was grabbed in the
// meantime), converge the listener onto convergeAddress so the running server
// and the rolled-back persisted configuration agree; config state itself is
// managed by the caller.
func (a *App) rollbackListener(restoreAddress, convergeAddress string) {
	if a.shuttingDown.Load() {
		return
	}
	a.setAPIAddress(restoreAddress)
	a.restartAPIServer()
	if bound, _ := a.waitForAPIBind(); bound {
		return
	}
	if restoreAddress == convergeAddress {
		return
	}
	a.setAPIAddress(convergeAddress)
	a.restartAPIServer()
}

// waitForAPIBind polls the freshly restarted listener until it reports a
// successful bind or the verification window expires, reporting the first
// recorded bind error when the window runs out. A recorded error does not end
// the wait early: runAPIServer retries a failed bind after apiRetryDelay, and
// the window is sized to cover one retry so transient port contention is not
// misread as a permanent failure. restartAPIServer waits for the old loop to
// finish (which clears the status), so every observed outcome belongs to the
// new loop.
func (a *App) waitForAPIBind() (bool, error) {
	deadline := time.Now().Add(a.apiBindVerifyWindow())
	var firstErr error
	for {
		status := a.GetAPIStatus()
		if status.Running {
			return true, nil
		}
		if status.Error != "" && firstErr == nil {
			firstErr = errors.New(status.Error)
		}
		if time.Now().After(deadline) {
			return false, firstErr
		}
		time.Sleep(10 * time.Millisecond)
	}
}
