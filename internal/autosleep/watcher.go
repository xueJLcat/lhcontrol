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

// TriggerFunc runs the configured action when the monitor fires. It receives
// the watcher lifetime context so a long action can abort during shutdown.
type TriggerFunc func(ctx context.Context)

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
	mutex             sync.Mutex
	triggerOwed       bool
	triggerGeneration uint64
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
// remain owed so the watcher runs it afterwards.
func (w *Watcher) finishTrigger(generation uint64) {
	w.mutex.Lock()
	if w.triggerGeneration == generation {
		w.triggerGeneration++
		w.triggerOwed = false
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
	monitor := w.Monitor
	if monitor == nil {
		monitor = NewMonitor(w.Settings.Delay())
	}
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
	stopTrigger := func() {
		if triggerCancel == nil {
			return
		}
		triggerCancel()
		<-triggerDone
		triggerCancel = nil
		triggerDone = nil
	}
	fireTrigger := func(generation uint64) {
		log.Printf("Auto-sleep: %s stayed closed for %s; triggering", processName, w.Settings.Delay())
		triggerContext, cancelTrigger := context.WithCancel(ctx)
		done := make(chan struct{})
		triggerCancel = cancelTrigger
		triggerDone = done
		go func() {
			defer close(done)
			// Clear the consumed debt before publishing completion. A settings
			// replacement can then observe the action as finished even if the
			// watcher loop has not selected the closed done channel yet.
			defer w.finishTrigger(generation)
			w.Trigger(triggerContext)
		}()
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
			running, checkErr := w.IsRunning(processName)
			if checkErr != nil {
				log.Printf("Auto-sleep process check failed: %v", checkErr)
				continue
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
				w.markTriggerOwed(false)
			}
			if monitor.Poll(running, now) == ActionTrigger {
				pendingTrigger = true
				pendingTriggerGeneration = w.markTriggerOwed(true)
			}
			// Fire when a trigger is owed and no action is currently running.
			// If the previous action is still stopping, this stays pending and
			// fires on a later tick once it completes; the running re-check on
			// every tick guarantees a relaunched session is never slept.
			if pendingTrigger && triggerDone == nil && !running {
				pendingTrigger = false
				generation := pendingTriggerGeneration
				pendingTriggerGeneration = 0
				fireTrigger(generation)
			}
		}
	}
}
