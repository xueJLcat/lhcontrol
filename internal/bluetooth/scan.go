package bluetooth

import (
	"context"
	"errors"
	"fmt"
	"log"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"tinygo.org/x/bluetooth"
)

var ErrScanCancelled = errors.New("Bluetooth scan cancelled")

type scanStopReason uint32

const (
	scanStopNone scanStopReason = iota
	scanStopDuration
	scanStopCancelled
)

type scanSession struct {
	mutex       sync.Mutex
	doneOnce    sync.Once
	reason      atomic.Uint32
	started     bool
	finished    bool
	stopStarted bool
	stopDone    chan struct{}
	stopErr     error
	// durationStopIssued records that the duration timer requested the stop
	// before any cancellation was recorded. Cancellation overwrites reason
	// afterwards, so this latch is what lets a scan that already ran its
	// full duration keep its results when StopScan lands during the
	// stop-handshake window.
	durationStopIssued bool
}

func newScanSession() *scanSession {
	return &scanSession{stopDone: make(chan struct{})}
}

func (s *scanSession) requestStop(reason scanStopReason) error {
	s.requestStopAsync(reason)
	s.mutex.Lock()
	// A platform watcher has not started yet, so there is no StopScan call to
	// await. markStarted will issue the recorded cancellation when it arrives.
	pendingStart := !s.started && !s.finished
	s.mutex.Unlock()
	if pendingStart {
		return nil
	}
	<-s.stopDone
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.stopErr
}

func (s *scanSession) requestStopAsync(reason scanStopReason) {
	s.mutex.Lock()
	currentReason := scanStopReason(s.reason.Load())
	if currentReason == scanStopNone || reason == scanStopCancelled {
		// The latch is set in the same critical section that stores the
		// duration reason, under the mutex that cancellation also takes, so
		// a racing cancel either lands first (no latch) or after (latch
		// survives the reason overwrite).
		if reason == scanStopDuration && currentReason == scanStopNone {
			s.durationStopIssued = true
		}
		s.reason.Store(uint32(reason))
	}
	shouldStop := s.started && !s.finished
	s.mutex.Unlock()
	if shouldStop {
		s.issueStop()
	}
}

func (s *scanSession) issueStop() {
	s.mutex.Lock()
	if s.stopStarted || s.finished || !s.started {
		s.mutex.Unlock()
		return
	}
	s.stopStarted = true
	s.mutex.Unlock()
	go func() {
		err := stopScanSafely()
		s.mutex.Lock()
		s.stopErr = err
		s.mutex.Unlock()
		s.doneOnce.Do(func() { close(s.stopDone) })
	}()
}

func (s *scanSession) markStarted() {
	s.mutex.Lock()
	s.started = true
	pendingStop := s.reason.Load() != uint32(scanStopNone) && !s.finished
	s.mutex.Unlock()
	if pendingStop {
		s.issueStop()
	}
}

func (s *scanSession) markFinished() {
	s.mutex.Lock()
	s.finished = true
	stopStarted := s.stopStarted
	s.mutex.Unlock()
	if !stopStarted {
		s.doneOnce.Do(func() { close(s.stopDone) })
	}
}

func (s *scanSession) waitForIssuedStop() {
	s.mutex.Lock()
	stopStarted := s.stopStarted
	s.mutex.Unlock()
	if stopStarted {
		<-s.stopDone
	}
}

func (s *scanSession) stopReason() scanStopReason {
	return scanStopReason(s.reason.Load())
}

func (s *scanSession) durationStopIssuedFlag() bool {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.durationStopIssued
}

// IsConnected returns the current connection status safely.

func ScanForDuration(duration time.Duration) ([]DiscoveredStation, error) {
	return ScanForDurationContext(context.Background(), duration)
}

