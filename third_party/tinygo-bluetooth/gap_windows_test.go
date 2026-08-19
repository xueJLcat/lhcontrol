//go:build windows

package bluetooth

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	winbluetooth "github.com/saltosystems/winrt-go/windows/devices/bluetooth"
	"github.com/saltosystems/winrt-go/windows/devices/bluetooth/advertisement"
	"github.com/saltosystems/winrt-go/windows/foundation"
)

func TestScanStoppedErrorMapping(t *testing.T) {
	tests := []struct {
		code winbluetooth.BluetoothError
		want error
	}{
		{winbluetooth.BluetoothErrorSuccess, nil},
		{winbluetooth.BluetoothErrorRadioNotAvailable, ErrRadioNotAvailable},
		{winbluetooth.BluetoothErrorResourceInUse, ErrResourceInUse},
		{winbluetooth.BluetoothErrorDisabledByPolicy, ErrDisabledByPolicy},
	}
	for _, test := range tests {
		err := scanStoppedError(test.code)
		if test.want == nil && err != nil {
			t.Fatalf("code %d returned %v", test.code, err)
		}
		if test.want != nil && !errors.Is(err, test.want) {
			t.Fatalf("code %d returned %v, want %v", test.code, err, test.want)
		}
	}
}

func TestWaitForAsyncCompletionHasBoundedCancellationGrace(t *testing.T) {
	originalGrace := asyncCancellationGrace
	originalPoll := asyncStatusPollInterval
	asyncCancellationGrace = 30 * time.Millisecond
	asyncStatusPollInterval = time.Millisecond
	t.Cleanup(func() {
		asyncCancellationGrace = originalGrace
		asyncStatusPollInterval = originalPoll
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cancelCalled := make(chan struct{}, 1)
	started := time.Now()
	_, err := waitForAsyncCompletion(ctx, make(chan foundation.AsyncStatus), func() error {
		cancelCalled <- struct{}{}
		return nil
	}, func() (foundation.AsyncStatus, error) {
		return foundation.AsyncStatusStarted, nil
	})
	var timeoutErr *AsyncOperationTimeoutError
	if !errors.As(err, &timeoutErr) || !errors.Is(err, context.Canceled) {
		t.Fatalf("waitForAsyncCompletion() error = %v, want typed context timeout", err)
	}
	if time.Since(started) > 250*time.Millisecond {
		t.Fatalf("cancellation grace was not bounded: %v", time.Since(started))
	}
	select {
	case <-cancelCalled:
	default:
		t.Fatal("WinRT cancellation hook was not called")
	}
}

func TestWaitForAsyncCompletionAcceptsPolledTerminalStatus(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	status, err := waitForAsyncCompletion(ctx, make(chan foundation.AsyncStatus), func() error { return nil }, func() (foundation.AsyncStatus, error) {
		return foundation.AsyncStatusCanceled, nil
	})
	if err != nil || status != foundation.AsyncStatusCanceled {
		t.Fatalf("waitForAsyncCompletion() = (%d, %v), want canceled status", status, err)
	}
}

func TestContextualAsyncCompletionErrorPreservesCancellationIdentity(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := contextualAsyncCompletionError(ctx.Err(), nil, foundation.AsyncStatusCanceled)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("contextualAsyncCompletionError() error = %v, want context.Canceled", err)
	}
}

func TestContextualAsyncCompletionErrorPreservesBudgetIdentity(t *testing.T) {
	err := contextualAsyncCompletionError(ErrAsyncBudgetExceeded, nil, foundation.AsyncStatusCanceled)
	if !errors.Is(err, ErrAsyncBudgetExceeded) {
		t.Fatalf("contextualAsyncCompletionError() error = %v, want budget error", err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("budget expiry leaked as a caller deadline: %v", err)
	}
}

func TestAsyncTimeoutCauseDistinguishesLibraryBudgetFromCallerDeadlines(t *testing.T) {
	budget, cancelBudget := context.WithCancel(context.Background())
	cancelBudget()
	if got := asyncTimeoutCause(context.Background(), budget, true); !errors.Is(got, ErrAsyncBudgetExceeded) {
		t.Fatalf("injected budget expiry cause = %v, want ErrAsyncBudgetExceeded", got)
	}

	parentCtx, cancelParent := context.WithCancel(context.Background())
	cancelParent()
	child, cancelChild := context.WithCancel(parentCtx)
	cancelChild()
	defer cancelChild()
	if got := asyncTimeoutCause(parentCtx, child, true); !errors.Is(got, context.Canceled) {
		t.Fatalf("parent cancellation cause = %v, want context.Canceled", got)
	}

	deadlineCtx, cancelDeadline := context.WithTimeout(context.Background(), -time.Nanosecond)
	defer cancelDeadline()
	<-deadlineCtx.Done()
	if got := asyncTimeoutCause(context.Background(), deadlineCtx, false); !errors.Is(got, context.DeadlineExceeded) {
		t.Fatalf("caller deadline cause = %v, want context.DeadlineExceeded", got)
	}

	live := context.Background()
	if got := asyncTimeoutCause(live, live, true); got != nil {
		t.Fatalf("unexpired contexts produced cause %v", got)
	}
}

func TestBoundedAsyncOperationContextPreservesTimeoutAndParentCancellation(t *testing.T) {
	originalTimeout := asyncOperationTimeout
	asyncOperationTimeout = 40 * time.Millisecond
	t.Cleanup(func() { asyncOperationTimeout = originalTimeout })

	started := time.Now()
	bounded, cancelBounded := boundedAsyncOperationContext(context.Background())
	defer cancelBounded()
	<-bounded.Done()
	if !errors.Is(bounded.Err(), context.DeadlineExceeded) {
		t.Fatalf("bounded context error = %v, want deadline exceeded", bounded.Err())
	}
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("bounded context elapsed = %v, want no more than 250ms", elapsed)
	}

	parent, cancelParent := context.WithCancel(context.Background())
	child, cancelChild := boundedAsyncOperationContext(parent)
	cancelParent()
	defer cancelChild()
	select {
	case <-child.Done():
		if !errors.Is(child.Err(), context.Canceled) {
			t.Fatalf("child context error = %v, want parent cancellation", child.Err())
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("parent cancellation did not stop bounded context")
	}
}

func TestBoundedAsyncOperationContextKeepsLongerCallerDeadline(t *testing.T) {
	originalTimeout := asyncOperationTimeout
	asyncOperationTimeout = 40 * time.Millisecond
	t.Cleanup(func() { asyncOperationTimeout = originalTimeout })

	parent, cancelParent := context.WithTimeout(context.Background(), time.Minute)
	defer cancelParent()
	child, cancelChild := boundedAsyncOperationContext(parent)
	defer cancelChild()

	select {
	case <-child.Done():
		t.Fatalf("bounded context expired early: %v", child.Err())
	case <-time.After(120 * time.Millisecond):
	}
	deadline, ok := child.Deadline()
	if !ok {
		t.Fatal("bounded context lost the caller deadline")
	}
	if remaining := time.Until(deadline); remaining < 30*time.Second {
		t.Fatalf("caller deadline was shortened: %v remaining", remaining)
	}
}

func TestGATTContextOperationsHonorPreCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name string
		run  func() error
	}{
		{
			name: "connect",
			run: func() error {
				_, err := (&Adapter{}).ConnectContext(ctx, Address{}, ConnectionParams{})
				return err
			},
		},
		{
			name: "service discovery",
			run: func() error {
				_, err := (Device{}).DiscoverServicesContext(ctx, nil)
				return err
			},
		},
		{
			name: "characteristic discovery",
			run: func() error {
				_, err := (DeviceService{}).DiscoverCharacteristicsContext(ctx, nil)
				return err
			},
		},
		{
			name: "read",
			run: func() error {
				_, err := (DeviceCharacteristic{}).ReadContext(ctx, make([]byte, 1))
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.run(); !errors.Is(err, context.Canceled) {
				t.Fatalf("context operation error = %v, want context.Canceled", err)
			}
		})
	}
}

