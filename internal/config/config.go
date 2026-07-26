package config

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Config struct {
	RenamedStations          map[string]string `json:"renamedStations"`
	RenamedStationsByAddress map[string]string `json:"renamedStationsByAddress"`
	persistenceBlockedErr    error
	mutex                    sync.RWMutex
}

type persistedConfig struct {
	RenamedStations          map[string]string `json:"renamedStations"`
	RenamedStationsByAddress map[string]string `json:"renamedStationsByAddress,omitempty"`
}

var configFileWriter = writeFileAtomically

// NewConfig creates a new Config with defaults
func NewConfig() *Config {
	return &Config{
		RenamedStations:          make(map[string]string),
		RenamedStationsByAddress: make(map[string]string),
	}
}

// Helper function to get the full path to the config file
func getConfigPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user config dir: %w", err)
	}
	appConfigDir := filepath.Join(configDir, "lhcontrol")
	err = os.MkdirAll(appConfigDir, 0755) // Ensure the directory exists
	if err != nil {
		return "", fmt.Errorf("failed to create app config dir '%s': %w", appConfigDir, err)
	}
	return filepath.Join(appConfigDir, "config.json"), nil
}

// Load reads the configuration from disk
func (c *Config) Load() error {
	configFilePath, err := getConfigPath()
	if err != nil {
		return err
	}

	log.Printf("Loading config from: %s", configFilePath)
	configFile, err := os.ReadFile(configFilePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // No config file yet, which is fine
		}
		return fmt.Errorf("error reading config file '%s': %w", configFilePath, err)
	}

	var loaded persistedConfig
	err = json.Unmarshal(configFile, &loaded)
	if err != nil {
		invalidPath := fmt.Sprintf("%s.invalid-%s", configFilePath, time.Now().Format("20060102T150405.000000000"))
		if renameErr := os.Rename(configFilePath, invalidPath); renameErr != nil {
			c.mutex.Lock()
			c.persistenceBlockedErr = fmt.Errorf("invalid config could not be preserved: %w", renameErr)
			c.mutex.Unlock()
			return fmt.Errorf("error unmarshalling config (failed to preserve invalid file: %v): %w", renameErr, err)
		}
		return fmt.Errorf("error unmarshalling config; invalid file preserved as '%s': %w", invalidPath, err)
	}
	if loaded.RenamedStations == nil {
		loaded.RenamedStations = make(map[string]string)
	}
	if loaded.RenamedStationsByAddress == nil {
		loaded.RenamedStationsByAddress = make(map[string]string)
	}
	c.mutex.Lock()
	c.RenamedStations = loaded.RenamedStations
	c.RenamedStationsByAddress = loaded.RenamedStationsByAddress
	c.persistenceBlockedErr = nil
	c.mutex.Unlock()
	return nil
}

// Save writes the configuration to disk
func (c *Config) Save() error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.saveLocked()
}

// saveLocked persists exactly the state protected by mutex. Keeping mutation
// and persistence under one exclusive lock prevents an older Save from
// overwriting a newer rename.
func (c *Config) saveLocked() error {
	if c.persistenceBlockedErr != nil {
		return fmt.Errorf("config save blocked to preserve the unreadable file: %w", c.persistenceBlockedErr)
	}
	configFilePath, err := getConfigPath()
	if err != nil {
		return err
	}

	snapshot := persistedConfig{
		RenamedStations:          make(map[string]string, len(c.RenamedStations)),
		RenamedStationsByAddress: make(map[string]string, len(c.RenamedStationsByAddress)),
	}
	for originalName, renamedName := range c.RenamedStations {
		snapshot.RenamedStations[originalName] = renamedName
	}
	for address, renamedName := range c.RenamedStationsByAddress {
		snapshot.RenamedStationsByAddress[address] = renamedName
	}

	configFile, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return fmt.Errorf("error marshalling config: %w", err)
	}

	log.Printf("Saving config to: %s", configFilePath)
	err = configFileWriter(configFilePath, configFile, 0644)
	if err != nil {
		return fmt.Errorf("failed to write config file '%s': %w", configFilePath, err)
	}
	return nil
}

