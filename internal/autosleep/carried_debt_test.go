package autosleep

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCarriedDebtKeepsSessionWhenNewCountdownExpiresWhileActionDrains(t *testing.T) {
	oldClose := time.Now().Add(-time.Hour)
	var offset atomic.Int64
	offset.Store(int64(10 * time.Second))
	var running atomic.Bool
	fired := make(chan time.Time, 2)
	cancelObserved := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	var calls atomic.Int32
	watcher := &Watcher{
		Settings:  Settings{Enabled: true, Target: string(TargetSteam), DelaySeconds: 60},
		Interval:  time.Millisecond,
		Monitor:   NewMonitorContinuing(time.Minute, oldClose),
		Now:       func() time.Time { return oldClose.Add(time.Duration(offset.Load())) },
		IsRunning: func(string) (bool, error) { return running.Load(), nil },
		Trigger: func(ctx context.Context, closedAt time.Time) bool {
			fired <- closedAt
			if calls.Add(1) == 1 {
				<-ctx.Done()
				close(cancelObserved)
				<-release
				return false
			}
			return true
		},
	}
	watcher.SeedOwedSession(oldClose)
	ctx, cancel := context.WithCancel(context.Background())
	go watcher.Run(ctx)
	defer func() {
		releaseOnce.Do(func() { close(release) })
		cancel()
		select {
		case <-watcher.Done():
		case <-time.After(2 * time.Second):
			t.Error("watcher failed to stop")
		}
	}()
	assertFire := func() {
		t.Helper()
		select {
		case got := <-fired:
			if !got.Equal(oldClose) {
				t.Fatalf("trigger session = %v, want inherited session %v", got, oldClose)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("owed sleep did not fire")
		}
	}
	waitFor := func(condition func() bool) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for !condition() {
			if time.Now().After(deadline) {
				t.Fatal("monitor did not reach expected countdown state")
			}
			time.Sleep(time.Millisecond)
		}
	}
	assertFire()
	running.Store(true)
	select {
	case <-cancelObserved:
	case <-time.After(2 * time.Second):
		t.Fatal("new target did not cancel the old action")
	}
	offset.Store(int64(20 * time.Second))
	running.Store(false)
	waitFor(func() bool { active, _ := watcher.MonitorCountdown(); return active })
	offset.Store(int64(2 * time.Minute))
	waitFor(func() bool { active, _ := watcher.MonitorCountdown(); return !active })
	if owed, key := watcher.OwedSession(); !owed || !key.Equal(oldClose) {
		t.Fatalf("new countdown replaced inherited debt: owed=%v key=%v, want %v", owed, key, oldClose)
	}
	releaseOnce.Do(func() { close(release) })
	assertFire()
}
