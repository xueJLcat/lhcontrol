package autosleep

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestTargetProcessName(t *testing.T) {
	for _, test := range []struct {
		target  Target
		want    string
		wantErr bool
	}{
		{target: TargetSteam, want: "steam.exe"},
		{target: TargetSteamVR, want: "vrserver.exe"},
		{target: Target("chrome"), wantErr: true},
		{target: Target(""), wantErr: true},
	} {
		got, err := test.target.ProcessName()
		if test.wantErr {
			if err == nil {
				t.Fatalf("ProcessName(%q) unexpectedly succeeded", string(test.target))
			}
			continue
		}
		if err != nil {
			t.Fatalf("ProcessName(%q) error = %v", string(test.target), err)
		}
		if got != test.want {
			t.Fatalf("ProcessName(%q) = %q, want %q", string(test.target), got, test.want)
		}
	}
}

func TestSettingsValidate(t *testing.T) {
	valid := DefaultSettings()
	if err := valid.Validate(); err != nil {
		t.Fatalf("DefaultSettings().Validate() = %v, want nil", err)
	}

	for name, mutate := range map[string]func(*Settings){
		"unknown target": func(s *Settings) { s.Target = "chrome" },
		"empty target":   func(s *Settings) { s.Target = "" },
		"delay too low":  func(s *Settings) { s.DelaySeconds = MinDelaySeconds - 1 },
		"delay too high": func(s *Settings) { s.DelaySeconds = MaxDelaySeconds + 1 },
		"delay zero":     func(s *Settings) { s.DelaySeconds = 0 },
	} {
		settings := DefaultSettings()
		mutate(&settings)
		if err := settings.Validate(); err == nil {
			t.Fatalf("Validate() for %s unexpectedly succeeded", name)
		}
	}
}

func TestMonitorFiresOnlyAfterSeenRunningThenClosed(t *testing.T) {
	base := time.Now()
	monitor := NewMonitor(5 * time.Minute)

	// Never-started process must not fire, no matter how long we wait.
	if got := monitor.Poll(false, base.Add(time.Hour)); got != ActionNone {
		t.Fatalf("Poll(false) without prior session = %v, want ActionNone", got)
	}

	// Session starts.
	if got := monitor.Poll(true, base); got != ActionNone {
		t.Fatalf("Poll(true) = %v, want ActionNone", got)
	}
	// Session ends; delay not yet elapsed.
	if got := monitor.Poll(false, base.Add(time.Minute)); got != ActionNone {
		t.Fatalf("Poll(false) immediately after close = %v, want ActionNone", got)
	}
	if got := monitor.Poll(false, base.Add(4*time.Minute)); got != ActionNone {
		t.Fatalf("Poll(false) before delay = %v, want ActionNone", got)
	}
	// Delay elapsed.
	if got := monitor.Poll(false, base.Add(6*time.Minute)); got != ActionTrigger {
		t.Fatalf("Poll(false) after delay = %v, want ActionTrigger", got)
	}
}

func TestMonitorDoesNotRefireWhileStillClosed(t *testing.T) {
	base := time.Now()
	monitor := NewMonitor(time.Minute)
	monitor.Poll(true, base)
	monitor.Poll(false, base.Add(time.Second))
	if got := monitor.Poll(false, base.Add(2*time.Minute)); got != ActionTrigger {
		t.Fatalf("first trigger = %v, want ActionTrigger", got)
	}
	// Without a new session the monitor stays quiet.
	for _, offset := range []time.Duration{3 * time.Minute, 10 * time.Minute, time.Hour} {
		if got := monitor.Poll(false, base.Add(offset)); got != ActionNone {
			t.Fatalf("Poll(false) at +%s after trigger = %v, want ActionNone", offset, got)
		}
	}
}

func TestMonitorReArmsAfterNewSession(t *testing.T) {
	base := time.Now()
	monitor := NewMonitor(time.Minute)

	monitor.Poll(true, base)
	monitor.Poll(false, base.Add(time.Second))
	if got := monitor.Poll(false, base.Add(2*time.Minute)); got != ActionTrigger {
		t.Fatalf("first trigger = %v, want ActionTrigger", got)
	}

	// Process returns, runs a second session, closes again.
	monitor.Poll(true, base.Add(3*time.Minute))
	monitor.Poll(false, base.Add(4*time.Minute))
	if got := monitor.Poll(false, base.Add(4*time.Minute+30*time.Second)); got != ActionNone {
		t.Fatalf("second session before delay = %v, want ActionNone", got)
	}
	if got := monitor.Poll(false, base.Add(5*time.Minute+10*time.Second)); got != ActionTrigger {
		t.Fatalf("second session after delay = %v, want ActionTrigger", got)
	}
}

func TestMonitorAbortsWhenProcessRelaunchesDuringDelay(t *testing.T) {
	base := time.Now()
	monitor := NewMonitor(5 * time.Minute)
	monitor.Poll(true, base)
	monitor.Poll(false, base.Add(time.Minute))
	// User reopens before the delay elapses.
	if got := monitor.Poll(true, base.Add(2*time.Minute)); got != ActionNone {
		t.Fatalf("relaunch during delay = %v, want ActionNone", got)
	}
	// The earlier close must not fire late: the pending trigger is gone.
	if got := monitor.Poll(false, base.Add(7*time.Minute)); got != ActionNone {
		t.Fatalf("close after relaunch (fresh delay) = %v, want ActionNone", got)
	}
	// And the new session fires on its own schedule.
	if got := monitor.Poll(false, base.Add(13*time.Minute)); got != ActionTrigger {
		t.Fatalf("fresh session after delay = %v, want ActionTrigger", got)
	}
}

