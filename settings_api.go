package main

func (a *App) GetAPIListenAddress() string {

	return a.config.GetAPIListenAddress()

}

// SetAPIListenAddress validates and persists the HTTP API listen address and
// hot-restarts the listener loop so the change applies without app restart.

func (a *App) SetAPIListenAddress(address string) error {

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
