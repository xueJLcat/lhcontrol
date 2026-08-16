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
	// stopWaitLimit is captured from scanStopWaitLimit at session creation so
	// stop-handshake waiters (including timer goroutines that outlive the test
	// that created them) never race a new scan's override of that variable.
	stopWaitLimit time.Duration
	// durationStopIssued records that the duration timer requested the stop
	// before any cancellation was recorded. Cancellation overwrites reason
	// afterwards, so this latch is what lets a scan that already ran its
	// full duration keep its results when StopScan lands during the
	// stop-handshake window.
	durationStopIssued bool
}

const defaultScanStopWait = 10 * time.Second

// scanStopWaitLimit bounds every wait on the platform stop handshake. The
// WinRT watcher stop can hang when the radio is removed or reset mid-scan;
// without a budget one stuck StopScan would keep activeScan set, wedge every
// later scan until a process restart, and block shutdown callers in
// CancelScan. The stop attempt keeps running in its own goroutine; waiters
// give up on it and the session records the stop as abandoned. It is a var so
// tests can exercise the timeout without waiting out the production budget;
// sessions snapshot it at creation for the reason documented on the field.
var scanStopWaitLimit = defaultScanStopWait

func newScanSession() *scanSession {
	limit := scanStopWaitLimit
	if limit <= 0 {
		limit = defaultScanStopWait
	}
	return &scanSession{stopDone: make(chan struct{}), stopWaitLimit: limit}
}

// scanStopAbandonedError reports a stop handshake that was given up on after
// the bounded wait. It is deliberately distinct from a stop failure reported
// by the adapter so a scan that ran its full duration can keep its discovery
// results even when the watcher teardown never finished.
type scanStopAbandonedError struct {
	budget time.Duration
}

func (e *scanStopAbandonedError) Error() string {
	return fmt.Sprintf("Bluetooth scan stop did not complete within %s", e.budget)
}

func isScanStopAbandoned(err error) bool {
	var abandoned *scanStopAbandonedError
	return errors.As(err, &abandoned)
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
	return s.awaitStop(s.stopWaitLimit)
}

// awaitStop waits for the issued stop to finish, bounded by the budget. On a
// timeout it records the abandonment and releases the waiters exactly once;
// the hung platform call keeps running on its own goroutine but no longer
// holds up the scan session.
func (s *scanSession) awaitStop(budget time.Duration) error {
	timer := time.NewTimer(budget)
	defer timer.Stop()
	select {
	case <-s.stopDone:
	case <-timer.C:
		s.mutex.Lock()
		if s.stopErr == nil {
			s.stopErr = &scanStopAbandonedError{budget: budget}
		}
		s.mutex.Unlock()
		s.doneOnce.Do(func() { close(s.stopDone) })
	}
	return s.stopError()
}

// stopError reads the recorded stop outcome under the session lock. A stop
// abandoned after a wait timeout can still be racing the hung platform
// goroutine, so lock-free reads are not safe.
func (s *scanSession) stopError() error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.stopErr
}

// clearStopError drops a recorded stop failure once the adapter-level stop
// handshake proves the stop eventually succeeded. issueStop records the first
// watcher.Stop() result, but the adapter layer can retry a failed stop and
// reach a clean terminal state afterwards; keeping the stale first error
// would discard a completed duration scan's results and misclassify a clean
// cancellation as a stop failure.
func (s *scanSession) clearStopError() {
	s.mutex.Lock()
	s.stopErr = nil
	s.mutex.Unlock()
}

func (s *scanSession) requestStopAsync(reason scanStopReason) {
	s.mutex.Lock()
	if s.finished {
		// The scan already ended on its own; recording a stop reason here
		// would misclassify that natural finish as a duration/cancel stop.
		s.mutex.Unlock()
		return
	}
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
	shouldStop := s.started
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
		// A bounded waiter can have recorded an abandonment first; whichever
		// finalization lands first owns the outcome, so a late platform result
		// cannot flip a classification that ScanForDurationContext already
		// observed.
		if s.stopErr == nil {
			s.stopErr = err
		}
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
		_ = s.awaitStop(s.stopWaitLimit)
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

// startedFlag reports whether the platform watcher accepted a Start. A scan
// that never started treats its start error as the authoritative outcome
// even when a cancellation was recorded concurrently.
func (s *scanSession) startedFlag() bool {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.started
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
		// accidentally stop a subsequent scan. The wait is bounded like every
		// other stop-handshake wait so a hung platform stop cannot wedge the
		// scan subsystem.
		_ = session.awaitStop(session.stopWaitLimit)
	}
	if scanErr == nil {
		// The adapter-level handshake is authoritative: a clean finish means a
		// failed first stop was repaired by the adapter's own retry, so the
		// stale session record must not poison the classification below.
		session.clearStopError()
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
	stopErr := session.stopError()
	abandonedStop := isScanStopAbandoned(stopErr)
	// A scan whose duration elapsed and whose stop was accepted keeps its
	// discovery results even when the watcher's stop tail reports a final
	// error (for example the watcher lingered in Stopping past the stop
	// budget, or the Stopped event arrived with an error code): the duration
	// fully ran, so discarding valid stations would lose them for no reason.
	// A stop that was abandoned after the bounded wait is treated the same
	// way: the watcher teardown never finished, but the discovery data was
	// collected during the completed duration. This also preserves results
	// when a cancellation lands in the stop-handshake window after the
	// duration already elapsed. Checked before the scanErr early return so
	// that tail-of-stop errors cannot shadow it.
	if session.durationStopIssuedFlag() && (stopErr == nil || abandonedStop) {
		return results, nil
	}
	// A watcher that failed to stop or timed out after a cancellation
	// request must be reported as a failure so callers (HTTP, Wails,
	// status) agree the scan did not complete cleanly. Checked before the
	// scanErr early return: the adapter commonly reports its own tail error
	// (radio disabled or removed mid-stop) while a cancellation is already
	// recorded, and that error must not reclassify the requested stop as a
	// hard scan failure. An abandoned stop is swallowed by the cancellation:
	// the stop attempt was given up on as part of honoring the cancel.
	if reason == scanStopCancelled || ctx.Err() != nil {
		// A platform failure that happened before the watcher ever started is
		// the real outcome of the scan (for example the radio was unavailable
		// and the start failure raced a cancellation): report it instead of a
		// plain cancellation, which would never reach the adapter retry path.
		// An adapter-unavailable failure keeps priority over cancellation even
		// after a start so a pulled or disabled radio is still classified.
		if scanErr != nil && (IsAdapterUnavailable(scanErr) || !session.startedFlag()) {
			if err := scanCompletionError(scanErr); err != nil {
				return nil, err
			}
		}
		if stopErr != nil && !abandonedStop {
			return nil, fmt.Errorf("failed to stop Bluetooth scan after cancellation: %w", stopErr)
		}
		return nil, ErrScanCancelled
	}
	if scanErr != nil {
		if err := scanCompletionError(scanErr); err != nil {
			return nil, err
		}
	}
	if reason != scanStopDuration {
		return nil, errors.New("scan stopped before the requested duration completed")
	}
	if stopErr != nil {
		return nil, fmt.Errorf("failed to stop Bluetooth scan safely: %w", stopErr)
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
	err := adapter.StopScan()
	if errors.Is(err, bluetooth.ErrNotScanning) {
		// The platform watcher already ended on its own (a radio event or a
		// racing stop finished it first). A late stop found no scan to halt,
		// which is the desired end state, not a stop failure.
		return nil
	}
	return err
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

