package autosleep

import (
	"context"
	"log"
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
	monitor := NewMonitor(w.Settings.Delay())
	log.Printf("Auto-sleep watcher started: watching %s, delay %s", processName, w.Settings.Delay())
	defer log.Println("Auto-sleep watcher stopped")

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var triggerCancel context.CancelFunc
	var triggerDone <-chan struct{}
	stopTrigger := func() {
		if triggerCancel == nil {
			return
		}
		triggerCancel()
		<-triggerDone
		triggerCancel = nil
		triggerDone = nil
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
			if running && triggerCancel != nil {
				triggerCancel()
			}
			if monitor.Poll(running, now) != ActionTrigger {
				continue
			}
			if triggerDone != nil {
				log.Printf("Auto-sleep trigger ignored because the previous action is still stopping")
				continue
			}
			log.Printf("Auto-sleep: %s stayed closed for %s; triggering", processName, w.Settings.Delay())
			triggerContext, cancelTrigger := context.WithCancel(ctx)
			done := make(chan struct{})
			triggerCancel = cancelTrigger
			triggerDone = done
			go func() {
				defer close(done)
				w.Trigger(triggerContext)
			}()
		}
	}
}
