package autosleep

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

// DefaultPollInterval is how often the watcher re-checks the watched
// process. A few seconds keeps trigger latency close to the configured delay
// while making the snapshot cost negligible.
const DefaultPollInterval = 3 * time.Second

// maxConsecutiveCheckErrors bounds how many consecutive process-check
// failures the watcher tolerates before stopping. A persistently failing
// process snapshot (or an unsupported platform) can never produce a trigger,
// and retrying forever only fills the log; stopping surfaces the failure
// instead of silently spinning.
const maxConsecutiveCheckErrors = 10

// IsRunningFunc reports whether the named process is currently running.
type IsRunningFunc func(processName string) (bool, error)

// TriggerFunc runs the configured action when the monitor fires. closedAt
// identifies the watched process session that ended, allowing replacement
// watchers to serialize and de-duplicate the same owed action. The returned
// settled flag reports that the action reached a terminal outcome (completed,
// failed, skipped, or timed out); false means the action was cancelled before
// settling, so the owed sleep must stay owed for a replacement watcher to
// re-arm instead of being cleared by finishTrigger.
type TriggerFunc func(ctx context.Context, closedAt time.Time) (settled bool)

// Watcher polls the target process on an interval and fires Trigger once a
// session (running, then closed) stays closed for the configured delay.
type Watcher struct {
	Settings  Settings
	Interval  time.Duration
	IsRunning IsRunningFunc
	Trigger   TriggerFunc
	// Now is injectable for deterministic lifecycle tests. Production uses
	// time.Now when it is nil.
	Now func() time.Time
	// Monitor optionally supplies the session state machine. When nil, Run
	// creates a fresh one from Settings. Callers that replace a watcher pass
	// a monitor carrying the previous countdown so a pending sleep survives
	// the replacement.
	Monitor *Monitor

	// mutex guards the trigger debt so a replacement watcher can snapshot it
	// while the poll loop and the action goroutine update it. The generation
	// prevents an older action completion from clearing a newer trigger that
	// became pending while that action was still draining. carriedDebt marks a
	// debt inherited from a watcher that watched a different process: the new
	// target running is not a relaunch of that owed session, so the running
	// branch must keep the debt owed until the action settles instead of
	// dropping it. owedClosedAt is the session-close time that identifies the
	// owed debt. It must travel with the debt itself instead of being derived
	// from the monitor at fire time: once a carried watcher's new target runs
	// and stops, the monitor countdown describes the new session, and re-arm
	// fires would otherwise book the old debt under the new session's key.
	lifecycleMutex    sync.Mutex
	mutex             sync.Mutex
	triggerOwed       bool
	triggerGeneration uint64
	carriedDebt       bool
	owedClosedAt      time.Time

	// done closes when Run returns, for any reason. Owners observe it to tell
	// a watcher that stopped on its own (invalid target, persistent
	// process-check failures) apart from one that was cancelled by a
	// replacement or shutdown; without that signal a self-stopped watcher
	// keeps looks-alive checks passing and the feature never rebuilds.
	done chan struct{}
	// exitErr records why Run stopped; nil means the context was cancelled.
	exitErr error
}

// ReplacementMonitor snapshots the complete watched-session state for a new
// watcher with a different delay. Callers cancel this watcher first. The poll
// loop checks that cancellation while holding lifecycleMutex, so a process
// observation cannot be committed after this snapshot and then disappear
// between the old and new watchers. The second return reports whether the
// unsettled debt (if any) was carried over from a different watched process;
// the replacement seeds SeedOwedSession in that case so the debt keeps its
// carry semantics across the replacement.
func (w *Watcher) ReplacementMonitor(delay time.Duration) (*Monitor, bool) {
	w.lifecycleMutex.Lock()
	defer w.lifecycleMutex.Unlock()
	monitor := w.Monitor
	if monitor == nil {
		monitor = NewMonitor(w.Settings.Delay())
		w.Monitor = monitor
	}
	w.mutex.Lock()
	triggerOwed := w.triggerOwed
	carried := w.carriedDebt && triggerOwed
	w.mutex.Unlock()
	return monitor.replacement(delay, triggerOwed), carried
}