func writeFileAtomically(path string, data []byte, permissions os.FileMode) (returnErr error) {
	tempFile, err := os.CreateTemp(filepath.Dir(path), ".lhcontrol-config-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary config: %w", err)
	}
	tempPath := tempFile.Name()
	closed := false
	defer func() {
		if !closed {
			if closeErr := tempFile.Close(); returnErr == nil && closeErr != nil {
				returnErr = fmt.Errorf("close temporary config: %w", closeErr)
			}
		}
		_ = os.Remove(tempPath)
	}()

	if err := tempFile.Chmod(permissions); err != nil {
		return fmt.Errorf("set temporary config permissions: %w", err)
	}
	if _, err := tempFile.Write(data); err != nil {
		return fmt.Errorf("write temporary config: %w", err)
	}
	if err := tempFile.Sync(); err != nil {
		return fmt.Errorf("flush temporary config: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("close temporary config before replacement: %w", err)
	}
	closed = true
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}
	return nil
}

// GetRenamedStation returns the local display name for a station.
func (c *Config) GetRenamedStation(originalName string) (string, bool) {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	renamedName, ok := c.RenamedStations[originalName]
	return renamedName, ok
}

// SetRenamedStation updates a local display name and persists the config.
func (c *Config) SetRenamedStation(originalName string, newName string) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	previous, existed := c.RenamedStations[originalName]
	if newName == "" {
		delete(c.RenamedStations, originalName)
	} else {
		c.RenamedStations[originalName] = newName
	}
	if err := c.saveLocked(); err != nil {
		if existed {
			c.RenamedStations[originalName] = previous
		} else {
			delete(c.RenamedStations, originalName)
		}
		return err
	}
	return nil
}

// SetRenamedStationForAddresses keeps the legacy name-based API effective for
// stations that already have a more specific address alias.
func (c *Config) SetRenamedStationForAddresses(originalName, newName string, addresses []string) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	previousLegacy, legacyExisted := c.RenamedStations[originalName]
	previousAddresses := make(map[string]string, len(addresses))
	existingAddresses := make(map[string]bool, len(addresses))
	for _, address := range addresses {
		previousAddresses[address], existingAddresses[address] = c.RenamedStationsByAddress[address]
		if newName == "" {
			delete(c.RenamedStationsByAddress, address)
		} else {
			c.RenamedStationsByAddress[address] = newName
		}
	}
	if newName == "" {
		delete(c.RenamedStations, originalName)
	} else {
		c.RenamedStations[originalName] = newName
	}

	if err := c.saveLocked(); err != nil {
		if legacyExisted {
			c.RenamedStations[originalName] = previousLegacy
		} else {
			delete(c.RenamedStations, originalName)
		}
		for _, address := range addresses {
			if existingAddresses[address] {
				c.RenamedStationsByAddress[address] = previousAddresses[address]
			} else {
				delete(c.RenamedStationsByAddress, address)
			}
		}
		return err
	}
	return nil
}

// GetStationDisplayName uses the stable BLE address first and falls back to
// legacy name-keyed entries so existing configurations continue to work.
func (c *Config) GetStationDisplayName(address, originalName string) (string, bool) {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	if renamedName, ok := c.RenamedStationsByAddress[address]; ok {
		if renamedName == "" {
			return originalName, false
		}
		return renamedName, true
	}
	renamedName, ok := c.RenamedStations[originalName]
	return renamedName, ok
}

func (c *Config) SetRenamedStationByAddress(address, originalName, newName string) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if c.RenamedStationsByAddress == nil {
		c.RenamedStationsByAddress = make(map[string]string)
	}
	previousAddressName, addressExisted := c.RenamedStationsByAddress[address]
	previousLegacyName, legacyExisted := c.RenamedStations[originalName]
	if newName == "" {
		if legacyExisted {
			// An empty address entry is a tombstone for this device only. It
			// prevents the shared legacy name from being applied again without
			// removing that alias from other devices with the same factory name.
			c.RenamedStationsByAddress[address] = ""
		} else {
			delete(c.RenamedStationsByAddress, address)
		}
	} else {
		c.RenamedStationsByAddress[address] = newName
	}
	if err := c.saveLocked(); err != nil {
		if addressExisted {
			c.RenamedStationsByAddress[address] = previousAddressName
		} else {
			delete(c.RenamedStationsByAddress, address)
		}
		if legacyExisted {
			c.RenamedStations[originalName] = previousLegacyName
		} else {
			delete(c.RenamedStations, originalName)
		}
		return err
	}
	return nil
}
