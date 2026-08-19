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
	c.recoverBlockedPersistenceLocked()
	return c.saveLocked()
}

// recoverBlockedPersistenceLocked retries the configuration load while saves
// are blocked by an unreadable file. The block exists because a startup load
// failure left only defaults in memory, and saving them would destroy the
// unreadable file's contents. When the read failure was transient (an AV
// scanner or sync tool holding the file, a momentary IO error) the next
// setter should recover within the same session instead of reporting every
// setting change as blocked until a restart. Setters invoke this after
// taking the write lock and before mutating their field so the recovered
// disk contents become authoritative and the pending mutation applies on top
// of them; a recovery that still fails leaves the block in place. Callers
// must hold c.mutex.
func (c *Config) recoverBlockedPersistenceLocked() {
	if c.persistenceBlockedErr == nil {
		return
	}
	configFilePath, err := getConfigPath()
	if err != nil {
		return
	}
	previousBlocked := c.persistenceBlockedErr
	loadErr := c.loadLocked(configFilePath)
	if loadErr != nil && c.persistenceBlockedErr != nil {
		// Recovery still failed. Keep the original block reason instead of a
		// fresh error instance from this attempt so repeated retries report a
		// stable failure and callers holding the original error keep matching
		// it. A successful recovery clears both fields inside loadLocked.
		c.persistenceBlockedErr = previousBlocked
		c.lastPersistenceErr = previousBlocked
	} else if loadErr != nil {
		// Recovery unblocked persistence (the unreadable file was quarantined
		// and defaults were applied), but the persisted contents were lost.
		// The upcoming save succeeds, so no persistence error would surface
		// it; record a session warning instead so the quarantine is not
		// silent.
		c.recoveryLoadWarning = fmt.Sprintf("Configuration was reset to defaults during recovery: %v", loadErr)
		log.Printf("Blocked-save recovery quarantined the config: %v", loadErr)
	}
	if c.persistenceBlockedErr == nil {
		// The persisted contents are authoritative again. Notify runtime
		// observers that the in-memory configuration no longer matches what
		// startup applied, so they can re-apply their derived side effects
		// (Bluetooth timing, auto-sleep watcher, API listener).
		c.recoveryGeneration++
	}
}

// RecoveryGeneration reports how many times a blocked-save recovery has
// replaced the in-memory configuration since process start. Runtime layers
// that derive state from the configuration at startup compare observed values
// to detect a recovery and re-apply their side effects; without that replay a
// recovered configuration would stay inert until the next restart. It is safe
// to call while another goroutine mutates the configuration.
func (c *Config) RecoveryGeneration() uint64 {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return c.recoveryGeneration
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

// RecoveryLoadWarning reports a blocked-save recovery that had to quarantine
// the persisted configuration and fall back to defaults mid-session. It
// remains set for the session: saves succeed again, but the original contents
// were lost and only the timestamped quarantine copy preserves them.
func (c *Config) RecoveryLoadWarning() string {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return c.recoveryLoadWarning
}

// blockedSaveError reports that persistence is intentionally withheld because
// the configuration file could not be read and saving the sparse in-memory
// state would destroy it. Every setter that skips a write must surface this
// instead of claiming success.
func blockedSaveError(cause error) error {
	return fmt.Errorf("config save blocked to preserve the unreadable file: %w", cause)
}

// saveLocked persists exactly the state protected by mutex. Keeping mutation
// and persistence under one exclusive lock prevents an older Save from
// overwriting a newer rename.
func (c *Config) saveLocked() error {
	if c.persistenceBlockedErr != nil {
		return blockedSaveError(c.persistenceBlockedErr)
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
	// of the ranged settings). Snapshot the pre-normalization values first:
	// when the write below fails, every setter only rolls back its own field,
	// so the in-place normalization of unrelated fields must be undone too or
	// memory silently diverges from the file that was never written.
	bindings := c.intSettingBindings(nil)
	originalValues := make([]int, len(bindings))
	for index, binding := range bindings {
		originalValues[index] = *binding.runtime
	}
	restoreNormalizedRuntime := func() {
		for index, binding := range bindings {
			*binding.runtime = originalValues[index]
		}
	}
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
		restoreNormalizedRuntime()
		saveErr := fmt.Errorf("error marshalling config: %w", err)
		c.lastPersistenceErr = saveErr
		return saveErr
	}

	log.Printf("Saving config to: %s", configFilePath)
	err = configFileWriter(configFilePath, configFile, 0644)
	if err != nil {
		restoreNormalizedRuntime()
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