// TestBoundedWatcherCallBoundsWedgedWatcherCalls guards the wedge budget: a
// watcher COM call that never returns must surface as a typed timeout once
// the budget elapses so the scan session can proceed, while a call that
// completes normally passes its result through unchanged.
func TestBoundedWatcherCallBoundsWedgedWatcherCalls(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	started := time.Now()
	err := boundedWatcherCall(30*time.Millisecond, func() error {
		<-release
		return nil
	})
	var timeoutErr *WatcherCallTimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("boundedWatcherCall() error = %v, want a typed timeout", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("bounded call took %v, want the budget", elapsed)
	}

	callErr := errors.New("watcher failure")
	if err := boundedWatcherCall(time.Second, func() error { return callErr }); !errors.Is(err, callErr) {
		t.Fatalf("boundedWatcherCall() error = %v, want %v", err, callErr)
	}
}

// TestBoundedWatcherCallReportsThreadInitFailure guards the apartment guard:
// when the WinRT thread initialization fails, the COM call must not run on
// the unprepared thread (it produces opaque failures there); the helper
// reports the initialization failure instead.
func TestBoundedWatcherCallReportsThreadInitFailure(t *testing.T) {
	originalEnter := enterWinRTThread
	threadErr := errors.New("RoInitialize failed")
	enterWinRTThread = func() (func(), error) { return nil, threadErr }
	t.Cleanup(func() { enterWinRTThread = originalEnter })

	called := false
	err := boundedWatcherCall(time.Second, func() error {
		called = true
		return nil
	})
	if !errors.Is(err, threadErr) {
		t.Fatalf("boundedWatcherCall() error = %v, want %v", err, threadErr)
	}
	if called {
		t.Fatal("watcher COM call ran despite the thread initialization failure")
	}
}

// TestStopScanSessionTargetsItsOwnScan guards session-targeted stops: a stop
// must act on the session's own control instead of the adapter's global scan
// slot, so a delayed stop from an older session can never stop a newer scan
// that already owns the slot.
func TestStopScanSessionTargetsItsOwnScan(t *testing.T) {
	originalEnter := enterWinRTThread
	enterWinRTThread = func() (func(), error) { return func() {}, nil }
	t.Cleanup(func() { enterWinRTThread = originalEnter })

	adapter := &Adapter{}
	// A not-yet-started control records the stop as pending without
	// delivering anything, matching the global stop's deferred contract.
	pending := &scanControl{watcher: &advertisement.BluetoothLEAdvertisementWatcher{}, stopRequests: make(chan error, 1)}
	if err := adapter.StopScanSession(scanSessionToken{control: pending}); err != nil {
		t.Fatalf("StopScanSession() before Start error = %v, want a deferred stop", err)
	}
	pending.mutex.Lock()
	recorded := pending.pendingStop
	pending.mutex.Unlock()
	if !recorded {
		t.Fatal("session stop before Start was not recorded as pending")
	}
	select {
	case err := <-pending.stopRequests:
		t.Fatalf("pending session stop consumed stopRequests with %v", err)
	default:
	}

	// A started control receives its own stop outcome even though the
	// adapter's global slot does not reference it (a newer scan owns the
	// slot). The nil watcher makes watcher.Stop() panic inside the bounded
	// call, standing in for a wedged radio; the recovered error is the
	// delivered outcome.
	startedControl := &scanControl{stopRequests: make(chan error, 1), started: true}
	err := adapter.StopScanSession(scanSessionToken{control: startedControl})
	if err == nil {
		t.Fatal("StopScanSession() on a broken watcher unexpectedly succeeded")
	}
	select {
	case delivered := <-startedControl.stopRequests:
		if delivered != err {
			t.Fatalf("delivered stop outcome = %v, want %v", delivered, err)
		}
	default:
		t.Fatal("session stop did not deliver its outcome to its own scan")
	}

	if err := adapter.StopScanSession(nil); !errors.Is(err, ErrNotScanning) {
		t.Fatalf("StopScanSession(nil) error = %v, want ErrNotScanning", err)
	}
}

