package main

func (a *App) GetAPIListenAddress() string {

	return a.config.GetAPIListenAddress()

}

// SetAPIListenAddress validates and persists the HTTP API listen address and
// hot-restarts the listener loop so the change applies without app restart.

func (a *App) SetAPIListenAddress(address string) error {
	a.apiSettingsMutex.Lock()
	defer a.apiSettingsMutex.Unlock()

	// Re-saving the unchanged address must not restart the listener: the
	// restart tears down the live socket and drops in-flight responses for a
	// bind target that is already the configured one.
	if a.config.GetAPIListenAddress() == address {
		return nil
	}

	err := a.config.SetAPIListenAddress(address)

	a.setConfigPersistenceStatus()

	if err != nil {

		return err

	}

	// The listener loop binds whatever address the status advertises, so
	// publish the new address before restarting the loop.

	a.setAPIAddress(address)

	a.restartAPIServer()

	return nil

}