func TestMonitorRejectsNonPositiveDelay(t *testing.T) {
	monitor := NewMonitor(0)
	base := time.Now()
	monitor.Poll(true, base)
	monitor.Poll(false, base.Add(time.Second))
	// With the default delay in effect an immediate poll must not fire.
	if got := monitor.Poll(false, base.Add(2*time.Second)); got != ActionNone {
		t.Fatalf("zero-delay fallback fired early: %v", got)
	}
}

func TestWatcherCancelsActiveTriggerWhenProcessRelaunches(t *testing.T) {
	var running atomic.Bool
	running.Store(true)
	base := time.Now()
	var ticks atomic.Int64
	observedRunning := make(chan struct{})
	var observedOnce sync.Once
	triggerStarted := make(chan struct{})
	triggerCancelled := make(chan struct{})
	watcher := &Watcher{
		Settings: Settings{Enabled: true, Target: string(TargetSteamVR), DelaySeconds: 60},
		Interval: time.Millisecond,
		IsRunning: func(string) (bool, error) {
			value := running.Load()
			if value {
				observedOnce.Do(func() { close(observedRunning) })
			}
			return value, nil
		},
		Now: func() time.Time {
			return base.Add(time.Duration(ticks.Add(1)) * time.Minute)
		},
		Trigger: func(ctx context.Context) {
			close(triggerStarted)
			<-ctx.Done()
			close(triggerCancelled)
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		watcher.Run(ctx)
	}()
	defer func() {
		cancel()
		<-done
	}()

	// Let the watcher observe a running session, then its close and delay.
	select {
	case <-observedRunning:
	case <-time.After(time.Second):
		t.Fatal("watcher did not observe the running session")
	}
	running.Store(false)
	select {
	case <-triggerStarted:
	case <-time.After(time.Second):
		t.Fatal("watcher did not start the automatic-sleep action")
	}

	running.Store(true)
	select {
	case <-triggerCancelled:
	case <-time.After(time.Second):
		t.Fatal("relaunch did not cancel the active automatic-sleep action")
	}
}

// TestWatcherDefersTriggerUntilPreviousActionStops covers the case where the
// monitor fires while the previous sleep action is still draining. The new
// trigger must be deferred and then run once the previous action completes,
// instead of being silently dropped (which would leave the stations awake).
func TestWatcherDefersTriggerUntilPreviousActionStops(t *testing.T) {
	var running atomic.Bool
	base := time.Now()
	var step atomic.Int64
	// The first trigger call blocks until released regardless of cancellation,
	// emulating a slow action that is still ending when the next session's
	// delay elapses.
	var calls atomic.Int32
	release := make(chan struct{})
	var releaseOnce sync.Once
	firstStarted := make(chan struct{})
	secondFired := make(chan struct{})
	var firstOnce sync.Once

	watcher := &Watcher{
		Settings: Settings{Enabled: true, Target: string(TargetSteamVR), DelaySeconds: 60},
		Interval: time.Millisecond,
		IsRunning: func(string) (bool, error) {
			return running.Load(), nil
		},
		Now: func() time.Time {
			return base.Add(time.Duration(step.Load()) * time.Minute)
		},
		Trigger: func(ctx context.Context) {
			n := calls.Add(1)
			if n == 1 {
				firstOnce.Do(func() { close(firstStarted) })
				<-release
				return
			}
			close(secondFired)
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		watcher.Run(ctx)
	}()
	defer func() {
		releaseOnce.Do(func() { close(release) })
		cancel()
		<-done
	}()

	// Session one: observe running, close (recording the close timestamp at
	// the current clock), then advance the clock past the delay so the first
	// action starts and blocks.
	running.Store(true)
	time.Sleep(20 * time.Millisecond)
	running.Store(false)
	time.Sleep(20 * time.Millisecond)
	step.Add(1)
	select {
	case <-firstStarted:
	case <-time.After(time.Second):
		t.Fatal("watcher did not start the first automatic-sleep action")
	}

	// Session two while the first action is still ending: relaunch, close
	// again, and elapse the delay. The monitor fires but the previous action
	// has not returned, so the trigger must be deferred rather than dropped.
	running.Store(true)
	time.Sleep(20 * time.Millisecond)
	running.Store(false)
	time.Sleep(20 * time.Millisecond)
	step.Add(1)
	time.Sleep(50 * time.Millisecond)
	if got := calls.Load(); got != 1 {
		t.Fatalf("trigger calls while the previous action was draining = %d, want 1 (deferred, not fired early)", got)
	}

	// Release the first action; the deferred trigger must then run.
	releaseOnce.Do(func() { close(release) })
	select {
	case <-secondFired:
	case <-time.After(time.Second):
		t.Fatal("deferred trigger was dropped instead of running after the previous action stopped")
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("trigger calls after the deferred trigger = %d, want 2", got)
	}
}