func TestWaitForScanStopReturnsStopErrorWithoutStoppedEvent(t *testing.T) {
	originalTimeout := scanStopTimeout
	originalPoll := scanStopPollInterval
	scanStopTimeout = 100 * time.Millisecond
	scanStopPollInterval = time.Millisecond
	t.Cleanup(func() {
		scanStopTimeout = originalTimeout
		scanStopPollInterval = originalPoll
	})

	stopErr := errors.New("stop failed")
	stopRequests := make(chan error, 1)
	stopRequests <- stopErr
	var calls atomic.Int32
	err := waitForScanStop(make(chan error), stopRequests, func() error {
		calls.Add(1)
		return nil
	}, func() (advertisement.BluetoothLEAdvertisementWatcherStatus, error) {
		return advertisement.BluetoothLEAdvertisementWatcherStatusStopped, nil
	})
	if !errors.Is(err, stopErr) {
		t.Fatalf("waitForScanStop() error = %v, want original stop error", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("stop retries = %d, want 0 after terminal status", calls.Load())
	}
}

func TestWaitForScanStopTimesOutAndPreservesStopError(t *testing.T) {
	originalTimeout := scanStopTimeout
	originalPoll := scanStopPollInterval
	scanStopTimeout = 30 * time.Millisecond
	scanStopPollInterval = time.Millisecond
	t.Cleanup(func() {
		scanStopTimeout = originalTimeout
		scanStopPollInterval = originalPoll
	})

	stopErr := errors.New("stop failed")
	stopRequests := make(chan error, 1)
	stopRequests <- stopErr
	err := waitForScanStop(make(chan error), stopRequests, func() error { return stopErr }, func() (advertisement.BluetoothLEAdvertisementWatcherStatus, error) {
		return advertisement.BluetoothLEAdvertisementWatcherStatusStopping, nil
	})
	var timeoutErr *ScanStopTimeoutError
	if !errors.Is(err, stopErr) || !errors.As(err, &timeoutErr) {
		t.Fatalf("waitForScanStop() error = %v, want stop error and typed timeout", err)
	}
}

func TestWaitForScanStopSurfacesLateStoppedEventError(t *testing.T) {
	originalTimeout := scanStopTimeout
	originalPoll := scanStopPollInterval
	scanStopTimeout = time.Second
	scanStopPollInterval = 250 * time.Millisecond
	t.Cleanup(func() {
		scanStopTimeout = originalTimeout
		scanStopPollInterval = originalPoll
	})

	stopped := make(chan error, 1)
	stopRequests := make(chan error, 1)
	stopRequests <- nil
	// Status flips to Stopped before the Stopped event is dispatched; the
	// event carries the real cause (radio removed while draining). The faster
	// status poll must not drop it.
	go func() {
		time.Sleep(20 * time.Millisecond)
		stopped <- ErrRadioNotAvailable
		close(stopped)
	}()
	err := waitForScanStop(stopped, stopRequests, func() error { return nil }, func() (advertisement.BluetoothLEAdvertisementWatcherStatus, error) {
		return advertisement.BluetoothLEAdvertisementWatcherStatusStopped, nil
	})
	if !errors.Is(err, ErrRadioNotAvailable) {
		t.Fatalf("waitForScanStop() error = %v, want the late stopped event error", err)
	}
}

// TestWaitForScanStopDropsStaleStopErrorAfterRetrySucceeds guards the retry
// classification: when the initial stop failed but the forced retry is
// accepted and the watcher reaches a clean Stopped state, the stale initial
// failure must not be reported (callers would otherwise discard a completed
// scan's results).
func TestWaitForScanStopDropsStaleStopErrorAfterRetrySucceeds(t *testing.T) {
	originalTimeout := scanStopTimeout
	originalPoll := scanStopPollInterval
	scanStopTimeout = time.Second
	scanStopPollInterval = 2 * time.Millisecond
	t.Cleanup(func() {
		scanStopTimeout = originalTimeout
		scanStopPollInterval = originalPoll
	})

	stopRequests := make(chan error, 1)
	stopRequests <- errors.New("transient stop failure")
	var retried atomic.Bool
	err := waitForScanStop(make(chan error), stopRequests, func() error {
		retried.Store(true)
		return nil
	}, func() (advertisement.BluetoothLEAdvertisementWatcherStatus, error) {
		if !retried.Load() {
			return advertisement.BluetoothLEAdvertisementWatcherStatusStopping, nil
		}
		return advertisement.BluetoothLEAdvertisementWatcherStatusStopped, nil
	})
	if !retried.Load() {
		t.Fatal("failed stop was never retried")
	}
	if err != nil {
		t.Fatalf("waitForScanStop() error = %v, want a clean stop after the retry succeeded", err)
	}
}

func TestWaitForScanStopFallsBackToStatusWhenEventNeverArrives(t *testing.T) {
	originalTimeout := scanStopTimeout
	originalPoll := scanStopPollInterval
	scanStopTimeout = time.Second
	scanStopPollInterval = 2 * time.Millisecond
	t.Cleanup(func() {
		scanStopTimeout = originalTimeout
		scanStopPollInterval = originalPoll
	})

	stopRequests := make(chan error, 1)
	stopRequests <- nil
	// No Stopped event ever arrives; the terminal status must still finish the
	// wait cleanly after the short event grace window.
	err := waitForScanStop(make(chan error), stopRequests, func() error { return nil }, func() (advertisement.BluetoothLEAdvertisementWatcherStatus, error) {
		return advertisement.BluetoothLEAdvertisementWatcherStatusStopped, nil
	})
	if err != nil {
		t.Fatalf("waitForScanStop() error = %v, want a clean stop", err)
	}
}

// TestWaitForScanStopAcceptsSlowDrainAfterAcceptedStop guards the drain
// budget: a Stop accepted by WinRT can linger in the intermediate Stopping
// state longer than the retry budget for a failed stop. That scan is ending
// cleanly, so the wait must use the longer drain budget instead of reporting
// a stop timeout (which would discard a completed scan's results and release
// a still-draining watcher).
func TestWaitForScanStopAcceptsSlowDrainAfterAcceptedStop(t *testing.T) {
	originalTimeout := scanStopTimeout
	originalDrain := scanStopDrainTimeout
	originalPoll := scanStopPollInterval
	scanStopTimeout = 10 * time.Millisecond
	scanStopDrainTimeout = 2 * time.Second
	scanStopPollInterval = time.Millisecond
	t.Cleanup(func() {
		scanStopTimeout = originalTimeout
		scanStopDrainTimeout = originalDrain
		scanStopPollInterval = originalPoll
	})

	drainedAt := time.Now().Add(50 * time.Millisecond)
	stopRequests := make(chan error, 1)
	stopRequests <- nil // the stop was accepted
	started := time.Now()
	err := waitForScanStop(make(chan error), stopRequests, func() error { return nil }, func() (advertisement.BluetoothLEAdvertisementWatcherStatus, error) {
		if time.Now().Before(drainedAt) {
			return advertisement.BluetoothLEAdvertisementWatcherStatusStopping, nil
		}
		return advertisement.BluetoothLEAdvertisementWatcherStatusStopped, nil
	})
	if err != nil {
		t.Fatalf("waitForScanStop() error = %v, want a clean stop after a slow drain", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("drain wait took %v, want bounded by the drain budget", elapsed)
	}
}

// TestWaitForScanStopExtendsDeadlineWhenRetrySucceeds guards the budget
// switch: when the first Stop fails and a retry is accepted, the watcher
// becomes an ordinarily draining stop and must earn the full drain budget.
// Keeping the short retry budget armed at the start would time out a clean
// slow drain, report a stop timeout for a completed scan, and release a
// watcher that is still stopping.
func TestWaitForScanStopExtendsDeadlineWhenRetrySucceeds(t *testing.T) {
	originalTimeout := scanStopTimeout
	originalDrain := scanStopDrainTimeout
	originalPoll := scanStopPollInterval
	scanStopTimeout = 30 * time.Millisecond
	scanStopDrainTimeout = 2 * time.Second
	scanStopPollInterval = time.Millisecond
	t.Cleanup(func() {
		scanStopTimeout = originalTimeout
		scanStopDrainTimeout = originalDrain
		scanStopPollInterval = originalPoll
	})

	stopRequests := make(chan error, 1)
	stopRequests <- errors.New("transient stop failure")
	drainedAt := time.Now().Add(100 * time.Millisecond)
	var retried atomic.Bool
	started := time.Now()
	err := waitForScanStop(make(chan error), stopRequests, func() error {
		retried.Store(true)
		return nil
	}, func() (advertisement.BluetoothLEAdvertisementWatcherStatus, error) {
		if time.Now().Before(drainedAt) {
			return advertisement.BluetoothLEAdvertisementWatcherStatusStopping, nil
		}
		return advertisement.BluetoothLEAdvertisementWatcherStatusStopped, nil
	})
	if !retried.Load() {
		t.Fatal("failed stop was never retried")
	}
	if err != nil {
		t.Fatalf("waitForScanStop() error = %v, want a clean stop once the retry drained under the extended budget", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("drain wait took %v, want bounded by the extended drain budget", elapsed)
	}
}

// TestRescueLateStartRetriesFailedStopAndReleasesDrainedWatcher covers the
// orphaned-watcher teardown when the late Start self-heals into a running
// watcher: a rejecting first Stop must be retried, and once the accepted stop
// drains to a terminal state the rescue must release the COM reference
// (releaseWatcher abandons a wedged-start watcher, so no other owner frees
// it).
func TestRescueLateStartRetriesFailedStopAndReleasesDrainedWatcher(t *testing.T) {
	originalDrain := scanStopDrainTimeout
	originalPoll := scanStopPollInterval
	scanStopDrainTimeout = time.Second
	scanStopPollInterval = time.Millisecond
	t.Cleanup(func() {
		scanStopDrainTimeout = originalDrain
		scanStopPollInterval = originalPoll
	})

	var stops atomic.Int32
	var released atomic.Bool
	draining := atomic.Bool{}
	status := func() (advertisement.BluetoothLEAdvertisementWatcherStatus, error) {
		if draining.Load() {
			return advertisement.BluetoothLEAdvertisementWatcherStatusStopped, nil
		}
		return advertisement.BluetoothLEAdvertisementWatcherStatusStarted, nil
	}
	runRescueLateStart(status, func() error {
		if stops.Add(1) == 1 {
			return errors.New("radio busy")
		}
		draining.Store(true)
		return nil
	}, func() {
		released.Store(true)
	})

	if stops.Load() != 2 {
		t.Fatalf("stop attempts = %d, want the failed first stop retried once", stops.Load())
	}
	if !released.Load() {
		t.Fatal("drained orphan watcher was not released")
	}
}

// TestRescueLateStartReleasesWatcherThatEndedOnItsOwn covers the rescue
// observing a watcher that reached a terminal state without a stop: the
// reference must be released instead of leaked.
func TestRescueLateStartReleasesWatcherThatEndedOnItsOwn(t *testing.T) {
	var stops atomic.Int32
	var released atomic.Bool
	runRescueLateStart(
		func() (advertisement.BluetoothLEAdvertisementWatcherStatus, error) {
			return advertisement.BluetoothLEAdvertisementWatcherStatusStopped, nil
		},
		func() error {
			stops.Add(1)
			return nil
		},
		func() {
			released.Store(true)
		},
	)
	if stops.Load() != 0 {
		t.Fatalf("stop attempts = %d, want no stop for an already terminal watcher", stops.Load())
	}
	if !released.Load() {
		t.Fatal("terminal orphan watcher was not released")
	}
}

// TestRescueLateStartBoundedWhenStopKeepsFailing guards against an endless
// stop retry loop: a permanently rejecting stop gives up after the attempt
// limit, and nothing is released while the watcher stays Started.
func TestRescueLateStartBoundedWhenStopKeepsFailing(t *testing.T) {
	originalPoll := scanStopPollInterval
	scanStopPollInterval = time.Millisecond
	t.Cleanup(func() { scanStopPollInterval = originalPoll })

	var stops atomic.Int32
	var released atomic.Bool
	runRescueLateStart(
		func() (advertisement.BluetoothLEAdvertisementWatcherStatus, error) {
			return advertisement.BluetoothLEAdvertisementWatcherStatusStarted, nil
		},
		func() error {
			stops.Add(1)
			return errors.New("stop rejected")
		},
		func() {
			released.Store(true)
		},
	)
	if stops.Load() != rescueStopAttempts {
		t.Fatalf("stop attempts = %d, want exactly %d bounded attempts", stops.Load(), rescueStopAttempts)
	}
	if released.Load() {
		t.Fatal("a still-running watcher must not be released")
	}
}

// TestCallbackGateWaitIsBounded guards against a permanently blocked callback
// body pinning scan teardown or device cleanup forever: after the drain limit
// the wait must return even though the callback never ends.
func TestCallbackGateWaitIsBounded(t *testing.T) {
	originalLimit := callbackDrainLimit
	callbackDrainLimit = 30 * time.Millisecond
	t.Cleanup(func() { callbackDrainLimit = originalLimit })

	gate := newCallbackGate()
	if !gate.begin() {
		t.Fatal("open callback gate rejected work")
	}
	gate.close()
	started := time.Now()
	gate.wait()
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("callback gate wait took %v, want bounded by the drain limit", elapsed)
	}
	if gate.begin() {
		gate.end()
		t.Fatal("closed gate admitted late callback after bounded wait")
	}
	gate.end()
}

func TestScheduleCleanupRetrySuppressesDuplicateTimers(t *testing.T) {
	originalEnter := enterWinRTThread
	originalBaseDelay := cleanupRetryBaseDelay
	// Keep the scheduled timer far enough away that it cannot fire while the
	// test asserts the pending state.
	enterWinRTThread = func() (func(), error) { return nil, errors.New("apartment failure") }
	cleanupRetryBaseDelay = time.Hour
	t.Cleanup(func() {
		enterWinRTThread = originalEnter
		cleanupRetryBaseDelay = originalBaseDelay
	})

	state := &deviceState{callbacks: newCallbackGate()}
	device := Device{state: state}
	if err := device.Disconnect(); err == nil {
		t.Fatal("Disconnect() unexpectedly succeeded")
	}
	state.cleanupMutex.Lock()
	pending := state.cleanupRetryPending
	retries := state.cleanupRetries
	state.cleanupMutex.Unlock()
	if !pending {
		t.Fatal("retryable cleanup failure did not mark a pending retry")
	}
	if retries != 1 {
		t.Fatalf("cleanup retries = %d, want 1 after one failed attempt", retries)
	}

	// Interleaved failures must not stack redundant pending timers.
	device.scheduleCleanupRetry(time.Hour)
	device.scheduleCleanupRetry(time.Hour)
	state.cleanupMutex.Lock()
	stillPending := state.cleanupRetryPending
	unchanged := state.cleanupRetries == retries
	state.cleanupMutex.Unlock()
	if !stillPending || !unchanged {
		t.Fatalf("duplicate scheduling changed pending=%v retries=%d", stillPending, state.cleanupRetries)
	}
}

func TestStopScanCommunicatesWinRTInitializationFailure(t *testing.T) {
	originalEnter := enterWinRTThread
	threadErr := errors.New("apartment unavailable")
	enterWinRTThread = func() (func(), error) { return nil, threadErr }
	t.Cleanup(func() { enterWinRTThread = originalEnter })

	control := &scanControl{
		watcher:      &advertisement.BluetoothLEAdvertisementWatcher{},
		stopRequests: make(chan error, 1),
		started:      true,
	}
	adapter := &Adapter{scan: control}
	if err := adapter.StopScan(); !errors.Is(err, threadErr) {
		t.Fatalf("StopScan() error = %v, want %v", err, threadErr)
	}
	select {
	case err := <-control.stopRequests:
		if !errors.Is(err, threadErr) {
			t.Fatalf("communicated error = %v, want %v", err, threadErr)
		}
	default:
		t.Fatal("StopScan did not communicate initialization failure")
	}
}

// TestStopScanThreadFailureBeforeStartIsDeferred guards the deferred-stop
// contract for the thread-failure window: when the watcher has not accepted
// Start, the stop must be recorded as pending (so ScanWithStart delivers the
// real stop result after Start) instead of consuming stopOnce on the thread
// error, which would misreport the scan as failed.
func TestStopScanThreadFailureBeforeStartIsDeferred(t *testing.T) {
	originalEnter := enterWinRTThread
	threadErr := errors.New("apartment unavailable")
	enterWinRTThread = func() (func(), error) { return nil, threadErr }
	t.Cleanup(func() { enterWinRTThread = originalEnter })

	control := &scanControl{
		watcher:      &advertisement.BluetoothLEAdvertisementWatcher{},
		stopRequests: make(chan error, 1),
	}
	adapter := &Adapter{scan: control}
	if err := adapter.StopScan(); err != nil {
		t.Fatalf("StopScan() error = %v, want a deferred stop without error", err)
	}
	control.mutex.Lock()
	pending := control.pendingStop
	control.mutex.Unlock()
	if !pending {
		t.Fatal("stop before Start was not recorded as pending")
	}
	select {
	case err := <-control.stopRequests:
		t.Fatalf("deferred stop consumed stopRequests with %v", err)
	default:
	}
}

func TestStopScanWithoutScanReportsNotScanningOnThreadFailure(t *testing.T) {
	originalEnter := enterWinRTThread
	enterWinRTThread = func() (func(), error) { return nil, errors.New("apartment unavailable") }
	t.Cleanup(func() { enterWinRTThread = originalEnter })

	adapter := &Adapter{}
	if err := adapter.StopScan(); !errors.Is(err, ErrNotScanning) {
		t.Fatalf("StopScan() error = %v, want ErrNotScanning", err)
	}
}

// TestStopWatcherSafelyRecoversPanic guards the panic bound around the retry
// and last-resort watcher.Stop() calls: a panic inside the WinRT call must
// surface as a stop error instead of unwinding the scan (a nil watcher
// dereferences inside the COM call, standing in for a wedged radio panic).
func TestStopWatcherSafelyRecoversPanic(t *testing.T) {
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("stopWatcherSafely leaked a panic: %v", recovered)
		}
	}()
	err := stopWatcherSafely(nil)
	if err == nil {
		t.Fatal("stopWatcherSafely(nil) error = nil, want a recovered panic error")
	}
}