// ScanForDurationContext performs a blocking BLE scan using the same guarded
// platform scan session as ScanForDuration and stops it when ctx is cancelled.
func ScanForDurationContext(ctx context.Context, duration time.Duration) ([]DiscoveredStation, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		return nil, ErrScanCancelled
	}
	// log.Printf("[BT] ScanForDuration: Starting scan for %v...", duration)
	localStations := make(map[string]DiscoveredStation)
	var localMutex sync.Mutex
	session := newScanSession()
	activeScanMutex.Lock()
	if activeScan != nil {
		activeScanMutex.Unlock()
		return nil, errors.New("Bluetooth scan is already active")
	}
	activeScan = session
	activeScanMutex.Unlock()
	contextWatcherDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			session.requestStopAsync(scanStopCancelled)
		case <-contextWatcherDone:
		}
	}()
	defer func() {
		close(contextWatcherDone)
		activeScanMutex.Lock()
		if activeScan == session {
			activeScan = nil
		}
		activeScanMutex.Unlock()
	}()

	scanCallback := func(adapter *bluetooth.Adapter, result bluetooth.ScanResult) {
		localName := result.LocalName()
		isNamedLighthouse := strings.HasPrefix(localName, "LHB-")
		hasControlService := result.AdvertisementPayload.HasServiceUUID(powerControlServiceUUID)
		if !isNamedLighthouse && !hasControlService {
			return
		}
		addressString := result.Address.String()
		if addressString == "" || addressString == "00:00:00:00:00:00" {
			return
		}
		localMutex.Lock()
		previous, found := localStations[addressString]
		if !found {
			// log.Printf("[BT] Scan: Discovered %s (%s)", result.LocalName(), result.Address.String())
		}
		if localName == "" && found {
			localName = previous.Name
		}
		localStations[addressString] = DiscoveredStation{
			Name:    localName,
			Address: result.Address,
		}
		localMutex.Unlock()
	}

	var stopTimer *time.Timer
	scanStarted := func() {
		session.markStarted()
		stopTimer = time.AfterFunc(duration, func() {
			log.Printf("[BT] ScanForDuration: Duration %v elapsed. Calling StopScan...", duration)
			err := session.requestStop(scanStopDuration)
			if err != nil {
				log.Printf("[BT] ScanForDuration: adapter.StopScan() error: %v", err)
			}
		})
	}

	// Start the blocking scan directly
	log.Println("[BT] ScanForDuration: Calling adapter.Scan()...")
	scanErr := scanSafely(scanCallback, scanStarted)
	session.markFinished()
	session.waitForIssuedStop()
	timerStopped := stopTimer == nil || stopTimer.Stop()
	if stopTimer != nil && !timerStopped {
		// The timer callback has started. Wait for it so a late StopScan cannot
		// accidentally stop a subsequent scan.
		<-session.stopDone
	}

	if scanErr != nil {
		log.Printf("[BT] ScanForDuration (AfterFunc): adapter.Scan() finished with error: %v", scanErr)
	} else {
		log.Println("[BT] ScanForDuration (AfterFunc): adapter.Scan() finished gracefully (likely due to StopScan timer).)")
	}

	// Collect results
	localMutex.Lock()
	results := make([]DiscoveredStation, 0, len(localStations))
	for _, station := range localStations {
		results = append(results, station)
	}
	localMutex.Unlock()

	log.Printf("[BT] ScanForDuration (AfterFunc): Finished. Found %d stations.", len(results))

	reason := session.stopReason()
	if scanErr != nil {
		if err := scanCompletionError(scanErr); err != nil {
			return nil, err
		}
	}
	// A scan whose duration elapsed and whose stop completed cleanly keeps
	// its discovery results even when a cancellation lands in the
	// stop-handshake window: the scan work is already done, so reporting
	// it as cancelled would discard valid stations for no reason.
	if session.durationStopIssuedFlag() && session.stopErr == nil {
		return results, nil
	}
	// A watcher that failed to stop or timed out after a cancellation
	// request must be reported as a failure so callers (HTTP, Wails,
	// status) agree the scan did not complete cleanly.
	if reason == scanStopCancelled || ctx.Err() != nil {
		if session.stopErr != nil {
			return nil, fmt.Errorf("failed to stop Bluetooth scan after cancellation: %w", session.stopErr)
		}
		return nil, ErrScanCancelled
	}
	if reason != scanStopDuration {
		return nil, errors.New("scan stopped before the requested duration completed")
	}
	if session.stopErr != nil {
		return nil, fmt.Errorf("failed to stop Bluetooth scan safely: %w", session.stopErr)
	}
	if err := scanCompletionError(scanErr); err != nil {
		return nil, err
	}
	return results, nil
}

func scanSafely(callback func(*bluetooth.Adapter, bluetooth.ScanResult), started func()) (returnErr error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			returnErr = fmt.Errorf("Bluetooth scan panicked: %v\n%s", recovered, debug.Stack())
		}
	}()
	if startAware, ok := adapter.(interface {
		ScanWithStart(func(*bluetooth.Adapter, bluetooth.ScanResult), func()) error
	}); ok {
		return startAware.ScanWithStart(callback, started)
	}
	started()
	return adapter.Scan(callback)
}

func stopScanSafely() (returnErr error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			returnErr = fmt.Errorf("Bluetooth StopScan panicked: %v\n%s", recovered, debug.Stack())
		}
	}()
	return adapter.StopScan()
}

// CancelScan requests cancellation of an active platform scan. It is used
// during application shutdown and retains the same panic boundary as the
// duration timer.
func CancelScan() error {
	activeScanMutex.Lock()
	session := activeScan
	activeScanMutex.Unlock()
	if session == nil {
		return nil
	}
	return session.requestStop(scanStopCancelled)
}

// RequestScanCancellation records shutdown cancellation and starts StopScan
// when possible without waiting for the WinRT watcher to start or stop.
func RequestScanCancellation() {
	activeScanMutex.Lock()
	session := activeScan
	activeScanMutex.Unlock()
	if session != nil {
		session.requestStopAsync(scanStopCancelled)
	}
}

func scanCompletionError(scanErr error) error {
	if scanErr != nil {
		if IsAdapterUnavailable(scanErr) {
			return fmt.Errorf("Bluetooth is unavailable; turn on Bluetooth or check the adapter, then retry: %w", scanErr)
		}
		return fmt.Errorf("scan failed before the requested duration completed: %w", scanErr)
	}
	return nil
}

// readPowerStateInternalContext performs the actual read and update.
// Assumes caller holds the write lock (station.mutex.Lock()).
