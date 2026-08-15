package autosleep

import (
	"context"
	"log"
	"sync"
	"time"
)

// DefaultPollInterval is how often the watcher re-checks the watched
// process. A few seconds keeps trigger latency close to the configured delay
// while making the snapshot cost negligible.
const DefaultPollInterval = 3 * time.Second

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
	// became pending while that action was still draining.
	lifecycleMutex    sync.Mutex
	mutex             sync.Mutex
	triggerOwed       bool
	triggerGeneration uint64
}

// ReplacementMonitor snapshots the complete watched-session state for a new
// watcher with a different delay. Callers cancel this watcher first. The poll
// loop checks that cancellation while holding lifecycleMutex, so a process
// observation cannot be committed after this snapshot and then disappear
// between the old and new watchers.
func (w *Watcher) ReplacementMonitor(delay time.Duration) *Monitor {
	w.lifecycleMutex.Lock()
	defer w.lifecycleMutex.Unlock()
	monitor := w.Monitor
	if monitor == nil {
		monitor = NewMonitor(w.Settings.Delay())
		w.Monitor = monitor
	}
	w.mutex.Lock()
	triggerOwed := w.triggerOwed
	w.mutex.Unlock()
	return monitor.replacement(delay, triggerOwed)
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
// the new target's next full session.
func (w *Watcher) OwedSession() (bool, time.Time) {
	w.mutex.Lock()
	owed := w.triggerOwed
	w.mutex.Unlock()
	if !owed {
		return false, time.Time{}
	}
	w.lifecycleMutex.Lock()
	monitor := w.Monitor
	w.lifecycleMutex.Unlock()
	if monitor == nil {
		return true, time.Time{}
	}
	_, closedAt := monitor.Countdown()
	return true, closedAt
}

func (w *Watcher) markTriggerOwed(owed bool) uint64 {
	w.mutex.Lock()
	w.triggerGeneration++
	w.triggerOwed = owed
	generation := w.triggerGeneration
	w.mutex.Unlock()
	return generation
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
		}
	}
	w.mutex.Unlock()
}

// Run blocks until ctx is cancelled. It is meant to execute in its own
// goroutine; every dependency is injected so the App owns lifecycle and
// error surfacing.
func (w *Watcher) Run(ctx context.Context) {
	interval := w.Interval
	if interval <= 0 {
		interval = DefaultPollInterval
	}
	processName, err := Target(w.Settings.Target).ProcessName()
	if err != nil {
		log.Printf("Auto-sleep watcher not started: %v", err)
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
	poll := func(now time.Time) bool {
		running, checkErr := w.IsRunning(processName)
		if checkErr != nil {
			log.Printf("Auto-sleep process check failed: %v", checkErr)
			return true
		}

		w.lifecycleMutex.Lock()
		if ctx.Err() != nil {
			w.lifecycleMutex.Unlock()
			return false
		}
		// Keep monitoring while the Bluetooth action runs. A new session
		// invalidates the old session's pending sleep immediately, including
		// work that is already scanning or waiting for a GATT slot.
		if running {
			if triggerCancel != nil {
				triggerCancel()
			}
			pendingTrigger = false
			pendingTriggerGeneration = 0
			pendingTriggerClosedAt = time.Time{}
			w.markTriggerOwed(false)
		}
		if monitor.Poll(running, now) == ActionTrigger {
			pendingTrigger = true
			pendingTriggerGeneration = w.markTriggerOwed(true)
			_, pendingTriggerClosedAt = monitor.Countdown()
		}
		// A cancelled action keeps its sleep debt owed, but the monitor has
		// already consumed the trigger and returned to idle. Without re-arming
		// here the owed sleep would wait forever for a brand-new process
		// session that may never come (for example when an external scan stop
		// cancelled the action's scan phase). Re-arm on the next quiet poll;
		// the running branch above still clears the debt if the process
		// relaunches first.
		if !pendingTrigger && triggerDone == nil && !running && w.OwesTrigger() {
			pendingTrigger = true
			pendingTriggerGeneration = w.markTriggerOwed(true)
			_, pendingTriggerClosedAt = monitor.Countdown()
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