// TestStopScanTerminalScanReportsNotScanningOnThreadFailure guards the teardown
// window: a late idempotent stop that cannot enter its WinRT thread after the
// scan already finished cleanly must report not-scanning, not the unrelated
// thread initialization error.
func TestStopScanTerminalScanReportsNotScanningOnThreadFailure(t *testing.T) {
	originalEnter := enterWinRTThread
	enterWinRTThread = func() (func(), error) { return nil, errors.New("apartment unavailable") }
	t.Cleanup(func() { enterWinRTThread = originalEnter })

	control := &scanControl{
		watcher:      &advertisement.BluetoothLEAdvertisementWatcher{},
		stopRequests: make(chan error, 1),
		started:      true,
		terminal:     true,
	}
	adapter := &Adapter{scan: control}
	if err := adapter.StopScan(); !errors.Is(err, ErrNotScanning) {
		t.Fatalf("StopScan() error = %v, want ErrNotScanning for a finished scan", err)
	}
}

func TestConnectHandlerAccessIsSynchronized(t *testing.T) {
	adapter := &Adapter{}
	handler := func(Device, bool) {}
	for i := 0; i < 100; i++ {
		var group sync.WaitGroup
		group.Add(2)
		go func() {
			defer group.Done()
			adapter.SetConnectHandler(handler)
		}()
		go func() {
			defer group.Done()
			_ = adapter.connectionHandler()
		}()
		group.Wait()
	}
}