// MonitorCountdown reports the monitor's in-flight session-close countdown,
// if any, for a watcher that stopped on its own. It reads the monitor through
// the same lifecycle lock ReplacementMonitor writes under, so a concurrent
// settings replacement cannot race the snapshot.
func (w *Watcher) MonitorCountdown() (active bool, closedAt time.Time) {
	w.lifecycleMutex.Lock()
	defer w.lifecycleMutex.Unlock()
	if w.Monitor == nil {
		return false, time.Time{}
	}
	return w.Monitor.Countdown()
}

// SeedOwedSession seeds an unsettled sleep debt inherited from a watcher for
// a different watched process (a target change). The monitor countdown cannot
// express the debt (it belongs to the action bookkeeping, and the new
// target's observations must not re-derive it), so the replacement watcher is
// seeded directly: while the new target runs, the debt stays owed instead of
// being cleared as a relaunched session, and the re-arm path fires it once
// the new target stops running. closedAt identifies the owed session so the
// re-arm fire and downstream de-duplication keep using the debt's own
// session key even after the new target's observations replace the monitor
// state. Call before Run.
func (w *Watcher) SeedOwedSession(closedAt time.Time) {
	w.mutex.Lock()
	w.triggerGeneration++
	w.triggerOwed = true
	w.carriedDebt = true
	w.owedClosedAt = closedAt
	w.mutex.Unlock()
}

// Done returns a channel closed once Run returns, whether the context was
// cancelled, the target was invalid, or the watcher stopped itself after too
// many process-check failures. A watcher never restarts; the owner must apply
// settings again to build a replacement.
func (w *Watcher) Done() <-chan struct{} {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	if w.done == nil {
		w.done = make(chan struct{})
	}
	return w.done
}

// ExitErr reports why Run stopped: nil means the context was cancelled; a
// non-nil error means the watcher gave up on its own. Only meaningful after
// Done closes.
func (w *Watcher) ExitErr() error {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	return w.exitErr
}

// markExited closes Done exactly once and records the exit reason.
func (w *Watcher) markExited(reason error) {
	w.mutex.Lock()
	if w.done == nil {
		w.done = make(chan struct{})
	}
	if reason != nil {
		w.exitErr = reason
	}
	close(w.done)
	w.mutex.Unlock()
}

// OwesTrigger reports that a consumed trigger's sleep has not completed yet:
// the action is running or waits for a previous action to finish. A
// replacement watcher uses it to re-arm its countdown instead of silently
// dropping the owed sleep when the in-flight action is cancelled.
func (w *Watcher) OwesTrigger() bool {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	return w.triggerOwed
}

// OwedSession reports the consumed-trigger debt: whether an owed sleep is
// still unsettled and the session-close time that identifies it. A watcher
// replacing this one for a different watched process cannot reuse the
// monitor's observation state (running/countdown belongs to the old
// process), but the debt itself does not depend on which process is watched
// next and must survive the switch instead of being silently dropped until
// the new target's next full session. The reported close time is the debt's
// own key, never the monitor's current countdown: after the new target runs
// and stops, the monitor countdown belongs to that new session and must not
// re-key the carried debt.
func (w *Watcher) OwedSession() (bool, time.Time) {
	// The entire snapshot runs under lifecycleMutex, matching
	// ReplacementMonitor: the poll loop checks cancellation and commits its
	// observation under the same lock, so no observation can land on the
	// dying watcher after this read and disappear before the replacement
	// starts. Reading the debt under a separate lock first left a window
	// where a consumed trigger committed just after the read and its owed
	// sleep was silently dropped by the target switch.
	w.lifecycleMutex.Lock()
	defer w.lifecycleMutex.Unlock()
	w.mutex.Lock()
	defer w.mutex.Unlock()
	if !w.triggerOwed {
		return false, time.Time{}
	}
	return true, w.owedClosedAt
}

func (w *Watcher) markTriggerOwed(owed bool, closedAt time.Time) uint64 {
	w.mutex.Lock()
	w.triggerGeneration++
	w.triggerOwed = owed
	w.owedClosedAt = closedAt
	generation := w.triggerGeneration
	w.mutex.Unlock()
	return generation
}

