package autosleep

import (
	"context"
	"errors"
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

func TestMonitorCountdownContinuesAcrossReplacement(t *testing.T) {
	base := time.Now()
	original := NewMonitor(5 * time.Minute)

	active, _ := original.Countdown()
	if active {
		t.Fatal("fresh monitor reported an active countdown")
	}

	original.Poll(true, base)
	original.Poll(false, base.Add(time.Minute))
	active, closedAt := original.Countdown()
	if !active || !closedAt.Equal(base.Add(time.Minute)) {
		t.Fatalf("Countdown() = (%v, %v), want an active countdown from %v", active, closedAt, base.Add(time.Minute))
	}

	// A replacement watcher for the same target keeps the pending sleep: the
	// carried countdown fires on the new delay schedule without a fresh
	// running->closed observation.
	replacement := NewMonitorContinuing(2*time.Minute, closedAt)
	if got := replacement.Poll(false, base.Add(2*time.Minute)); got != ActionNone {
		t.Fatalf("poll before the carried delay elapsed = %v, want ActionNone", got)
	}
	if got := replacement.Poll(false, base.Add(3*time.Minute+time.Second)); got != ActionTrigger {
		t.Fatalf("poll after the carried delay elapsed = %v, want ActionTrigger", got)
	}

	// A relaunch still aborts the carried countdown like a fresh one.
	relaunched := NewMonitorContinuing(2*time.Minute, closedAt)
	if got := relaunched.Poll(true, base.Add(90*time.Second)); got != ActionNone {
		t.Fatalf("relaunch during carried countdown = %v, want ActionNone", got)
	}
	if got := relaunched.Poll(false, base.Add(10*time.Minute)); got != ActionNone {
		t.Fatalf("late close after relaunch = %v, want ActionNone", got)
	}
}

// TestMonitorRetainsClosedAtAfterFiring verifies that once the monitor fires it
// still reports the session's close time. applyAutoSleep relies on that to
// carry an owed sleep (a consumed trigger whose action is still running) over
// to a replacement watcher instead of silently dropping it.
func TestMonitorRetainsClosedAtAfterFiring(t *testing.T) {
	base := time.Now()
	monitor := NewMonitor(time.Minute)
	monitor.Poll(true, base)
	monitor.Poll(false, base.Add(30*time.Second))
	if got := monitor.Poll(false, base.Add(2*time.Minute)); got != ActionTrigger {
		t.Fatalf("poll at elapsed delay = %v, want ActionTrigger", got)
	}

	active, closedAt := monitor.Countdown()
	if active {
		t.Fatal("Countdown() reported an active countdown after the trigger fired")
	}
	if want := base.Add(30 * time.Second); !closedAt.Equal(want) {
		t.Fatalf("Countdown() closedAt = %v, want %v retained after firing", closedAt, want)
	}

	// Continuation from that close time is already due, so the replacement
	// watcher re-fires the owed sleep without waiting a fresh delay.
	replacement := NewMonitorContinuing(time.Minute, closedAt)
	if got := replacement.Poll(false, base.Add(2*time.Minute)); got != ActionTrigger {
		t.Fatalf("carried replacement poll = %v, want ActionTrigger (owed sleep re-fires)", got)
	}

	// A relaunched session still cancels the carried countdown, so a station is
	// never slept for a session that started again.
	restarted := NewMonitorContinuing(time.Minute, closedAt)
	if got := restarted.Poll(true, base.Add(2*time.Minute)); got != ActionNone {
		t.Fatalf("relaunch during carried countdown = %v, want ActionNone", got)
	}
}

// TestWatcherContinuesCarriedCountdown verifies that a replacement watcher
// built with a carried monitor fires the pending sleep without observing a
// fresh running->closed session, as after a settings change mid-countdown.
func TestWatcherContinuesCarriedCountdown(t *testing.T) {
	base := time.Now()
	monitor := NewMonitorContinuing(time.Minute, base)
	triggered := make(chan time.Time, 1)
	watcher := &Watcher{
		Settings:  Settings{Enabled: true, Target: string(TargetSteamVR), DelaySeconds: 60},
		Interval:  time.Millisecond,
		IsRunning: func(string) (bool, error) { return false, nil },
		Monitor:   monitor,
		Now:       func() time.Time { return base.Add(2 * time.Minute) },
		Trigger: func(_ context.Context, closedAt time.Time) bool {
			select {
			case triggered <- closedAt:
			default:
			}
			return true
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

	select {
	case closedAt := <-triggered:
		if !closedAt.Equal(base) {
			t.Fatalf("trigger session closedAt = %v, want carried time %v", closedAt, base)
		}
	case <-time.After(time.Second):
		t.Fatal("replacement watcher did not continue the carried countdown")
	}
}

func TestWatcherReplacementPreservesRunningSessionAcrossCancelledPoll(t *testing.T) {
	base := time.Now()
	monitor := NewMonitor(time.Minute)
	monitor.Poll(true, base)
	checkStarted := make(chan struct{})
	releaseCheck := make(chan struct{})
	var startedOnce sync.Once
	watcher := &Watcher{
		Settings: Settings{Enabled: true, Target: string(TargetSteamVR), DelaySeconds: 60},
		// The process check must happen immediately; this deliberately gives the
		// periodic ticker no chance to run during the test.
		Interval: time.Hour,
		Monitor:  monitor,
		IsRunning: func(string) (bool, error) {
			startedOnce.Do(func() { close(checkStarted) })
			<-releaseCheck
			return false, nil
		},
		Trigger: func(context.Context, time.Time) bool { return true },
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		watcher.Run(ctx)
	}()
	select {
	case <-checkStarted:
	case <-time.After(time.Second):
		cancel()
		close(releaseCheck)
		<-done
		t.Fatal("watcher did not sample the process immediately")
	}

	// Reconfigure while the old process check is in flight. The replacement
	// must retain stateRunning; otherwise its first false observation would be
	// treated as idle and this whole session close would be lost.
	cancel()
	replacement := watcher.ReplacementMonitor(2 * time.Minute)
	close(releaseCheck)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancelled watcher did not stop")
	}
	closedAt := base.Add(10 * time.Second)
	if got := replacement.Poll(false, closedAt); got != ActionNone {
		t.Fatalf("replacement close poll = %v, want countdown start", got)
	}
	active, gotClosedAt := replacement.Countdown()
	if !active || !gotClosedAt.Equal(closedAt) {
		t.Fatalf("replacement countdown = active %v closedAt %v, want active at %v", active, gotClosedAt, closedAt)
	}
}

func TestWatcherReplacementRearmsOwedTrigger(t *testing.T) {
	base := time.Now()
	monitor := NewMonitor(time.Minute)
	monitor.Poll(true, base)
	monitor.Poll(false, base.Add(time.Second))
	if got := monitor.Poll(false, base.Add(2*time.Minute)); got != ActionTrigger {
		t.Fatalf("source monitor = %v, want ActionTrigger", got)
	}
	watcher := &Watcher{Settings: Settings{Target: string(TargetSteamVR), DelaySeconds: 60}, Monitor: monitor}
	watcher.markTriggerOwed(true)
	replacement := watcher.ReplacementMonitor(3 * time.Minute)
	active, closedAt := replacement.Countdown()
	if !active || !closedAt.Equal(base.Add(time.Second)) {
		t.Fatalf("owed replacement = active %v closedAt %v", active, closedAt)
	}
}

// TestWatcherOwedSessionSurvivesTargetSwitch covers a watched-target change
// while a sleep debt is unsettled: the replacement watcher cannot reuse the
// old process observation, but the debt must survive and fire on the new
// watcher instead of being dropped until the new target's next full session.
func TestWatcherOwedSessionSurvivesTargetSwitch(t *testing.T) {
	base := time.Now()
	monitor := NewMonitor(time.Minute)
	monitor.Poll(true, base)
	monitor.Poll(false, base.Add(time.Second))
	if got := monitor.Poll(false, base.Add(2*time.Minute)); got != ActionTrigger {
		t.Fatalf("source monitor = %v, want ActionTrigger", got)
	}
	source := &Watcher{Settings: Settings{Target: string(TargetSteamVR), DelaySeconds: 60}, Monitor: monitor}
	source.markTriggerOwed(true)

	owed, closedAt := source.OwedSession()
	if !owed || !closedAt.Equal(base.Add(time.Second)) {
		t.Fatalf("OwedSession() = %v, %v, want owed at %v", owed, closedAt, base.Add(time.Second))
	}

	// Seed the owed session into a watcher for a different target whose
	// monitor starts counting from the old session-close time.
	var ticks atomic.Int64
	var firedClosedAt atomic.Value
	secondRan := make(chan struct{})
	var secondOnce sync.Once
	replacement := NewMonitorContinuing(3*time.Minute, closedAt)
	watcher := &Watcher{
		Settings:  Settings{Enabled: true, Target: string(TargetSteam), DelaySeconds: 180},
		Interval:  time.Millisecond,
		IsRunning: func(string) (bool, error) { return false, nil },
		Now:       func() time.Time { return base.Add(3*time.Minute + time.Duration(ticks.Add(1))*time.Minute) },
		Monitor:   replacement,
		Trigger: func(_ context.Context, gotClosedAt time.Time) bool {
			firedClosedAt.Store(gotClosedAt)
			secondOnce.Do(func() { close(secondRan) })
			return true
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

	select {
	case <-secondRan:
	case <-time.After(2 * time.Second):
		t.Fatal("owed sleep did not fire on the replacement watcher after the target switch")
	}
	got, _ := firedClosedAt.Load().(time.Time)
	if !got.Equal(closedAt) {
		t.Fatalf("replacement trigger closedAt = %v, want the original session close %v", got, closedAt)
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
		Trigger: func(ctx context.Context, _ time.Time) bool {
			close(triggerStarted)
			<-ctx.Done()
			close(triggerCancelled)
			return false
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
		Trigger: func(ctx context.Context, _ time.Time) bool {
			n := calls.Add(1)
			if n == 1 {
				firstOnce.Do(func() { close(firstStarted) })
				<-release
				return true
			}
			close(secondFired)
			return true
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

// TestWatcherOwesTriggerWhileActionInFlight verifies that the watcher reports an
// owed trigger while its sleep action is still running, and clears it once the
// action drains. A settings change (applyAutoSleep) relies on this to re-arm the
// owed sleep instead of losing it when the running action is cancelled.
func TestWatcherOwesTriggerWhileActionInFlight(t *testing.T) {
	var running atomic.Bool
	running.Store(true)
	var ticks atomic.Int64
	base := time.Now()
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseAction := func() { releaseOnce.Do(func() { close(release) }) }
	triggerStarted := make(chan struct{})
	var startOnce sync.Once

	watcher := &Watcher{
		Settings:  Settings{Enabled: true, Target: string(TargetSteamVR), DelaySeconds: 60},
		Interval:  time.Millisecond,
		IsRunning: func(string) (bool, error) { return running.Load(), nil },
		Now:       func() time.Time { return base.Add(time.Duration(ticks.Add(1)) * time.Minute) },
		Monitor:   NewMonitor(time.Minute),
		Trigger: func(ctx context.Context, _ time.Time) bool {
			startOnce.Do(func() { close(triggerStarted) })
			select {
			case <-release:
				return true
			case <-ctx.Done():
				return false
			}
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		watcher.Run(ctx)
	}()
	defer func() {
		releaseAction()
		cancel()
		<-done
	}()

	// Let the watcher observe a running session, then its close.
	time.Sleep(20 * time.Millisecond)
	running.Store(false)
	select {
	case <-triggerStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("watcher did not start the automatic-sleep action")
	}

	// While the action is in flight the owed flag must be set even though the
	// monitor has already consumed its countdown.
	if active, _ := watcher.Monitor.Countdown(); active {
		t.Fatal("countdown still active after the trigger fired")
	}
	if !watcher.OwesTrigger() {
		t.Fatal("OwesTrigger() = false while the sleep action is running")
	}

	// Complete the action; the owed flag must clear once it drains.
	releaseAction()
	deadline := time.Now().Add(2 * time.Second)
	for watcher.OwesTrigger() {
		if time.Now().After(deadline) {
			t.Fatal("OwesTrigger() stayed true after the action completed")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestWatcherTriggerDebtUsesCompletionGeneration(t *testing.T) {
	watcher := &Watcher{}

	first := watcher.markTriggerOwed(true)
	watcher.finishTrigger(first, true)
	if watcher.OwesTrigger() {
		t.Fatal("completed action remained owed before the watcher loop observed its done channel")
	}

	older := watcher.markTriggerOwed(true)
	newer := watcher.markTriggerOwed(true)
	watcher.finishTrigger(older, true)
	if !watcher.OwesTrigger() {
		t.Fatal("older action completion cleared a newer pending trigger")
	}
	watcher.finishTrigger(newer, true)
	if watcher.OwesTrigger() {
		t.Fatal("newest action completion did not clear its trigger debt")
	}
}

// TestWatcherRearmsOwedTriggerAfterUnsettledCancel covers an action cancelled
// before settling while the same watcher keeps running (for example when an
// external scan stop cancels the action's scan phase). The monitor already
// consumed its trigger and returned to idle, so without a re-arm the owed
// sleep would wait forever for a replacement watcher or a brand-new process
// session that may never come.
func TestWatcherRearmsOwedTriggerAfterUnsettledCancel(t *testing.T) {
	var running atomic.Bool
	running.Store(true)
	var ticks atomic.Int64
	base := time.Now()
	firstCancelled := make(chan struct{})
	var cancelOnce sync.Once
	secondRan := make(chan struct{})
	var secondOnce sync.Once
	var calls atomic.Int32

	watcher := &Watcher{
		Settings:  Settings{Enabled: true, Target: string(TargetSteamVR), DelaySeconds: 60},
		Interval:  time.Millisecond,
		IsRunning: func(string) (bool, error) { return running.Load(), nil },
		Now:       func() time.Time { return base.Add(time.Duration(ticks.Add(1)) * time.Minute) },
		Monitor:   NewMonitor(time.Minute),
		Trigger: func(ctx context.Context, _ time.Time) bool {
			if calls.Add(1) == 1 {
				cancelOnce.Do(func() { close(firstCancelled) })
				return false // cancelled before settling: the debt stays owed
			}
			secondOnce.Do(func() { close(secondRan) })
			return true
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

	time.Sleep(20 * time.Millisecond)
	running.Store(false)
	select {
	case <-firstCancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("watcher did not start the automatic-sleep action")
	}
	// The process stays closed and no replacement watcher arrives; the owed
	// sleep must still run exactly once.
	select {
	case <-secondRan:
	case <-time.After(2 * time.Second):
		t.Fatal("watcher did not re-arm the owed trigger after the unsettled cancel")
	}
	time.Sleep(30 * time.Millisecond)
	if got := calls.Load(); got != 2 {
		t.Fatalf("trigger calls = %d, want exactly 2 (re-arm must not loop)", got)
	}
}

// TestCancelledActionKeepsOwedTriggerForReplacement guards the owed-trigger
// race: when a settings change cancels a running sleep action before it
// settles, the action's trigger debt must survive so the replacement watcher
// re-arms the session instead of silently dropping the owed sleep.
func TestCancelledActionKeepsOwedTriggerForReplacement(t *testing.T) {
	base := time.Now()
	monitor := NewMonitor(time.Minute)
	monitor.Poll(true, base)
	monitor.Poll(false, base.Add(time.Second))
	if got := monitor.Poll(false, base.Add(2*time.Minute)); got != ActionTrigger {
		t.Fatalf("source monitor = %v, want ActionTrigger", got)
	}
	watcher := &Watcher{Settings: Settings{Target: string(TargetSteamVR), DelaySeconds: 60}, Monitor: monitor}
	generation := watcher.markTriggerOwed(true)

	// The in-flight action is cancelled before settling.
	watcher.finishTrigger(generation, false)
	if !watcher.OwesTrigger() {
		t.Fatal("cancelled (unsettled) action cleared its trigger debt")
	}

	// The replacement watcher must re-arm the owed session.
	replacement := watcher.ReplacementMonitor(3 * time.Minute)
	active, closedAt := replacement.Countdown()
	if !active || !closedAt.Equal(base.Add(time.Second)) {
		t.Fatalf("replacement after cancelled action = active %v closedAt %v, want armed at %v", active, closedAt, base.Add(time.Second))
	}
}

// TestWatcherStopsAfterConsecutiveCheckFailures covers a persistently failing
// process snapshot (or an unsupported platform): retrying forever only fills
// the log and can never produce a trigger, so the watcher stops after a
// bounded number of consecutive check errors.
func TestWatcherStopsAfterConsecutiveCheckFailures(t *testing.T) {
	watcher := &Watcher{
		Settings: Settings{Enabled: true, Target: string(TargetSteamVR), DelaySeconds: 60},
		Interval: time.Millisecond,
		IsRunning: func(string) (bool, error) {
			return false, errors.New("process snapshot failed")
		},
		Trigger: func(context.Context, time.Time) bool {
			t.Error("a failing process check must never trigger")
			return true
		},
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		watcher.Run(context.Background())
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("watcher did not stop after repeated process check failures")
	}
}