func TestDisconnectWaitsForExistingCleanup(t *testing.T) {
	cleanupErr := errors.New("cleanup failed")
	attempt := &deviceCleanupAttempt{done: make(chan struct{})}
	state := &deviceState{
		cleanupStarted: true,
		cleanupAttempt: attempt,
	}
	device := Device{state: state}
	returned := make(chan error, 1)
	go func() {
		returned <- device.Disconnect()
	}()

	select {
	case err := <-returned:
		t.Fatalf("Disconnect returned before cleanup completed: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	state.cleanupMutex.Lock()
	attempt.err = cleanupErr
	state.cleanupComplete = true
	close(attempt.done)
	state.cleanupMutex.Unlock()

	select {
	case err := <-returned:
		if !errors.Is(err, cleanupErr) {
			t.Fatalf("Disconnect error = %v, want %v", err, cleanupErr)
		}
	case <-time.After(time.Second):
		t.Fatal("Disconnect did not return after cleanup completed")
	}
}

func TestDisconnectRetriesAfterWinRTThreadInitializationFailure(t *testing.T) {
	originalEnter := enterWinRTThread
	attempts := 0
	enterWinRTThread = func() (func(), error) {
		attempts++
		if attempts == 1 {
			return nil, errors.New("temporary apartment failure")
		}
		return func() {}, nil
	}
	t.Cleanup(func() { enterWinRTThread = originalEnter })

	state := &deviceState{callbacks: newCallbackGate()}
	device := Device{state: state}
	if err := device.Disconnect(); err == nil {
		t.Fatal("first Disconnect() unexpectedly succeeded")
	}
	if err := device.Disconnect(); err != nil {
		t.Fatalf("second Disconnect() error = %v", err)
	}
	if attempts != 2 {
		t.Fatalf("WinRT initialization attempts = %d, want 2", attempts)
	}
	state.cleanupMutex.Lock()
	complete := state.cleanupComplete
	state.cleanupMutex.Unlock()
	if !complete {
		t.Fatal("successful retry did not mark cleanup complete")
	}
}

func TestRegisterNotificationKeepsExistingRegistrationWhenRemovalFails(t *testing.T) {
	removeErr := errors.New("remove existing notification")
	removed := 0
	released := 0
	state := &deviceState{
		notifications: []notificationRegistration{{
			token: foundation.EventRegistrationToken{Value: 1},
			removeValueChanged: func() error {
				removed++
				return removeErr
			},
			releaseHandler: func() { released++ },
		}},
	}
	device := Device{state: state}

	err := device.registerNotification(notificationRegistration{
		token: foundation.EventRegistrationToken{Value: 2},
	})
	if !errors.Is(err, removeErr) {
		t.Fatalf("registerNotification() error = %v, want %v", err, removeErr)
	}
	if removed != 1 || released != 0 {
		t.Fatalf("existing registration cleanup = (%d removes, %d releases), want (1, 0)", removed, released)
	}
	if len(state.notifications) != 1 || state.notifications[0].token.Value != 1 {
		t.Fatalf("registrations = %+v, want the existing registration retained", state.notifications)
	}
}

func TestRegisterNotificationReplacesExistingAfterSuccessfulRemoval(t *testing.T) {
	removed := 0
	released := 0
	state := &deviceState{
		notifications: []notificationRegistration{{
			token: foundation.EventRegistrationToken{Value: 1},
			removeValueChanged: func() error {
				removed++
				return nil
			},
			releaseHandler: func() { released++ },
		}},
	}
	device := Device{state: state}

	err := device.registerNotification(notificationRegistration{
		token: foundation.EventRegistrationToken{Value: 2},
	})
	if err != nil {
		t.Fatalf("registerNotification() error = %v", err)
	}
	if removed != 1 || released != 1 {
		t.Fatalf("existing registration cleanup = (%d removes, %d releases), want (1, 1)", removed, released)
	}
	if len(state.notifications) != 1 || state.notifications[0].token.Value != 2 {
		t.Fatalf("registrations = %+v, want the replacement registration", state.notifications)
	}
}

func TestRollbackNotificationRetainsRegistrationWhenRemovalFails(t *testing.T) {
	removeErr := errors.New("remove new notification")
	removed := 0
	released := 0
	state := &deviceState{}
	device := Device{state: state}
	registration := notificationRegistration{
		token: foundation.EventRegistrationToken{Value: 3},
		removeValueChanged: func() error {
			removed++
			return removeErr
		},
		releaseHandler: func() { released++ },
	}

	err := device.rollbackNotificationRegistration(registration)
	if !errors.Is(err, removeErr) {
		t.Fatalf("rollbackNotificationRegistration() error = %v, want %v", err, removeErr)
	}
	if removed != 1 || released != 0 {
		t.Fatalf("rollback cleanup = (%d removes, %d releases), want (1, 0)", removed, released)
	}
	if len(state.notifications) != 1 || state.notifications[0].token.Value != 3 {
		t.Fatalf("registrations = %+v, want the failed registration retained", state.notifications)
	}
}

func TestRollbackNotificationReleasesRegistrationAfterSuccessfulRemoval(t *testing.T) {
	removed := 0
	released := 0
	state := &deviceState{}
	device := Device{state: state}
	registration := notificationRegistration{
		token: foundation.EventRegistrationToken{Value: 4},
		removeValueChanged: func() error {
			removed++
			return nil
		},
		releaseHandler: func() { released++ },
	}

	if err := device.rollbackNotificationRegistration(registration); err != nil {
		t.Fatalf("rollbackNotificationRegistration() error = %v", err)
	}
	if removed != 1 || released != 1 {
		t.Fatalf("rollback cleanup = (%d removes, %d releases), want (1, 1)", removed, released)
	}
	if len(state.notifications) != 0 {
		t.Fatalf("registrations = %+v, want no retained registration", state.notifications)
	}
}

func TestDisconnectSchedulesAutomaticRetryAfterWinRTThreadFailure(t *testing.T) {
	originalEnter := enterWinRTThread
	originalBaseDelay := cleanupRetryBaseDelay
	var attempts atomic.Int32
	enterWinRTThread = func() (func(), error) {
		if attempts.Add(1) == 1 {
			return nil, errors.New("temporary apartment failure")
		}
		return func() {}, nil
	}
	cleanupRetryBaseDelay = time.Millisecond
	t.Cleanup(func() {
		enterWinRTThread = originalEnter
		cleanupRetryBaseDelay = originalBaseDelay
	})

	state := &deviceState{callbacks: newCallbackGate()}
	device := Device{state: state}
	if err := device.Disconnect(); err == nil {
		t.Fatal("first Disconnect() unexpectedly succeeded")
	}

	// No further manual Disconnect: the retryable failure must schedule a
	// background attempt instead of leaking the owned handles forever.
	deadline := time.After(2 * time.Second)
	for {
		state.cleanupMutex.Lock()
		complete := state.cleanupComplete
		state.cleanupMutex.Unlock()
		if complete {
			break
		}
		select {
		case <-deadline:
			t.Fatal("automatic cleanup retry did not complete")
		case <-time.After(time.Millisecond):
		}
	}
	if got := attempts.Load(); got < 2 {
		t.Fatalf("cleanup attempts = %d, want at least 2", got)
	}
}

func TestDisconnectPanicAfterOwnershipBeginsDoesNotRetry(t *testing.T) {
	originalEnter := enterWinRTThread
	enterWinRTThread = func() (func(), error) { return func() {}, nil }
	t.Cleanup(func() { enterWinRTThread = originalEnter })

	var cancelCalls int
	state := &deviceState{
		callbacks: newCallbackGate(),
		cancel: func() {
			cancelCalls++
			panic("fixture cancel panic")
		},
	}
	device := Device{state: state}
	firstErr := device.Disconnect()
	if !IsDisconnectCleanupComplete(firstErr) {
		t.Fatalf("Disconnect() error = %v, want completed cleanup warning", firstErr)
	}
	if err := device.Disconnect(); err != firstErr {
		t.Fatalf("second Disconnect() error = %v, want cached %v", err, firstErr)
	}
	if cancelCalls != 1 {
		t.Fatalf("cancel calls = %d, want 1", cancelCalls)
	}
	if state.cancel != nil {
		t.Fatal("cleanup retained detached cancel function")
	}
}

func TestCleanupCallContainsPanicAndConnectionCallbackContinues(t *testing.T) {
	err := cleanupCall("fixture release", func() error {
		panic("fixture panic")
	})
	if err == nil || !strings.Contains(err.Error(), "fixture release panicked") {
		t.Fatalf("cleanupCall() error = %v", err)
	}

	returned := false
	invokeConnectionCallbackSafely(func() {
		panic("handler panic")
	})
	returned = true
	if !returned {
		t.Fatal("connection callback panic escaped its boundary")
	}
}

func TestDisconnectCleanupCompletedErrorPreservesCause(t *testing.T) {
	cause := errors.New("session close warning")
	err := &DisconnectCleanupError{Err: cause}
	if !IsDisconnectCleanupComplete(err) || !errors.Is(err, cause) {
		t.Fatalf("completed cleanup error classification failed: %v", err)
	}
	if IsDisconnectCleanupComplete(cause) {
		t.Fatal("ordinary disconnect failure was classified as completed cleanup")
	}
}

func TestCallbackGateWaitsForActiveCallbackAndRejectsNewWork(t *testing.T) {
	gate := newCallbackGate()
	if !gate.begin() {
		t.Fatal("open callback gate rejected work")
	}
	gate.close()

	waited := make(chan struct{})
	go func() {
		gate.wait()
		close(waited)
	}()
	select {
	case <-waited:
		t.Fatal("callback gate returned while a callback was active")
	case <-time.After(20 * time.Millisecond):
	}

	if gate.begin() {
		t.Fatal("closed callback gate allowed callback work")
	}
	gate.end()
	select {
	case <-waited:
		t.Fatal("callback gate ignored the original active callback")
	case <-time.After(20 * time.Millisecond):
	}

	gate.end()
	select {
	case <-waited:
	case <-time.After(time.Second):
		t.Fatal("callback gate did not finish after callbacks drained")
	}
}

func TestDeviceCleanupLetsAdmittedCallbackLeaveOperationLock(t *testing.T) {
	state := &deviceState{
		callbacks: newCallbackGate(),
	}
	if !state.beginCallback() {
		t.Fatal("callback was not admitted before cleanup")
	}

	callbackReady := make(chan struct{})
	continueCallback := make(chan struct{})
	callbackDone := make(chan struct{})
	go func() {
		defer close(callbackDone)
		defer state.endCallback()
		close(callbackReady)
		<-continueCallback
		state.operationMutex.Lock()
		if !state.closed.Load() {
			t.Error("callback did not observe the closing device")
		}
		state.operationMutex.Unlock()
	}()
	<-callbackReady

	state.closed.Store(true)
	state.blockCallbacks()
	drained := make(chan struct{})
	go func() {
		state.drainCallbacksForCleanup(func() {})
		close(drained)
	}()

	close(continueCallback)
	select {
	case <-callbackDone:
	case <-time.After(time.Second):
		t.Fatal("callback remained blocked behind cleanup operation lock")
	}
	select {
	case <-drained:
		state.operationMutex.Unlock()
	case <-time.After(time.Second):
		t.Fatal("cleanup deadlocked while waiting for admitted callback")
	}
}

func TestCallbackGateConcurrentCloseStress(t *testing.T) {
	for iteration := 0; iteration < 100; iteration++ {
		gate := newCallbackGate()
		var callbacks sync.WaitGroup
		start := make(chan struct{})
		for callback := 0; callback < 8; callback++ {
			callbacks.Add(1)
			go func() {
				defer callbacks.Done()
				<-start
				allowed := gate.begin()
				defer gate.end()
				if allowed {
					time.Sleep(time.Microsecond)
				}
			}()
		}

		close(start)
		gate.close()
		gate.wait()
		callbacks.Wait()
		if gate.begin() {
			gate.end()
			t.Fatalf("iteration %d: closed gate admitted late callback", iteration)
		}
	}
}

func TestHRESULTToErrorPreservesATTTypeAndHRESULT(t *testing.T) {
	const hr = uint32(0x80650001)
	err := hresultToError(hr)

	var attErr AttributeProtocolError
	if !errors.As(err, &attErr) {
		t.Fatalf("error %v does not expose AttributeProtocolError", err)
	}
	if attErr != ErrAttInvalidHandle {
		t.Fatalf("ATT error = 0x%02X, want 0x%02X", attErr.Code(), ErrAttInvalidHandle.Code())
	}
	if !strings.Contains(err.Error(), "0x80650001") {
		t.Fatalf("error %q does not retain HRESULT", err)
	}
}

func TestHRESULTToErrorPreservesUnknownHRESULT(t *testing.T) {
	const hr = uint32(0x80004005)
	err := hresultToError(hr)

	var attErr AttributeProtocolError
	if errors.As(err, &attErr) {
		t.Fatalf("unknown HRESULT unexpectedly classified as ATT: %v", err)
	}
	if !strings.Contains(err.Error(), "0x80004005") {
		t.Fatalf("error %q does not retain HRESULT", err)
	}
	if !errors.Is(err, ErrGATTCommunication) {
		t.Fatalf("unknown HRESULT lost its transport classification: %v", err)
	}
}

func TestHRESULTToErrorMapsKnownTransportHRESULTs(t *testing.T) {
	tests := []struct {
		hr   uint32
		want error
	}{
		{0x80070005, ErrGATTAccessDenied},  // E_ACCESSDENIED
		{0x8007048F, ErrGATTUnreachable},   // ERROR_DEVICE_NOT_CONNECTED
		{0x80004005, ErrGATTCommunication}, // E_FAIL
	}
	for _, test := range tests {
		err := hresultToError(test.hr)
		if !errors.Is(err, test.want) {
			t.Fatalf("HRESULT 0x%08X error = %v, want %v", test.hr, err, test.want)
		}
		var attErr AttributeProtocolError
		if errors.As(err, &attErr) {
			t.Fatalf("HRESULT 0x%08X unexpectedly classified as ATT: %v", test.hr, err)
		}
	}
}

func TestNotificationCallbackCanDisconnectWithoutDeadlock(t *testing.T) {
	originalEnter := enterWinRTThread
	enterWinRTThread = func() (func(), error) { return func() {}, nil }
	t.Cleanup(func() { enterWinRTThread = originalEnter })

	state := &deviceState{callbacks: newCallbackGate()}
	device := Device{state: state}

	// The ValueChanged handler admits the WinRT callback through the gate,
	// copies the payload, and dispatches the user callback outside the gate.
	// A Disconnect issued from that dispatched callback must therefore be
	// able to drain the gate and complete instead of deadlocking.
	if !state.beginCallback() {
		t.Fatal("callback gate rejected the notification")
	}
	dispatched := make(chan error, 1)
	go func() {
		defer state.endCallback()
		go func() {
			defer func() { _ = recover() }()
			dispatched <- device.Disconnect()
		}()
	}()

	select {
	case err := <-dispatched:
		if err != nil {
			t.Fatalf("Disconnect() from notification callback error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Disconnect() from a notification callback deadlocked")
	}
}

// TestBoundedCleanupCallBoundsWedgedCleanupCalls guards the disconnect cleanup
// against a wedged radio or driver: a COM call that never returns must not
// hang the cleanup forever. The helper must give up after the budget, report
// the call as hung (so the caller keeps the COM reference instead of
// releasing it underneath the in-flight call), and let the rest of the
// cleanup proceed.
func TestBoundedCleanupCallBoundsWedgedCleanupCalls(t *testing.T) {
	originalLimit := deviceCleanupCallLimit
	deviceCleanupCallLimit = 30 * time.Millisecond
	t.Cleanup(func() { deviceCleanupCallLimit = originalLimit })
	originalEnter := enterWinRTThread
	enterWinRTThread = func() (func(), error) { return func() {}, nil }
	t.Cleanup(func() { enterWinRTThread = originalEnter })

	release := make(chan struct{})
	defer close(release)
	started := time.Now()
	err, hung := boundedCleanupCall("close wedged session", func() error {
		<-release // simulates a COM call stuck on a wedged driver
		return nil
	})
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("boundedCleanupCall() waited %v, want the budget", elapsed)
	}
	if !hung {
		t.Fatal("boundedCleanupCall() hung = false, want the timed-out call reported as hung")
	}
	if err == nil || !strings.Contains(err.Error(), "close wedged session") {
		t.Fatalf("boundedCleanupCall() error = %v, want the timeout naming the operation", err)
	}
}

func TestBoundedCleanupCallReportsFastOutcomes(t *testing.T) {
	originalEnter := enterWinRTThread
	enterWinRTThread = func() (func(), error) { return func() {}, nil }
	t.Cleanup(func() { enterWinRTThread = originalEnter })

	err, hung := boundedCleanupCall("fast cleanup", func() error { return nil })
	if err != nil || hung {
		t.Fatalf("boundedCleanupCall() error = %v hung = %v, want a clean bounded completion", err, hung)
	}

	cleanupErr := errors.New("close failed")
	err, hung = boundedCleanupCall("failing cleanup", func() error { return cleanupErr })
	if hung || !errors.Is(err, cleanupErr) {
		t.Fatalf("boundedCleanupCall() error = %v hung = %v, want the cleanup failure surfaced", err, hung)
	}
}
