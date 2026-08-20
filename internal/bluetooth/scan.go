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
	// platformDone records that the platform Scan call already delivered its
	// outcome. A duration timer that fires after this moment raced a scan
	// that ended on its own (typically a radio pulled mid-scan delivering a
	// watcher error just before the duration elapsed); it must not latch
	// durationStopIssued, or the real platform failure would be reclassified
	// as a completed duration scan and the adapter-lost handling downstream
	// would never see it.
	platformDone bool
	// abandonGrace is captured from scanAbandonGrace at session creation,
	// mirroring stopWaitLimit, so the scan body's bounded wait on a wedged
	// platform Scan call cannot race a new scan's override of that variable.
	abandonGrace time.Duration
	// startWaitLimit is captured from scanStartWaitLimit at session creation,
	// mirroring the other budgets, so the scan body's bounded wait for the
	// platform watcher to accept a Start cannot race a new scan's override.
	startWaitLimit time.Duration
	// transportSession is the platform scan identity handed over by the
	// started hook. Stops use it to target this session's platform scan
	// directly; a late stop from this session must never resolve against a
	// newer scan that already owns the adapter's global scan slot.
	transportSession bluetooth.ScanSession
}

const defaultScanStopWait = 10 * time.Second

const defaultScanAbandonGrace = 2 * time.Second

const defaultScanStartWait = 10 * time.Second

const defaultScanStartOutcomeGrace = 250 * time.Millisecond

// scanStartOutcomeGrace bounds the extra wait for a platform outcome that
// lands just after the start budget commits. The platform Scan goroutine can
// fail microseconds after the budget branch runs (for example the radio
// became unavailable while the watcher was coming up); without this grace the
// authoritative failure stays unread in the outcome channel and the scan is
// classified as a plain start timeout, skipping the adapter-unavailable
// handling for this cycle. It is a var so tests can shorten it.
var scanStartOutcomeGrace = defaultScanStartOutcomeGrace

// scanStartWaitLimit bounds how long the scan body waits for the platform
// watcher to accept a Start. The WinRT start sequence (watcher creation,
// scanning mode, event registration, Start) can hang when the radio is
// removed or the driver wedges; without a budget the scan body would block
// forever on a scan that never started, keep activeScan set, and turn every
// later scan into "scan is already active" until a process restart. Once the
// budget runs out without a start the session is abandoned the same way a
// wedged post-stop scan is. It is a var so tests can exercise the timeout
// without the production wait; sessions snapshot it at creation for the
// reason documented on the field.
var scanStartWaitLimit = defaultScanStartWait

// scanAbandonGrace bounds how long the scan body waits for the platform Scan
// call to return after the stop handshake has finished or been abandoned. The
// WinRT watcher Stop can hang when the radio is removed mid-scan, which keeps
// the adapter's Scan call blocked too; once the stop side has reached its own
// terminal (or abandoned) state the body gives the platform call this short
// grace to drain, then abandons it and releases the active-scan slot so the
// next scan can start instead of every scan failing until a process restart.
// It is a var so tests can exercise the timeout without the production wait.
var scanAbandonGrace = defaultScanAbandonGrace

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
	grace := scanAbandonGrace
	if grace <= 0 {
		grace = defaultScanAbandonGrace
	}
	startWait := scanStartWaitLimit
	if startWait <= 0 {
		startWait = defaultScanStartWait
	}
	return &scanSession{stopDone: make(chan struct{}), stopWaitLimit: limit, abandonGrace: grace, startWaitLimit: startWait}
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

// scanStartTimeoutError reports a platform scan that never reported a started
// watcher within the start budget. It is deliberately distinct from a stop
// failure: no watcher ever came up, so there was nothing to stop, and the
// session must be abandoned so the active-scan slot is released.
type scanStartTimeoutError struct {
	budget time.Duration
}

func (e *scanStartTimeoutError) Error() string {
	return fmt.Sprintf("Bluetooth scan did not start within %s", e.budget)
}