// owedSessionClosedAt reports the session-close key recorded with the debt.
// Callers hold lifecycleMutex; the read takes the shorter debt lock.
func (w *Watcher) owedSessionClosedAt() time.Time {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	return w.owedClosedAt
}

// finishTrigger clears only the debt consumed by this action. A new session
// can close while an older action is still stopping; its newer generation must
// remain owed so the watcher runs it afterwards. An action that was cancelled
// before settling (settled false) never resolved its session's owed sleep, so
// its debt stays owed: a replacement watcher re-arms the countdown, and a
// relaunched process clears the debt through the next poll instead.
func (w *Watcher) finishTrigger(generation uint64, settled bool) {
	w.mutex.Lock()
	if w.triggerGeneration == generation {
		w.triggerGeneration++
		if settled {
			w.triggerOwed = false
			w.carriedDebt = false
			w.owedClosedAt = time.Time{}
		}
	}
	w.mutex.Unlock()
}

// clearSessionDebtUnlessCarried clears the debt a relaunched session
// invalidates, but leaves a carried debt owed: another process running is not
// a relaunch of the session that debt belongs to, and dropping it there would
// silently strand the stations awake on every target switch.
func (w *Watcher) clearSessionDebtUnlessCarried() {
	w.mutex.Lock()
	if !w.carriedDebt {
		w.triggerGeneration++
		w.triggerOwed = false
		w.owedClosedAt = time.Time{}
	}
	w.mutex.Unlock()
}

