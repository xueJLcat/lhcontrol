package main

func (a *App) GetDiscoveryAttempts() int {

	return a.config.GetDiscoveryAttempts()

}

func (a *App) SetDiscoveryAttempts(attempts int) error {

	err := a.config.SetDiscoveryAttempts(attempts)

	a.setConfigPersistenceStatus()

	if err == nil {

		a.applyBluetoothTiming()

	}

	return err

}

func (a *App) GetDiscoveryRetryDelayMs() int {

	return a.config.GetDiscoveryRetryDelayMs()

}

func (a *App) SetDiscoveryRetryDelayMs(delayMs int) error {

	err := a.config.SetDiscoveryRetryDelayMs(delayMs)

	a.setConfigPersistenceStatus()

	if err == nil {

		a.applyBluetoothTiming()

	}

	return err

}

// GetAPIListenAddress returns the configured HTTP API listen address.