func isScanStartTimeout(err error) bool {
	var startTimeout *scanStartTimeoutError
	return errors.As(err, &startTimeout)
}

// isWedgedStopError reports a stop outcome that proves the platform teardown
// never completed (a watcher call budget expired instead of watcher.Stop()
// returning). Such stops are treated like abandoned stops: the attempt was
// given up on, so a requested cancellation still reports the cancellation
// instead of the teardown failure.
func isWedgedStopError(err error) bool {
	var stopTimeout *bluetooth.ScanStopTimeoutError
	if errors.As(err, &stopTimeout) {
		return true
	}
	var watcherTimeout *bluetooth.WatcherCallTimeoutError
	return errors.As(err, &watcherTimeout)
}

func (s *scanSession) requestStop(reason scanStopReason) error {
	s.requestStopAsync(reason)
	s.mutex.Lock()
	// A platform watcher has not started yet, so there is no StopScan call to
	// await. markStarted will issue the recorded cancellation when it arrives.
	finished := s.finished
	pendingStart := !s.started && !finished
	s.mutex.Unlock()
	if pendingStart {
		return nil
	}
	if finished {
		// The scan already resolved; its body owns the stop handshake from
		// here. A stop error recorded earlier (an abandoned or wedged stop
		// the body already accounted for) must not surface to a late cancel
		// as if stopping had just failed.
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
	if s.finished || s.platformDone {
		// The scan already ended on its own; recording a stop reason here
		// would misclassify that natural finish as a duration/cancel stop.
		// A platform outcome delivered before this request means the stop
		// raced a scan that terminated on its own: the duration latch in
		// particular would mask the platform error as a completed scan.
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
	// No finished guard here: a session abandoned before the watcher started
	// records its stop intent (abandonStart) and relies on a late markStarted
	// to issue the real stop once the platform accepts the Start. Every other
	// caller is gated by requestStopAsync, which rejects finished sessions, so
	// this only loosens exactly the abandoned-start path.
	if s.stopStarted || !s.started {
		s.mutex.Unlock()
		return
	}
	s.stopStarted = true
	target := s.transportSession
	s.mutex.Unlock()
	go func() {
		err := stopScanSessionSafely(target)
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

// bindTransportSession records the platform scan identity once the platform
// watcher accepts a Start. The started hook runs on the platform scan
// goroutine while stops run on their own goroutines, so the handoff happens
// under the session lock.
func (s *scanSession) bindTransportSession(session bluetooth.ScanSession) {
	s.mutex.Lock()
	s.transportSession = session
	s.mutex.Unlock()
}

func (s *scanSession) markStarted() {
	s.mutex.Lock()
	s.started = true
	// An abandoned session keeps its recorded stop intent: when the platform
	// accepts a Start after the scan body already gave up, the watcher is
	// suddenly live and must be torn down. Gating the stop on !finished here
	// leaked that watcher forever, and every later scan failed with "scan is
	// already active" until a process restart.
	pendingStop := s.reason.Load() != uint32(scanStopNone)
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

// markPlatformDone records that the platform Scan call delivered its outcome
// (with or without an error). The scan goroutine invokes it before publishing
// the outcome on scanDone so the flag is set by the time any consumer can
// observe the result: a duration timer that fires afterwards cannot latch
// durationStopIssued and mask the platform outcome as a completed duration
// scan. Marking after the receive instead left a window in which the timer
// could still latch between the receive and the mark.
func (s *scanSession) markPlatformDone() {
	s.mutex.Lock()
	s.platformDone = true
	s.mutex.Unlock()
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

// abandonStart records that the session is being given up before the platform
// watcher accepted a Start, keeping a stop intent pending so a late-arriving
// Start cannot leave an orphaned watcher scanning forever. Without it the
// transport keeps its watcher registered and every later scan fails with
// "scan is already active" until a process restart. Callers invoke it before
// markFinished.
func (s *scanSession) abandonStart() {
	s.mutex.Lock()
	if scanStopReason(s.reason.Load()) == scanStopNone {
		s.reason.Store(uint32(scanStopCancelled))
	}
	started := s.started
	s.mutex.Unlock()
	if started {
		// The Start raced the budget check and just came up; stop it now.
		s.issueStop()
	}
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
	if ctxErr := ctx.Err(); ctxErr != nil {
		// A caller deadline that expired before the scan could start is a
		// timeout, matching the in-scan classification at the end of this
		// function; callers retry cancellations but treat timeouts as a
		// distinct failure mode. Only an explicit cancellation reports
		// ErrScanCancelled.
		if errors.Is(ctxErr, context.DeadlineExceeded) {
			return nil, fmt.Errorf("Bluetooth scan timed out: %w", context.DeadlineExceeded)
		}
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
			// requestStop (not requestStopAsync) so the bounded stop-handshake
			// wait runs here too: when watcher.Stop() hangs, the abandonment is
			// recorded and stopDone closes, which is what lets the scan body's
			// bounded wait below give up on the wedged platform call.
			if err := session.requestStop(scanStopCancelled); err != nil {
				log.Printf("[BT] ScanForDuration: adapter.StopScan() error: %v", err)
			}
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

	// stopTimer is written by scanStarted, which runs on the platform scan
	// goroutine now that the blocking Scan call lives there, and read by the
	// body afterwards; the mutex keeps the handoff race-free.
	var stopTimerMutex sync.Mutex
	var stopTimer *time.Timer
	scanStarted := func() {
		session.markStarted()
		stopTimerMutex.Lock()
		stopTimer = time.AfterFunc(duration, func() {
			log.Printf("[BT] ScanForDuration: Duration %v elapsed. Calling StopScan...", duration)
			err := session.requestStop(scanStopDuration)
			if err != nil {
				log.Printf("[BT] ScanForDuration: adapter.StopScan() error: %v", err)
			}
		})
		stopTimerMutex.Unlock()
	}

	// Start the blocking scan on its own goroutine: adapter.Scan only returns
	// after the adapter finishes the stop handshake, and a hung watcher.Stop()
	// (the radio removed or reset mid-scan) keeps both of them blocked
	// indefinitely. Waiting on the session's stopDone here instead of on the
	// scan call bounds the scan body by the same stop budget the other waiters
	// already apply, so one wedged StopScan can no longer keep activeScan set
	// and turn every later scan into "scan is already active" until a process
	// restart.
	log.Println("[BT] ScanForDuration: Calling adapter.Scan()...")
	scanDone := make(chan error, 1)
	go func() {
		scanErr := scanSafely(session, scanCallback, scanStarted)
		// Mark the platform outcome delivered before publishing it (see
		// markPlatformDone): a duration timer firing after this moment cannot
		// latch durationStopIssued and reclassify the outcome.
		session.markPlatformDone()
		scanDone <- scanErr
	}()
	var scanErr error
	scanWedged := false
	// The platform watcher's start sequence can wedge on its own (thread
	// initialization, watcher creation, Start). No stop-side budget covers it:
	// nothing has started so there is nothing to stop, stopDone never closes,
	// and the duration timer is only created after a successful start. Without
	// a start watchdog one wedged start blocks this body forever and keeps
	// activeScan set, turning every later scan into "scan is already active"
	// until a process restart. After the budget the session is abandoned the
	// same way a wedged post-stop scan is.
	startWatch := time.NewTimer(session.startWaitLimit)
waitScan:
	for {
		select {
		case scanErr = <-scanDone:
			break waitScan
		case <-session.stopDone:
			// The stop handshake completed or was abandoned. Give the platform
			// scan call a grace to drain its own tail; when it never returns,
			// abandon the blocked goroutine and proceed. The hang stays isolated
			// on the already-resolved watcher and cannot affect a later scan
			// session. A scan whose duration elapsed gets the stop-handshake
			// budget instead of the short abandonment grace: the adapter layer
			// can still be repairing a failed first stop (its retry and drain
			// budgets exceed the abandonment grace), and abandoning early would
			// discard a completed scan's discovery results through the stale
			// first-stop error. Cancelled scans keep the short grace so the
			// active-scan slot frees quickly.
			graceBudget := session.abandonGrace
			if session.durationStopIssuedFlag() {
				graceBudget = session.stopWaitLimit
			}
			grace := time.NewTimer(graceBudget)
			select {
			case scanErr = <-scanDone:
				grace.Stop()
			case <-grace.C:
				scanWedged = true
				log.Printf("[BT] ScanForDuration: platform scan did not finish within %s of the stop handshake; abandoning it", graceBudget)
			}
			break waitScan
		case <-startWatch.C:
			if session.startedFlag() {
				// The watcher accepted a Start right at the deadline. Keep waiting
				// for the normal outcome; the fired timer channel cannot fire again.
				continue
			}
			// Record the stop intent before finishing the session: if the
			// platform Start lands after this abandonment, markStarted must
			// still tear the watcher down instead of orphaning it.
			session.abandonStart()
			scanWedged = true
			select {
			case scanErr = <-scanDone:
				// A platform outcome landed concurrently with the start
				// deadline. The real failure (for example a radio that
				// became unavailable while the watcher was coming up) is
				// the authoritative result and must win over the start
				// timeout so the adapter-unavailable classification still
				// runs; only a nil outcome keeps the timeout.
				if scanErr == nil {
					scanErr = &scanStartTimeoutError{budget: session.startWaitLimit}
				}
			default:
				// A platform failure can land microseconds after this branch
				// commits (the same radio-unavailable race the concurrent
				// branch above handles). Give the outcome one bounded grace so
				// the authoritative failure wins over the start timeout and
				// the adapter-unavailable classification still runs; a wedged
				// platform call that never delivers an outcome keeps the
				// start timeout.
				grace := time.NewTimer(scanStartOutcomeGrace)
				select {
				case scanErr = <-scanDone:
					grace.Stop()
					if scanErr == nil {
						scanErr = &scanStartTimeoutError{budget: session.startWaitLimit}
					}
				case <-grace.C:
					scanErr = &scanStartTimeoutError{budget: session.startWaitLimit}
				}
			}
			log.Printf("[BT] ScanForDuration: platform scan did not start within %s; abandoning it", session.startWaitLimit)
			break waitScan
		}
	}
	startWatch.Stop()
	session.markFinished()
	if !scanWedged {
		session.waitForIssuedStop()
	}
	stopTimerMutex.Lock()
	durationTimer := stopTimer
	stopTimerMutex.Unlock()
	timerStopped := durationTimer == nil || durationTimer.Stop()
	if durationTimer != nil && !timerStopped {
		// The timer callback has started. Wait for it so a late StopScan cannot
		// accidentally stop a subsequent scan. The wait is bounded like every
		// other stop-handshake wait so a hung platform stop cannot wedge the
		// scan subsystem.
		_ = session.awaitStop(session.stopWaitLimit)
	}
	if !scanWedged && scanErr == nil {
		// The adapter-level handshake is authoritative: a clean finish means a
		// failed first stop was repaired by the adapter's own retry, so the
		// stale session record must not poison the classification below. A wedged
		// scan has no authoritative finish, so its abandoned-stop record stands.
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
	abandonedStop := isScanStopAbandoned(stopErr) || isWedgedStopError(stopErr)
	// A scan whose duration elapsed keeps its discovery results no matter how
	// the stop tail finished: the duration fully ran, so discarding valid
	// stations would lose them for no reason. This covers a watcher that
	// lingered in Stopping past the stop budget, a Stopped event that arrived
	// with an error code, and a StopScan call that failed outright (for
	// example the radio was disabled in the same instant the duration ended,
	// so WinRT reports the stop as an error instead of hanging). Discovery
	// completed in every shape; only the teardown did not. This also preserves
	// results when a cancellation lands in the stop-handshake window after the
	// duration already elapsed. Checked before the scanErr early return so
	// that tail-of-stop errors cannot shadow it. An adapter-unavailable
	// platform outcome racing the duration boundary is the exception: keeping
	// the results would hide a lost radio from the classification below and
	// skip the adapter recovery path.
	if session.durationStopIssuedFlag() && (scanErr == nil || !IsAdapterUnavailable(scanErr)) {
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
	// An expired caller context only classifies the outcome as a
	// cancellation or timeout when the scan did not already settle on its
	// own: a platform Scan that returned gracefully before the duration
	// elapsed is the "stopped early" failure even when the deadline lands
	// in the same instant, and reporting it as a benign cancellation
	// would swallow a failure callers should see and retry. A concurrent
	// adapter error keeps the cancellation branch: the watcher may still
	// record its stop, and adapter failures racing a requested stop are
	// swallowed by the cancellation below either way.
	if reason == scanStopCancelled || (ctx.Err() != nil && (reason != scanStopNone || scanErr != nil)) {
		// A platform failure that happened before the watcher ever started is
		// the real outcome of the scan (for example the radio was unavailable
		// and the start failure raced a cancellation): report it instead of a
		// plain cancellation, which would never reach the adapter retry path.
		// An adapter-unavailable failure keeps priority over cancellation even
		// after a start so a pulled or disabled radio is still classified. A
		// start timeout that raced the watcher accepting its Start is the
		// session's real outcome too: the scan never ran its duration window,
		// so reporting a plain cancellation would mislead callers that retry
		// cancellations but treat timeouts as a distinct failure mode.
		if scanErr != nil && (IsAdapterUnavailable(scanErr) || isScanStartTimeout(scanErr) || !session.startedFlag()) {
			if err := scanCompletionError(scanErr); err != nil {
				return nil, err
			}
		}
		// A caller deadline expiring is a timeout, not a user cancellation:
		// the context watcher records both as a cancellation stop, so classify
		// the deadline here. Callers normally retry cancelled work but treat
		// timeouts as a distinct failure mode.
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("Bluetooth scan timed out: %w", context.DeadlineExceeded)
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

func scanSafely(session *scanSession, callback func(*bluetooth.Adapter, bluetooth.ScanResult), started func()) (returnErr error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			returnErr = fmt.Errorf("Bluetooth scan panicked: %v\n%s", recovered, debug.Stack())
		}
	}()
	// A session-aware platform scan hands back its identity so later stops
	// target this exact scan; a platform without that API still stops through
	// the global adapter slot.
	if session != nil {
		if sessionAware, ok := adapter.(interface {
			ScanWithStartSession(func(*bluetooth.Adapter, bluetooth.ScanResult), func(bluetooth.ScanSession)) error
		}); ok {
			return sessionAware.ScanWithStartSession(callback, func(transportSession bluetooth.ScanSession) {
				session.bindTransportSession(transportSession)
				started()
			})
		}
	}
	if startAware, ok := adapter.(interface {
		ScanWithStart(func(*bluetooth.Adapter, bluetooth.ScanResult), func()) error
	}); ok {
		return startAware.ScanWithStart(callback, started)
	}
	started()
	return adapter.Scan(callback)
}

func stopScanSafely() error {
	return stopScanSessionSafely(nil)
}

// stopScanSessionSafely stops the platform scan identified by session. A nil
// session (platform without session support, or a scan that never handed
// back its identity) falls back to the global stop.
func stopScanSessionSafely(session bluetooth.ScanSession) (returnErr error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			returnErr = fmt.Errorf("Bluetooth StopScan panicked: %v\n%s", recovered, debug.Stack())
		}
	}()
	if session != nil {
		if sessionStopper, ok := adapter.(interface {
			StopScanSession(bluetooth.ScanSession) error
		}); ok {
			return settleNotScanning(sessionStopper.StopScanSession(session))
		}
	}
	return settleNotScanning(adapter.StopScan())
}

func settleNotScanning(err error) error {
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
