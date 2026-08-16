package config

import (
	"encoding/json"
	"fmt"
	"lhcontrol/internal/autosleep"
	"log"
	"os"
	"path/filepath"
)

func (c *Config) Save() error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.saveLocked()
}

// PersistenceError reports whether saves are intentionally blocked because an
// unreadable config file could not be preserved. It is safe to call while the
// application is exposing status to another goroutine.
func (c *Config) PersistenceError() error {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	if c.persistenceBlockedErr != nil {
		return c.persistenceBlockedErr
	}
	return c.lastPersistenceErr
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
		c.lastPersistenceErr = err
		return err
	}

	// Sanitize and repair the runtime fields in place while persisting so a
	// directly-assigned or zero-value field can never write an out-of-range
	// value that Load would later silently replace, then build the snapshot
	// from the normalized runtime values (fields.go is the single declaration
	// of the ranged settings).
	c.sanitizeRuntimeInPlace()
	c.repairCrossItemInvariants()

	snapshot := persistedConfig{
		RenamedStations:          make(map[string]string, len(c.RenamedStations)),
		RenamedStationsByAddress: make(map[string]string, len(c.RenamedStationsByAddress)),
		Language:                 sanitizeLanguage(c.Language),
	}
	scanOnStartup := c.ScanOnStartup
	snapshot.ScanOnStartup = &scanOnStartup
	statusPollingEnabled := c.StatusPollingEnabled
	snapshot.StatusPollingEnabled = &statusPollingEnabled
	snapshot.APIListenAddress = sanitizeAPIListenAddress(c.APIListenAddress)
	c.populatePersistedInts(&snapshot)
	autoSleep := sanitizeAutoSleep(&c.AutoSleep)
	if c.AutoSleep != (autosleep.Settings{}) {
		snapshot.AutoSleep = &autoSleep
	}
	for originalName, renamedName := range c.RenamedStations {
		snapshot.RenamedStations[originalName] = renamedName
	}
	for address, renamedName := range c.RenamedStationsByAddress {
		snapshot.RenamedStationsByAddress[address] = renamedName
	}

	configFile, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		saveErr := fmt.Errorf("error marshalling config: %w", err)
		c.lastPersistenceErr = saveErr
		return saveErr
	}

	log.Printf("Saving config to: %s", configFilePath)
	err = configFileWriter(configFilePath, configFile, 0644)
	if err != nil {
		saveErr := fmt.Errorf("failed to write config file '%s': %w", configFilePath, err)
		c.lastPersistenceErr = saveErr
		return saveErr
	}
	// Save is also a normalization boundary for callers that populated the
	// exported fields directly. The ranged runtime fields were already
	// sanitized and repaired in place above; mirror only the remaining
	// normalized non-ranged fields so runtime getters and the just-written
	// file agree.
	c.AutoSleep = autoSleep
	c.Language = snapshot.Language
	c.APIListenAddress = snapshot.APIListenAddress
	c.lastPersistenceErr = nil
	return nil
}

// configTempFilePattern names the atomic-write scratch files. Load sweeps
// matches so a crash between CreateTemp and Rename cannot accumulate stale
// temporaries forever; the pattern is shared so the two sites cannot drift.
const configTempFilePattern = ".lhcontrol-config-*.tmp"

func writeFileAtomically(path string, data []byte, permissions os.FileMode) (returnErr error) {
	tempFile, err := os.CreateTemp(filepath.Dir(path), configTempFilePattern)
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

// removeStaleConfigTemporaries deletes scratch files left behind by a crash
// or power loss between CreateTemp and Rename. Removal failures are ignored:
// a locked or already-gone file is harmless, and quarantined
// config.json.invalid-* files are deliberately left untouched.
func removeStaleConfigTemporaries(dir string) {
	matches, err := filepath.Glob(filepath.Join(dir, configTempFilePattern))
	if err != nil {
		return
	}
	for _, match := range matches {
		_ = os.Remove(match)
	}
}

// GetRenamedStation returns the local display name for a station.