// Run blocks until ctx is cancelled. It is meant to execute in its own
// goroutine; every dependency is injected so the App owns lifecycle and
// error surfacing.
func (w *Watcher) Run(ctx context.Context) {
	var exitErr error
	defer func() { w.markExited(exitErr) }()
	interval := w.Interval
	if interval <= 0 {
		interval = DefaultPollInterval
	}
	processName, err := Target(w.Settings.Target).ProcessName()
	if err != nil {
		log.Printf("Auto-sleep watcher not started: %v", err)
		exitErr = err
		return
	}
	w.lifecycleMutex.Lock()
	monitor := w.Monitor
	if monitor == nil {
		monitor = NewMonitor(w.Settings.Delay())
		w.Monitor = monitor
	}
	w.lifecycleMutex.Unlock()
	log.Printf("Auto-sleep watcher started: watching %s, delay %s", processName, w.Settings.Delay())
	defer log.Println("Auto-sleep watcher stopped")

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var triggerCancel context.CancelFunc
	var triggerDone <-chan struct{}
	// pendingTrigger records that the monitor fired (and consumed) a trigger
	// while the previous action was still stopping, so the sleep runs as soon
	// as the previous action finishes instead of being silently dropped.
	pendingTrigger := false
	var pendingTriggerGeneration uint64
	var pendingTriggerClosedAt time.Time
	stopTrigger := func() {
		if triggerCancel == nil {
			return
		}
		triggerCancel()
		<-triggerDone
		triggerCancel = nil
		triggerDone = nil
	}
	fireTrigger := func(generation uint64, closedAt time.Time) {
		log.Printf("Auto-sleep: %s stayed closed for %s; triggering", processName, w.Settings.Delay())
		triggerContext, cancelTrigger := context.WithCancel(ctx)
		done := make(chan struct{})
		triggerCancel = cancelTrigger
		triggerDone = done
		go func() {
			defer close(done)
			// Settle the consumed debt before publishing completion. A settings
			// replacement can then observe the action as finished even if the
			// watcher loop has not selected the closed done channel yet. A
			// cancelled action reports settled=false and keeps the debt owed so
			// the replacement re-arms it instead of dropping the session.
			settled := w.Trigger(triggerContext, closedAt)
			w.finishTrigger(generation, settled)
		}()
	}
	consecutiveCheckErrors := 0
	poll := func(now time.Time) bool {
		running, checkErr := w.IsRunning(processName)
		if checkErr != nil {
			consecutiveCheckErrors++
			if consecutiveCheckErrors >= maxConsecutiveCheckErrors {
				log.Printf("Auto-sleep watcher stopping after %d consecutive process check failures: %v", consecutiveCheckErrors, checkErr)
				exitErr = fmt.Errorf(
					"stopped after %d consecutive process check failures: %w",
					consecutiveCheckErrors, checkErr,
				)
				return false
			}
			log.Printf("Auto-sleep process check failed: %v", checkErr)
			return true
		}
		consecutiveCheckErrors = 0

		w.lifecycleMutex.Lock()
		if ctx.Err() != nil {
			w.lifecycleMutex.Unlock()
			return false
		}
		// Keep monitoring while the Bluetooth action runs. A new session
		// invalidates the old session's pending sleep immediately, including
		// work that is already scanning or waiting for a GATT slot. A debt
		// carried over from a different watched process stays owed: this
		// process is not a relaunch of that session, and the re-arm below
		// fires the debt once this process stops running.
		if running {
			if triggerCancel != nil {
				triggerCancel()
			}
			pendingTrigger = false
			pendingTriggerGeneration = 0
			pendingTriggerClosedAt = time.Time{}
			w.clearSessionDebtUnlessCarried()
		}
		if monitor.Poll(running, now) == ActionTrigger {
			pendingTrigger = true
			_, pendingTriggerClosedAt = monitor.Countdown()
			pendingTriggerGeneration = w.markTriggerOwed(true, pendingTriggerClosedAt)
		}
		// A cancelled action keeps its sleep debt owed, but the monitor has
		// already consumed the trigger and returned to idle. Without re-arming
		// here the owed sleep would wait forever for a brand-new process
		// session that may never come (for example when an external scan stop
		// cancelled the action's scan phase). Re-arm on the next quiet poll;
		// the running branch above still clears the debt if the process
		// relaunches first. The fire uses the debt's own session key instead
		// of the monitor countdown: after a carried debt's new target runs
		// and stops, the countdown describes that new session, and re-keying
		// the debt by it would double-book the new session. The fire also
		// consumes any armed countdown: it belongs to the same stopped session
		// this fire is settling, and leaving it armed would fire a second,
		// redundant sleep once the delay passes. A fire cancelled before
		// settling keeps the debt owed, so the re-arm still runs later.
		if !pendingTrigger && triggerDone == nil && !running && w.OwesTrigger() {
			pendingTrigger = true
			pendingTriggerClosedAt = w.owedSessionClosedAt()
			pendingTriggerGeneration = w.markTriggerOwed(true, pendingTriggerClosedAt)
			monitor.consumeActiveCountdown()
		}
		// Fire when a trigger is owed and no action is currently running.
		// If the previous action is still stopping, this stays pending and
		// fires on a later tick once it completes; the running re-check on
		// every tick guarantees a relaunched session is never slept.
		shouldFire := pendingTrigger && triggerDone == nil && !running
		generation := pendingTriggerGeneration
		closedAt := pendingTriggerClosedAt
		if shouldFire {
			pendingTrigger = false
			pendingTriggerGeneration = 0
			pendingTriggerClosedAt = time.Time{}
		}
		w.lifecycleMutex.Unlock()

		if shouldFire {
			fireTrigger(generation, closedAt)
		}
		return true
	}
	// Establish the running-session baseline immediately. Waiting for the first
	// ticker interval could miss a process that exits just after the watcher is
	// enabled or replaced.
	initialNow := time.Now()
	if w.Now != nil {
		initialNow = w.Now()
	}
	if !poll(initialNow) {
		stopTrigger()
		return
	}
	for {
		select {
		case <-ctx.Done():
			stopTrigger()
			return
		case <-triggerDone:
			triggerCancel()
			triggerCancel = nil
			triggerDone = nil
			// A trigger deferred while the previous action drained must fire
			// as soon as that action completes; waiting for the next tick
			// would add up to a full poll interval of dead time.
			if pendingTrigger {
				now := time.Now()
				if w.Now != nil {
					now = w.Now()
				}
				if !poll(now) {
					stopTrigger()
					return
				}
			}
		case now := <-ticker.C:
			if w.Now != nil {
				now = w.Now()
			}
			if !poll(now) {
				stopTrigger()
				return
			}
		}
	}
}
