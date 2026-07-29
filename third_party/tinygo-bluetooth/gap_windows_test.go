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

	err := contextualAsyncCompletionError(ctx, nil, foundation.AsyncStatusCanceled)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("contextualAsyncCompletionError() error = %v, want context.Canceled", err)
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

func TestStopScanCommunicatesWinRTInitializationFailure(t *testing.T) {
	originalEnter := enterWinRTThread
	threadErr := errors.New("apartment unavailable")
	enterWinRTThread = func() (func(), error) { return nil, threadErr }
	t.Cleanup(func() { enterWinRTThread = originalEnter })

	control := &scanControl{stopRequests: make(chan error, 1)}
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
}
